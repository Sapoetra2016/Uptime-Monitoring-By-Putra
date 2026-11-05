// Domain-intelligence probes: everything about a domain a black-box monitor
// can learn from the OUTSIDE — no agent, no server access. All use the Go
// standard library (net DNS resolver, crypto/tls, net/http), so no new
// dependencies. Every function degrades gracefully and returns an Error
// string the UI can render as "—" instead of breaking.
package domainssl

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- SSL detail (extends the leaf info in domainssl.go) ----------

// SSLDetail is a richer TLS certificate report for the "Domain Intelligence"
// panel. It complements SSLInfo by adding protocol, SANs and validity window.
type SSLDetail struct {
	Subject       string     `json:"subject"`
	Issuer        string     `json:"issuer"`
	Serial        string     `json:"serial,omitempty"`
	Protocol      string     `json:"protocol,omitempty"`
	KeyAlgorithm  string     `json:"keyAlgorithm,omitempty"`
	KeyBits       int        `json:"keyBits,omitempty"`
	SANs          []string   `json:"sans,omitempty"`
	NotBefore     *time.Time `json:"notBefore,omitempty"`
	NotAfter      *time.Time `json:"notAfter,omitempty"`
	DaysRemaining int        `json:"daysRemaining"`
	ExpiresSoon   bool       `json:"expiresSoon"`
	Expired       bool       `json:"expired"`
	HostnameMatch bool       `json:"hostnameMatch"`
	ChainTrusted  bool       `json:"chainTrusted"`
	SelfSigned    bool       `json:"selfSigned"`
	Score         int        `json:"score"`
	OK            bool       `json:"ok"`
	Error         string     `json:"error,omitempty"`
}

// CheckSSLDetailed dials host:port and returns the full leaf-cert report plus a
// 0-100 SSL score. Unlike the basic monitor (which verifies the leaf only),
// Domain Intelligence is the one place that does the full CA / chain-of-trust
// analysis - but it still reads the certificate when that chain is NOT trusted
// (self-signed or incomplete-chain internal servers) so the report and score
// stay useful; trust is folded into the score instead of aborting the probe.
func CheckSSLDetailed(ctx context.Context, host, port string, timeout time.Duration) SSLDetail {
	if port == "" {
		port = "443"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, port), &tls.Config{
		// Probe down to TLS 1.0 so an outdated protocol can be detected and
		// penalised in the score rather than failing the handshake outright.
		MinVersion:         tls.VersionTLS10,
		ServerName:         host,
		NextProtos:         []string{"h2", "http/1.1"},
		InsecureSkipVerify: true,
	})
	if err != nil {
		return SSLDetail{Error: err.Error()}
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return SSLDetail{Error: "no certificate presented"}
	}
	leaf := certs[0]
	days := int(time.Until(leaf.NotAfter).Hours() / 24)
	nb, na := leaf.NotBefore, leaf.NotAfter

	// Does the certificate actually cover the host we dialed (SAN / CN)?
	hostnameMatch := leaf.VerifyHostname(host) == nil

	// Chain-of-trust against the system roots, evaluated manually so an
	// untrusted chain is reported (and scored) rather than thrown away.
	chainTrusted := false
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, verr := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       host,
	}); verr == nil {
		chainTrusted = true
	}

	// Self-signed heuristic: issuer and subject are byte-identical.
	selfSigned := bytes.Equal(leaf.RawIssuer, leaf.RawSubject)

	d := SSLDetail{
		Subject:       leaf.Subject.CommonName,
		Issuer:        leaf.Issuer.CommonName,
		Serial:        leaf.SerialNumber.String(),
		Protocol:      tlsVersionName(conn.ConnectionState().Version),
		KeyAlgorithm:  keyAlgoName(leaf),
		KeyBits:       keyBits(leaf),
		SANs:          dedupeStrings(leaf.DNSNames),
		NotBefore:     &nb,
		NotAfter:      &na,
		DaysRemaining: days,
		ExpiresSoon:   days >= 0 && days <= 21,
		Expired:       days < 0,
		HostnameMatch: hostnameMatch,
		ChainTrusted:  chainTrusted,
		SelfSigned:    selfSigned,
		OK:            true,
	}
	d.Score = sslScore(d)
	return d
}

