// Package domainssl resolves two "shelf-life" facts that sysadmins care about
// for every web-facing monitor: when the TLS certificate expires, and when the
// domain registration expires.
//
// SSL is read directly from the live TLS handshake (no third party). Domain
// expiry is read from public WHOIS servers over TCP port 43 — we first ask
// IANA which WHOIS server owns the TLD, then query that server and parse the
// expiration field. Everything degrades gracefully: on any network/parse
// failure the caller gets a zero value plus a human-readable Error string so
// the UI can show "—" instead of breaking.
package domainssl

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/doh"
)

// SSLInfo describes the leaf certificate presented by host:port.
type SSLInfo struct {
	Subject       string     `json:"subject"`
	Issuer        string     `json:"issuer"`
	NotBefore     *time.Time `json:"notBefore,omitempty"`
	NotAfter      *time.Time `json:"notAfter,omitempty"`
	DaysRemaining int        `json:"daysRemaining"`
	OK            bool       `json:"ok"`
	Error         string     `json:"error,omitempty"`
}

// DomainInfo describes the registration expiry parsed from WHOIS.
type DomainInfo struct {
	Registrar     string     `json:"registrar,omitempty"`
	ExpiryDate    *time.Time `json:"expiryDate,omitempty"`
	DaysRemaining *int       `json:"daysRemaining,omitempty"`
	OK            bool       `json:"ok"`
	Error         string     `json:"error,omitempty"`
}

// CheckSSL connects to host:port (default 443) and returns the leaf cert info.
func CheckSSL(ctx context.Context, host, port string, timeout time.Duration) SSLInfo {
	if port == "" {
		port = "443"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// DoH-first dial: local resolvers may return sinkholed IPs for blocked
	// hosts; TLS verification still uses the real hostname via ServerName.
	d := doh.NewDialer(timeout)
	rawConn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return SSLInfo{Error: err.Error()}
	}
	conn := tls.Client(rawConn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: false,
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return SSLInfo{Error: err.Error()}
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return SSLInfo{Error: "no certificate presented"}
	}
	leaf := certs[0]
	days := int(time.Until(leaf.NotAfter).Hours() / 24)
	nb, na := leaf.NotBefore, leaf.NotAfter
	return SSLInfo{
		Subject:       leaf.Subject.CommonName,
		Issuer:        leaf.Issuer.CommonName,
		NotBefore:     &nb,
		NotAfter:      &na,
		DaysRemaining: days,
		OK:            true,
	}
}

// CheckDomainExpiry runs a WHOIS lookup for domain and parses its expiry.
func CheckDomainExpiry(ctx context.Context, domain string, timeout time.Duration) DomainInfo {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return DomainInfo{Error: "no domain"}
	}
	tld := registrableTLD(domain)
	if tld == "" {
		return DomainInfo{Error: "cannot determine TLD"}
	}

	// 1) Ask IANA which WHOIS server is authoritative for this TLD.
	iana, err := whoisQuery(ctx, "whois.iana.org", tld, timeout)
	if err != nil {
		return DomainInfo{Error: "iana whois failed: " + err.Error()}
	}
	server := findField(iana, "refer")
	if server == "" {
		server = findField(iana, "whois")
	}
	if server == "" {
		return DomainInfo{Error: "no whois server for ." + tld}
	}

	// 2) Query the TLD's WHOIS server for the full domain record.
	raw, err := whoisQuery(ctx, server, domain, timeout)
	if err != nil {
		// Port-43 WHOIS is blocked from some networks (e.g. Cloudflare
		// Registrar's server refuses it) — retry over HTTPS via RDAP.
		if d2 := rdapDomainExpiry(ctx, domain, timeout); d2.OK {
			return d2
		}
		return DomainInfo{Error: "whois failed: " + err.Error()}
	}

	exp := parseExpiry(raw)
	reg := findFieldCI(raw, "Registrar")
	if exp == nil {
		// Some registrars only expose expiry over RDAP; try HTTPS.
		if d2 := rdapDomainExpiry(ctx, domain, timeout); d2.OK {
			return d2
		}
	}
	d := DomainInfo{Registrar: reg, ExpiryDate: exp}
	if exp != nil {
		days := int(time.Until(*exp).Hours() / 24)
		d.DaysRemaining = &days
		d.OK = true
		return d
	}
	return DomainInfo{Registrar: reg, Error: "expiry date not found in whois response"}
}