// sslScore weights the signals a reviewer cares about into a 0-100 grade:
// trusted chain (30) + hostname match (15) + validity (20 not-expired, up to
// 10 more for a healthy window) + protocol (15 for TLS1.3, 10 for 1.2) + key
// strength (10). A valid-but-self-signed internal cert therefore lands around
// 70, a fully trusted modern public cert at 100.
func sslScore(d SSLDetail) int {
	score := 0
	if d.ChainTrusted {
		score += 30
	}
	if d.HostnameMatch {
		score += 15
	}
	if !d.Expired {
		score += 20
		if !d.ExpiresSoon {
			score += 10
		} else {
			score += 5
		}
	}
	switch d.Protocol {
	case "TLS 1.3":
		score += 15
	case "TLS 1.2":
		score += 10
	}
	switch {
	case d.KeyAlgorithm == "Ed25519":
		score += 10
	case d.KeyAlgorithm == "ECDSA" && d.KeyBits >= 256:
		score += 10
	case strings.HasPrefix(d.KeyAlgorithm, "RSA") && d.KeyBits >= 2048:
		score += 10
	case strings.HasPrefix(d.KeyAlgorithm, "RSA") && d.KeyBits >= 1024:
		score += 4
	}
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	}
	return ""
}

func keyAlgoName(c *x509.Certificate) string {
	switch c.PublicKeyAlgorithm {
	case x509.RSA:
		if pub := rsaBits(c); pub > 0 {
			return fmt.Sprintf("RSA %d", pub)
		}
		return "RSA"
	case x509.ECDSA:
		return "ECDSA"
	case x509.Ed25519:
		return "Ed25519"
	}
	return c.PublicKeyAlgorithm.String()
}

func rsaBits(c *x509.Certificate) int {
	if ks, ok := c.PublicKey.(interface{ Size() int }); ok {
		return ks.Size() * 8
	}
	return 0
}

// keyBits reports the effective security strength in bits for scoring: RSA
// modulus size, EC curve size, or 256 for Ed25519.
func keyBits(c *x509.Certificate) int {
	switch c.PublicKeyAlgorithm {
	case x509.RSA:
		return rsaBits(c)
	case x509.ECDSA:
		if ec, ok := c.PublicKey.(*ecdsa.PublicKey); ok && ec.Curve != nil {
			return ec.Curve.Params().BitSize
		}
	case x509.Ed25519:
		return 256
	}
	return 0
}

// ---------- DNS records ----------

// MXRecord is one mail exchanger with its priority.
type MXRecord struct {
	Host string `json:"host"`
	Pref uint16 `json:"pref"`
}

// DNSRecords is a snapshot of a host/domain's public DNS.
type DNSRecords struct {
	A     []string   `json:"a,omitempty"`
	AAAA  []string   `json:"aaaa,omitempty"`
	CNAME string     `json:"cname,omitempty"`
	MX    []MXRecord `json:"mx,omitempty"`
	NS    []string   `json:"ns,omitempty"`
	TXT   []string   `json:"txt,omitempty"`
	Error string     `json:"error,omitempty"`
}

// CheckDNS resolves A/AAAA/CNAME/MX/NS/TXT. NS + TXT are queried against the
// registrable domain (subdomains rarely delegate), the rest against the host.
func CheckDNS(ctx context.Context, host, domain string) DNSRecords {
	r := net.DefaultResolver
	var rec DNSRecords
	if addrs, err := r.LookupIPAddr(ctx, host); err == nil {
		for _, a := range addrs {
			if a.IP.To4() != nil {
				rec.A = append(rec.A, a.IP.String())
			} else {
				rec.AAAA = append(rec.AAAA, a.IP.String())
			}
		}
	}
	if c, err := r.LookupCNAME(ctx, host); err == nil {
		c = strings.TrimSuffix(c, ".")
		if c != "" && !strings.EqualFold(c, host) {
			rec.CNAME = c
		}
	}
	if mxs, err := r.LookupMX(ctx, domain); err == nil {
		for _, m := range mxs {
			rec.MX = append(rec.MX, MXRecord{Host: strings.TrimSuffix(m.Host, "."), Pref: m.Pref})
		}
		sort.Slice(rec.MX, func(i, j int) bool { return rec.MX[i].Pref < rec.MX[j].Pref })
	}
	if nss, err := r.LookupNS(ctx, domain); err == nil {
		for _, n := range nss {
			rec.NS = append(rec.NS, strings.TrimSuffix(n.Host, "."))
		}
	}
	if txts, err := r.LookupTXT(ctx, domain); err == nil {
		rec.TXT = txts
	}
	if len(rec.A) == 0 && len(rec.AAAA) == 0 && rec.CNAME == "" && len(rec.MX) == 0 && len(rec.NS) == 0 && len(rec.TXT) == 0 {
		rec.Error = "no DNS records resolved"
	}
	return rec
}

// ---------- Email security (SPF / DMARC / DKIM) ----------

// SPFInfo reports whether the domain publishes an SPF record.
type SPFInfo struct {
	Present bool   `json:"present"`
	Record  string `json:"record,omitempty"`
	OK      bool   `json:"ok"`
}

// DMARCInfo reports the _dmarc policy.
type DMARCInfo struct {
	Present  bool   `json:"present"`
	Policy   string `json:"policy,omitempty"` // none | quarantine | reject
	Pct      int    `json:"pct"`
	ReportTo string `json:"reportTo,omitempty"`
	Record   string `json:"record,omitempty"`
	OK       bool   `json:"ok"`
}

// DKIMInfo reports one detected DKIM selector.
type DKIMInfo struct {
	Selector string `json:"selector"`
	Present  bool   `json:"present"`
	OK       bool   `json:"ok"`
}

// EmailSecurity bundles the three deliverability/authentication checks.
type EmailSecurity struct {
	SPF   *SPFInfo   `json:"spf,omitempty"`
	DMARC *DMARCInfo `json:"dmarc,omitempty"`
	DKIM  []DKIMInfo `json:"dkim,omitempty"`
	Error string     `json:"error,omitempty"`
}

// Common DKIM selectors seen in the wild (Google, Microsoft, generic).
var dkimSelectors = []string{"default", "selector1", "selector2", "google", "k1", "s1", "mail"}

// CheckEmailSecurity inspects SPF, DMARC and probes DKIM selectors via TXT.
func CheckEmailSecurity(ctx context.Context, domain string) EmailSecurity {
	r := net.DefaultResolver
	var es EmailSecurity

	if txts, err := r.LookupTXT(ctx, domain); err == nil {
		es.SPF = &SPFInfo{}
		for _, t := range txts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), "v=spf1") {
				es.SPF.Present = true
				es.SPF.OK = !strings.Contains(strings.ToLower(t), "+all")
				es.SPF.Record = t
				break
			}
		}
	}

	if txts, err := r.LookupTXT(ctx, "_dmarc."+domain); err == nil {
		es.DMARC = &DMARCInfo{}
		for _, t := range txts {
			if strings.Contains(strings.ToLower(t), "v=dmarc1") {
				es.DMARC.Present = true
				es.DMARC.Record = t
				es.DMARC.Policy = dmarcTag(t, "p")
				if p := dmarcTag(t, "pct"); p != "" {
					es.DMARC.Pct, _ = strconv.Atoi(p)
				} else {
					es.DMARC.Pct = 100
				}
				es.DMARC.ReportTo = dmarcTag(t, "rua")
				es.DMARC.OK = es.DMARC.Policy == "reject" || es.DMARC.Policy == "quarantine"
				break
			}
		}
	}

	for _, sel := range dkimSelectors {
		txts, err := r.LookupTXT(ctx, sel+"._domainkey."+domain)
		if err != nil || len(txts) == 0 {
			continue
		}
		for _, t := range txts {
			lt := strings.ToLower(t)
			if strings.Contains(lt, "p=") && !strings.HasSuffix(lt, "p=") {
				es.DKIM = append(es.DKIM, DKIMInfo{Selector: sel, Present: true, OK: true})
			} else {
				es.DKIM = append(es.DKIM, DKIMInfo{Selector: sel, Present: true, OK: false})
			}
		}
	}

	if es.SPF == nil && es.DMARC == nil && len(es.DKIM) == 0 {
		es.Error = "no email records found"
	}
	return es
}