// whoisQuery opens a TCP connection to server:43, sends query and reads all
// output. Uses the DoH-first dialer because WHOIS server hostnames (e.g.
// whois.discord.com) can also be sinkholed by local resolvers.
func whoisQuery(ctx context.Context, server, query string, timeout time.Duration) (string, error) {
	d := doh.NewDialer(timeout)
	conn, err := d.DialContext(ctx, "tcp", server+":43")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := fmt.Fprintf(conn, "%s\r\n", query); err != nil {
		return "", err
	}

	var b strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil && b.Len() == 0 {
		return "", err
	}
	return b.String(), nil
}

var (
	expiryKeys = []string{
		"Registrar Registration Expiration Date",
		"Registry Expiry Date",
		"Expiration Date",
		"Expiry Date",
		"expires",
		"Expires on",
		"expire",
		"paid-till",
		"Valid Until",
		"Valid through",
		"Expiration",
	}
	dateLayouts = []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05 MST",
		"2006-01-02",
		"2006.01.02",
		"02-Jan-2006",
		"02 January 2006",
		"2006/01/02",
	}
)

// parseExpiry scans WHOIS lines for a recognised expiry key and parses the date.
func parseExpiry(raw string) *time.Time {
	for _, line := range strings.Split(raw, "\n") {
		lower := strings.ToLower(line)
		for _, key := range expiryKeys {
			if !strings.Contains(lower, strings.ToLower(key)) {
				continue
			}
			// take the part after the first ':'
			idx := strings.Index(line, ":")
			if idx < 0 || idx+1 >= len(line) {
				continue
			}
			val := strings.TrimSpace(line[idx+1:])
			if val == "" {
				continue
			}
			if t := parseDate(val); t != nil {
				return t
			}
		}
	}
	return nil
}

var tzAbbrev = regexp.MustCompile(` [A-Z]{2,5}$`)

// parseDate tries a set of common WHOIS date layouts.
func parseDate(val string) *time.Time {
	// strip trailing timezone abbreviations like " WIB", " UTC" for layouts that
	// don't include a zone token.
	cleaned := strings.TrimSpace(val)
	noTZ := tzAbbrev.ReplaceAllString(cleaned, "")
	candidates := []string{cleaned, noTZ}
	// also try trimming everything after a trailing comment
	for _, c := range candidates {
		for _, layout := range dateLayouts {
			if t, err := time.Parse(layout, c); err == nil {
				return &t
			}
		}
	}
	return nil
}

// findField returns the value of "key: value" (case-insensitive key, first hit).
func findField(raw, key string) string {
	prefix := strings.ToLower(key) + ":"
	for _, line := range strings.Split(raw, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(l), prefix) {
			return strings.TrimSpace(l[len(prefix):])
		}
	}
	return ""
}

// findFieldCI returns the first value whose key contains the given term.
func findFieldCI(raw, term string) string {
	for _, line := range strings.Split(raw, "\n") {
		l := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(l), strings.ToLower(term)+":") {
			idx := strings.Index(l, ":")
			if idx >= 0 {
				return strings.TrimSpace(l[idx+1:])
			}
		}
	}
	return ""
}

// registrableTLD returns the TLD portion used to find the WHOIS server. For
// multi-part public suffixes it prefers the longest common two-part TLD.
func registrableTLD(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return ""
	}
	// Handle a handful of common two-level ccTLDs (my.id, co.uk, com.au, ...).
	if len(parts) >= 3 {
		second := parts[len(parts)-2]
		switch second {
		case "co", "com", "net", "org", "ac", "gov", "edu", "my":
			return second + "." + parts[len(parts)-1]
		}
	}
	return parts[len(parts)-1]
}

// ---- RDAP (WHOIS over HTTPS) fallback ------------------------------------
//
// Plain port-43 WHOIS is refused or filtered from some networks while HTTPS
// always gets through, so every lookup that fails (or returns no expiry) is
// retried against the registrar's RDAP endpoint. Bootstrap data comes from
// IANA's published rdap-bootstrap JSON, cached for a day.

var (
	rdapBaseOnce sync.Once
	rdapBaseErr  error
	rdapEntries  []struct {
		Service string   `json:"service"`
		BaseURL string   `json:"base_url"` // may be "" — then use urls[0]
		URLs    []string `json:"urls"`
	}
)

func rdapHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = doh.NewDialer(timeout).DialContext
	return &http.Client{
		Timeout:   timeout + 5*time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 6 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// rdapBaseURL returns the registry RDAP base URL whose prefix matches the
// domain's TLD (longest match wins, so co.id beats id).
func rdapBaseURL(domain string) (string, error) {
	rdapBaseOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://data.iana.org/rdap/dns.json", nil)
		if err != nil {
			rdapBaseErr = err
			return
		}
		resp, err := rdapHTTPClient(15 * time.Second).Do(req)
		if err != nil {
			rdapBaseErr = err
			return
		}
		defer resp.Body.Close()
		var boot struct {
			Services [][]interface{} `json:"services"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&boot); err != nil {
			rdapBaseErr = err
			return
		}
		for _, svc := range boot.Services {
			// svc = [ [tlds...], { "rdapBase": "https://...", ... } ]
			if len(svc) < 2 {
				continue
			}
			tlds, ok1 := svc[0].([]interface{})
			srv, ok2 := svc[1].(map[string]interface{})
			if !ok1 || !ok2 {
				continue
			}
			base, _ := srv["rdapBase"].(string)
			for _, t := range tlds {
				if ts, ok := t.(string); ok {
					rdapEntries = append(rdapEntries, struct {
						Service string   `json:"service"`
						BaseURL string   `json:"base_url"`
						URLs    []string `json:"urls"`
					}{Service: ts, BaseURL: base})
				}
			}
		}
	})
	if rdapBaseErr != nil {
		return "", rdapBaseErr
	}
	tld := registrableTLD(domain)
	best := ""
	for _, e := range rdapEntries {
		if (e.Service == tld || strings.HasSuffix(tld, "."+e.Service)) && len(e.Service) > len(best) {
			best = e.BaseURL
		}
	}
	if best == "" {
		return "", fmt.Errorf("no RDAP endpoint for %s", tld)
	}
	return best, nil
}

// rdapDomainExpiry fetches the domain record over RDAP HTTPS and extracts
// expiry + registrar from the JSON (RFC 9083 / jcard shapes).
func rdapDomainExpiry(ctx context.Context, domain string, timeout time.Duration) DomainInfo {
	base, err := rdapBaseURL(domain)
	if err != nil {
		return DomainInfo{Error: err.Error()}
	}
	ctx2, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, strings.TrimRight(base, "/")+"/domain/"+domain, nil)
	if err != nil {
		return DomainInfo{Error: err.Error()}
	}
	req.Header.Set("Accept", "application/rdap+json")
	resp, err := rdapHTTPClient(timeout).Do(req)
	if err != nil {
		return DomainInfo{Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DomainInfo{Error: fmt.Sprintf("rdap status %d", resp.StatusCode)}
	}
	var doc struct {
		Events []struct {
			ActionDescription string `json:"eventAction"`
			Date              string `json:"eventDate"`
		} `json:"events"`
		Entities []struct {
			Roles      []string      `json:"roles"`
			VcardArray []interface{} `json:"vcardArray"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&doc); err != nil {
		return DomainInfo{Error: "rdap parse failed: " + err.Error()}
	}
	var exp *time.Time
	for _, ev := range doc.Events {
		key := strings.ToLower(strings.ReplaceAll(ev.ActionDescription, " ", ""))
		if key == "expiration" {
			if t, perr := time.Parse(time.RFC3339, ev.Date); perr == nil {
				tt := t
				exp = &tt
				break
			}
		}
	}
	reg := ""
	for _, ent := range doc.Entities {
		isRegistrar := false
		for _, r := range ent.Roles {
			if strings.EqualFold(r, "registrar") {
				isRegistrar = true
			}
		}
		if !isRegistrar || len(ent.VcardArray) < 2 {
			continue
		}
		// jcard: ["vcard", [["fn", {}, "text", "Name"], ...]]
		if items, ok := ent.VcardArray[1].([]interface{}); ok {
			for _, it := range items {
				row, ok := it.([]interface{})
				if !ok || len(row) < 4 {
					continue
				}
				if name, ok := row[0].(string); ok && strings.EqualFold(name, "fn") {
					if v, ok := row[3].(string); ok {
						reg = v
					}
				}
			}
		}
	}
	if exp == nil {
		return DomainInfo{Registrar: reg, Error: "rdap: expiry not found"}
	}
	days := int(time.Until(*exp).Hours() / 24)
	return DomainInfo{Registrar: reg, ExpiryDate: exp, DaysRemaining: &days, OK: true}
}

// RegistrableDomain reduces a hostname to its registrable domain (e.g.
// "www.example.co.uk" -> "example.co.uk") using the same lightweight
// public-suffix heuristic as registrableTLD.
func RegistrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	tld := registrableTLD(host)
	suffixParts := strings.Count(tld, ".") + 1
	if len(parts) <= suffixParts+1 {
		return host
	}
	return strings.Join(parts[len(parts)-(suffixParts+1):], ".")
}