// dmarcTag extracts "; tag=value" from a DMARC record.
func dmarcTag(rec, tag string) string {
	for _, f := range strings.Split(rec, ";") {
		kv := strings.SplitN(strings.TrimSpace(f), "=", 2)
		if len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), tag) {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

// ---------- Security headers ----------

// HeaderCheck is one evaluated response header.
type HeaderCheck struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
	Good    bool   `json:"good"`
	Note    string `json:"note"`
}

// SecurityHeaders is the checklist + a 0-100 score.
type SecurityHeaders struct {
	Checked string        `json:"checked"`
	Checks  []HeaderCheck `json:"checks"`
	Score   int           `json:"score"`
	Error   string        `json:"error,omitempty"`
}

type headerRule struct {
	name string
	good func(string) bool
	note string
}

var headerRules = []headerRule{
	{"Strict-Transport-Security", func(v string) bool { return strings.Contains(strings.ToLower(v), "max-age") }, "Paksa koneksi HTTPS (anti downgrade)"},
	{"Content-Security-Policy", func(v string) bool { return strings.TrimSpace(v) != "" }, "Batasi sumber script — anti XSS"},
	{"X-Frame-Options", func(v string) bool { s := strings.ToUpper(v); return s == "DENY" || s == "SAMEORIGIN" }, "Anti clickjacking"},
	{"X-Content-Type-Options", func(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "nosniff") }, "Anti MIME sniffing"},
	{"Referrer-Policy", func(v string) bool { return strings.TrimSpace(v) != "" }, "Kontrol kebocoran referrer"},
	{"Permissions-Policy", func(v string) bool { return strings.TrimSpace(v) != "" }, "Batasi fitur browser (kamera/mikrofon)"},
}

// CheckSecurityHeaders GETs the target URL and evaluates common hardening
// headers. TLS verification is skipped here (we only read headers) so
// self-signed sites are still auditable.
func CheckSecurityHeaders(ctx context.Context, target string, timeout time.Duration) SecurityHeaders {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if target == "" {
		return SecurityHeaders{Error: "no URL to check"}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return SecurityHeaders{Checked: target, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "UptimeMonitor-DomainIntel/1.0")

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			TLSHandshakeTimeout: timeout,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return SecurityHeaders{Checked: target, Error: err.Error()}
	}
	defer resp.Body.Close()

	sh := SecurityHeaders{Checked: resp.Request.URL.String()}
	good := 0
	for _, h := range headerRules {
		v := resp.Header.Get(h.name)
		present := strings.TrimSpace(v) != ""
		ok := present && h.good(v)
		if ok {
			good++
		}
		sh.Checks = append(sh.Checks, HeaderCheck{Name: h.name, Present: present, Value: v, Good: ok, Note: h.note})
	}
	if len(headerRules) > 0 {
		sh.Score = int(float64(good) / float64(len(headerRules)) * 100)
	}
	return sh
}

// ---------- helpers ----------

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
