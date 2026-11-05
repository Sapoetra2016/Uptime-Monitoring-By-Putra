package monitor

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" database/sql driver
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/miekg/dns"
	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/sysdns"
)

// CheckOutcome is the raw result produced by a checker before persistence.
type CheckOutcome struct {
	Status     models.MonitorStatus
	LatencyMs  *float64
	HTTPStatus int
	Message    string
	Meta       map[string]interface{}
}

func ms(v float64) *float64 { return &v }

// Checker runs a single probe for a monitor and returns its outcome.
type Checker interface {
	Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error)
}

// ---------- HTTP ----------

// legacyCipherSuites covers the older suites Go's crypto/tls still implements
// (static RSA key exchange, ECDHE-CBC, 3DES) up to the modern TLS 1.3 ones.
// Go omits the legacy suites from its default policy, which makes servers
// that only offer them fail as "tls: handshake failure". They are never used
// first — only as a retry after the modern handshake was rejected by the
// peer. (Finite-field DHE is NOT supported by Go at all; those servers are
// handled by the curl/openssl fallback below.)
var legacyCipherSuites = []uint16{
	// TLS 1.3 (must be listed explicitly once CipherSuites is set)
	tls.TLS_AES_128_GCM_SHA256,
	tls.TLS_AES_256_GCM_SHA384,
	tls.TLS_CHACHA20_POLY1305_SHA256,
	// Modern TLS 1.2
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	// Legacy TLS 1.2/1.1/1.0 (static RSA key exchange)
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

// isTLSNegotiationError reports whether err looks like the peer refusing our
// TLS parameters (cipher/protocol mismatch) rather than a certificate problem
// or a plain network failure. Only these errors justify the legacy retry.
func isTLSNegotiationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{
		"handshake failure",
		"protocol version",
		"no cipher suite",
		"unconfigured cipher",
		"unsupported certificate",
		"dh key",
		"legacy",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// verifyLeafCert keeps the monitor's leaf-only certificate policy: the cert
// must exist, be inside its validity window and match the host — but the
// issuing CA chain is not required to terminate in a trusted root.
func verifyLeafCert(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("no certificate presented")
	}
	leaf := cs.PeerCertificates[0]
	now := time.Now()
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("certificate not yet valid (valid from %s)", leaf.NotBefore.Format(time.RFC3339))
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("certificate expired on %s", leaf.NotAfter.Format(time.RFC3339))
	}
	if cs.ServerName != "" {
		if verr := leaf.VerifyHostname(cs.ServerName); verr != nil {
			return fmt.Errorf("certificate does not match host: %w", verr)
		}
	}
	return nil
}

type HTTPChecker struct {
	client         *http.Client
	insecureClient *http.Client
	legacyClient   *http.Client
	timeout        time.Duration
}

func NewHTTPChecker(timeout time.Duration) *HTTPChecker {
	// Follow the server's own DNS (/etc/resolv.conf) with retries, so a brief
	// SERVFAIL from systemd-resolved doesn't flip a healthy monitor to DOWN.
	dialer := sysdns.NewDialer(timeout)
	// Default client validates the certificate itself - that one was presented,
	// that it is inside its validity window, and that it matches the host - but
	// it deliberately does NOT require the issuing chain to terminate in a
	// trusted root. Chain-of-trust / CA analysis belongs to Domain Intelligence,
	// so an internal server with a self-signed or incomplete-chain certificate
	// still counts as UP as long as the leaf certificate is otherwise OK. This
	// is the documented Go custom-verification pattern: InsecureSkipVerify turns
	// off the built-in chain check and VerifyConnection runs our leaf checks.
	certOnlyTransport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			VerifyConnection:   verifyLeafCert,
		},
	}
	// A second client that skips TLS verification entirely, used only when a
	// monitor has "Ignore SSL" enabled.
	insecureTransport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	// Retry client for servers whose TLS stack predates Go's default policy:
	// down to TLS 1.0 and legacy DHE/RSA/3DES suites, same leaf-cert checks.
	legacyTransport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS10,
			CipherSuites:       legacyCipherSuites,
			InsecureSkipVerify: true,
			VerifyConnection:   verifyLeafCert,
		},
	}
	return &HTTPChecker{
		client:         &http.Client{Timeout: timeout, Transport: certOnlyTransport},
		insecureClient: &http.Client{Timeout: timeout, Transport: insecureTransport},
		legacyClient:   &http.Client{Timeout: timeout, Transport: legacyTransport},
		timeout:        timeout,
	}
}

func (c *HTTPChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	method := m.Method
	if method == "" {
		method = http.MethodGet
	}
	buildReq := func() (*http.Request, error) {
		var bodyReader io.Reader
		if m.Body != "" {
			bodyReader = strings.NewReader(m.Body)
		}
		req, err := http.NewRequestWithContext(ctx, method, m.URL, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "UptimeMonitor/1.0")
		for k, v := range m.Headers {
			req.Header.Set(k, v)
		}
		if m.BasicAuthUser != "" || m.BasicAuthPass != "" {
			req.SetBasicAuth(m.BasicAuthUser, m.BasicAuthPass)
		}
		return req, nil
	}
	req, err := buildReq()
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error()}, nil
	}

	start := time.Now()
	client := c.client
	if m.IgnoreTLS {
		client = c.insecureClient
	}
	resp, err := client.Do(req)
	latency := float64(time.Since(start).Milliseconds())
	legacyTLS := false
	// Stage 2: the peer rejected our modern TLS parameters — retry offering
	// Go's legacy suites (static RSA kx, CBC, back to TLS 1.0) so dated-but-
	// alive sites still count as UP.
	if err != nil && isTLSNegotiationError(err) {
		if retryReq, rerr := buildReq(); rerr == nil {
			start = time.Now()
			if retryResp, rerr2 := c.legacyClient.Do(retryReq); rerr2 == nil {
				resp, err = retryResp, nil
				latency = float64(time.Since(start).Milliseconds())
				legacyTLS = true
			}
		}
	}
	// Stage 3: servers that ONLY offer finite-field DHE can't be reached by
	// Go's crypto/tls at all (the kx isn't implemented). curl/openssl still
	// speak it at a relaxed security level, so probe through them as a last
	// resort and re-apply our leaf-only certificate policy in Go.
	if err != nil && isTLSNegotiationError(err) {
		if code, body, lat, cerr := c.probeViaCurl(ctx, m); cerr == nil {
			if !m.IgnoreTLS {
				if verr := leafCertViaOpenSSL(ctx, m.URL, c.timeout); verr != nil {
					return &CheckOutcome{Status: models.StatusDown, Message: verr.Error(), LatencyMs: ms(lat)}, nil
				}
			}
			return evaluateRawHTTP(m, code, body, lat), nil
		}
	}
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(latency)}, nil
	}
	defer resp.Body.Close()

	expected := m.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	outcome := &CheckOutcome{
		LatencyMs:  ms(latency),
		HTTPStatus: resp.StatusCode,
		Meta:       map[string]interface{}{"headers": resp.Header.Get("Content-Type")},
	}

	if resp.StatusCode != expected {
		outcome.Status = models.StatusDown
		outcome.Message = fmt.Sprintf("unexpected status code %d (expected %d)", resp.StatusCode, expected)
		return outcome, nil
	}

	if m.Keyword != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			outcome.Status = models.StatusDown
			outcome.Message = "failed reading body: " + err.Error()
			return outcome, nil
		}
		if !strings.Contains(string(body), m.Keyword) {
			outcome.Status = models.StatusDown
			outcome.Message = "keyword not found in response body"
			return outcome, nil
		}
	}

	// Latency threshold (degraded) is applied centrally by the engine.
	outcome.Status = models.StatusUp
	outcome.Message = "OK"
	if legacyTLS {
		outcome.Message = "OK (legacy TLS)"
		outcome.Meta["legacyTLS"] = true
	}
	return outcome, nil
}

// probeViaCurl runs the check through the system curl binary with TLS
// security relaxed to SECLEVEL=1, which still negotiates ancient handshakes
// (static DHE included) that Go's TLS stack refuses or can't implement. It
// returns the HTTP status code, up to 1 MB of body and the measured latency.
func (c *HTTPChecker) probeViaCurl(ctx context.Context, m *models.Monitor) (int, []byte, float64, error) {
	path, err := exec.LookPath("curl")
	if err != nil {
		return 0, nil, 0, fmt.Errorf("curl not available for legacy TLS fallback")
	}
	tmp, err := os.CreateTemp("", "uptime-curl-body-*")
	if err != nil {
		return 0, nil, 0, err
	}
	bodyPath := tmp.Name()
	tmp.Close()
	defer os.Remove(bodyPath)

	method := m.Method
	if method == "" {
		method = http.MethodGet
	}
	args := []string{
		"-s", "-L", "-k",
		"--ciphers", "ALL:eNULL@SECLEVEL=1",
		"--max-time", strconv.Itoa(int(c.timeout.Seconds()) + 2),
		"-o", bodyPath,
		"-w", "%{http_code} %{time_total}",
		"-X", method,
	}
	for k, v := range m.Headers {
		args = append(args, "-H", k+": "+v)
	}
	if m.BasicAuthUser != "" || m.BasicAuthPass != "" {
		args = append(args, "-u", m.BasicAuthUser+":"+m.BasicAuthPass)
	}
	if m.Body != "" {
		args = append(args, "--data-binary", "@-")
	}
	args = append(args, m.URL)

	cmd := exec.CommandContext(ctx, path, args...)
	if m.Body != "" {
		cmd.Stdin = strings.NewReader(m.Body)
	}
	start := time.Now()
	out, err := cmd.Output()
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return 0, nil, latency, fmt.Errorf("curl legacy probe failed: %w", err)
	}
	var code int
	var total float64
	if _, perr := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %f", &code, &total); perr != nil || code == 0 {
		return 0, nil, latency, fmt.Errorf("unparsable curl result %q", strings.TrimSpace(string(out)))
	}
	f, err := os.Open(bodyPath)
	if err != nil {
		return code, nil, latency, nil
	}
	defer f.Close()
	body, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return code, body, latency, nil
}

// evaluateRawHTTP turns a raw status+body probe (curl fallback) into an
// outcome using the same expected-status / keyword rules as the Go path.
func evaluateRawHTTP(m *models.Monitor, code int, body []byte, latency float64) *CheckOutcome {
	expected := m.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	outcome := &CheckOutcome{
		LatencyMs:  ms(latency),
		HTTPStatus: code,
		Meta:       map[string]interface{}{"legacyTLS": true},
	}
	if code != expected {
		outcome.Status = models.StatusDown
		outcome.Message = fmt.Sprintf("unexpected status code %d (expected %d)", code, expected)
		return outcome
	}
	if m.Keyword != "" && !strings.Contains(string(body), m.Keyword) {
		outcome.Status = models.StatusDown
		outcome.Message = "keyword not found in response body"
		return outcome
	}
	outcome.Status = models.StatusUp
	outcome.Message = "OK (legacy TLS)"
	return outcome
}

// leafCertViaOpenSSL fetches the peer's leaf certificate through the system
// openssl (again: it can still speak DHE-only handshakes) and applies the
// same leaf-only policy as verifyLeafCert: present, inside validity, matches
// the host. If openssl isn't available the check is tolerated (skip), since
// availability itself was already proven by curl.
func leafCertViaOpenSSL(ctx context.Context, rawURL string, timeout time.Duration) error {
	path, err := exec.LookPath("openssl")
	if err != nil {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, path,
		"s_client",
		"-connect", net.JoinHostPort(host, port),
		"-servername", host,
		"-cipher", "ALL:eNULL@SECLEVEL=1",
		"-showcerts",
	)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("legacy handshake failed: %w", err)
	}
	for rest := out; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		leaf, perr := x509.ParseCertificate(blk.Bytes)
		if perr != nil {
			continue
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) {
			return fmt.Errorf("certificate not yet valid (valid from %s)", leaf.NotBefore.Format(time.RFC3339))
		}
		if now.After(leaf.NotAfter) {
			return fmt.Errorf("certificate expired on %s", leaf.NotAfter.Format(time.RFC3339))
		}
		if herr := leaf.VerifyHostname(host); herr != nil {
			return fmt.Errorf("certificate does not match host: %w", herr)
		}
		return nil
	}
	return fmt.Errorf("no certificate presented")
}

// ---------- TCP ----------

type TCPChecker struct{ defaultTimeout time.Duration }

func NewTCPChecker(timeout time.Duration) *TCPChecker { return &TCPChecker{defaultTimeout: timeout} }

func (c *TCPChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	addr := net.JoinHostPort(m.Host, fmt.Sprintf("%d", m.Port))
	d := sysdns.NewDialer(timeout)

	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(latency)}, nil
	}
	conn.Close()
	return &CheckOutcome{Status: models.StatusUp, Message: "connection established", LatencyMs: ms(latency)}, nil
}

// ---------- DNS ----------

type DNSChecker struct{ defaultTimeout time.Duration }

func NewDNSChecker(timeout time.Duration) *DNSChecker { return &DNSChecker{defaultTimeout: timeout} }

func (c *DNSChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	server := m.DNSServer
	if server == "" {
		server = "8.8.8.8:53"
	} else if !strings.Contains(server, ":") {
		server += ":53"
	}

	recordType := strings.ToUpper(m.RecordType)
	if recordType == "" {
		recordType = "A"
	}
	qtype, ok := dns.StringToType[recordType]
	if !ok {
		return &CheckOutcome{Status: models.StatusDown, Message: "unsupported record type: " + recordType}, nil
	}

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(m.Host), qtype)
	msg.RecursionDesired = true

	client := &dns.Client{Timeout: timeout}
	start := time.Now()
	resp, _, err := client.ExchangeContext(ctx, msg, server)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(latency)}, nil
	}
	if len(resp.Answer) == 0 {
		return &CheckOutcome{Status: models.StatusDown, Message: "no answers returned", LatencyMs: ms(latency)}, nil
	}
	answers := make([]string, 0, len(resp.Answer))
	for _, a := range resp.Answer {
		answers = append(answers, a.String())
	}
	return &CheckOutcome{
		Status:    models.StatusUp,
		LatencyMs: ms(latency),
		Message:   fmt.Sprintf("%d record(s) resolved", len(resp.Answer)),
		Meta:      map[string]interface{}{"answers": answers},
	}, nil
}

// ---------- Cert (X.509 expiry check) ----------

type CertChecker struct{ defaultTimeout time.Duration }

func NewCertChecker(timeout time.Duration) *CertChecker { return &CertChecker{defaultTimeout: timeout} }

func (c *CertChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	warnDays := m.CertWarnDays
	if warnDays == 0 {
		warnDays = 14
	}

	host, port, err := net.SplitHostPort(m.Host)
	if err != nil {
		host = m.Host
		port = "443"
	}

	// Dial through sysdns (resolv.conf-aware, with retry), then wrap in TLS
	// manually because tls.DialWithDialer only accepts a plain net.Dialer.
	dialer := sysdns.NewDialer(timeout)
	start := time.Now()
	rawConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(float64(time.Since(start).Milliseconds()))}, nil
	}
	conn := tls.Client(rawConn, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// Cert monitoring reports expiry; it must still be able to read the leaf
		// when the issuing chain isn't trusted (self-signed / incomplete chain),
		// so the trusted-root check is skipped here too.
		InsecureSkipVerify: true,
	})
	legacyTLS := false
	if herr := conn.HandshakeContext(ctx); herr != nil {
		rawConn.Close()
		// Same adaptive retry as the HTTP checker: if the peer rejected our
		// modern TLS parameters, offer legacy suites/protocols before giving up
		// so dated-but-alive sites still report their certificate.
		if isTLSNegotiationError(herr) {
			if rawConn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port)); err == nil {
				conn = tls.Client(rawConn, &tls.Config{
					ServerName:         host,
					MinVersion:         tls.VersionTLS10,
					CipherSuites:       legacyCipherSuites,
					InsecureSkipVerify: true,
				})
				herr = conn.HandshakeContext(ctx)
				legacyTLS = true
			}
		}
		if herr != nil {
			rawConn.Close()
			return &CheckOutcome{Status: models.StatusDown, Message: herr.Error(), LatencyMs: ms(float64(time.Since(start).Milliseconds()))}, nil
		}
	}
	latency := float64(time.Since(start).Milliseconds())
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return &CheckOutcome{Status: models.StatusDown, Message: "no certificate presented", LatencyMs: ms(latency)}, nil
	}
	leaf := certs[0]
	daysRemaining := int(time.Until(leaf.NotAfter).Hours() / 24)

	meta := map[string]interface{}{
		"subject":       leaf.Subject.CommonName,
		"issuer":        leaf.Issuer.CommonName,
		"notAfter":      leaf.NotAfter.Format(time.RFC3339),
		"daysRemaining": daysRemaining,
	}

	if daysRemaining < 0 {
		return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: fmt.Sprintf("certificate expired %d day(s) ago", -daysRemaining), Meta: meta}, nil
	}
	if legacyTLS {
		meta["legacyTLS"] = true
	}
	if daysRemaining <= warnDays {
		return &CheckOutcome{Status: models.StatusDegraded, LatencyMs: ms(latency), Message: fmt.Sprintf("certificate expires in %d day(s)", daysRemaining), Meta: meta}, nil
	}
	msg := fmt.Sprintf("certificate valid, %d day(s) remaining", daysRemaining)
	if legacyTLS {
		msg += " (legacy TLS)"
	}
	return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: msg, Meta: meta}, nil
}

// ---------- Push / Heartbeat ----------
// The engine doesn't actively probe these; it evaluates whether the last push
// happened within interval + grace window.

type PushChecker struct{}

func NewPushChecker() *PushChecker { return &PushChecker{} }

func (c *PushChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	return &CheckOutcome{Status: models.StatusPending, Message: "waiting for heartbeat"}, nil
}

// ---------- Docker (via Engine HTTP API over unix socket, no external deps) ----------

type DockerChecker struct {
	client *http.Client
}

func NewDockerChecker(timeout time.Duration) *DockerChecker {
	socketPath := os.Getenv("UPTIME_DOCKER_SOCKET")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}
	transport := &http.Transport{}
	if strings.HasPrefix(socketPath, "/") {
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		}
	} else {
		// tcp form e.g. "localhost:2375" - used for remote engine (not the default)
		transport.DialContext = (&net.Dialer{Timeout: timeout}).DialContext
	}
	return &DockerChecker{client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}}
}

func (c *DockerChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	base := "http://docker" // host is ignored, unix socket transport is used
	url := fmt.Sprintf("%s/v1.41/containers/%s/json", base, m.ContainerName)
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error()}, nil
	}
	resp, err := c.client.Do(req)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: "docker socket unreachable: " + err.Error(), LatencyMs: ms(latency)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: "container not found"}, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var info []struct {
		State  string `json:"State"`
		Status string `json:"Status"`
	}
	if err := json.Unmarshal(body, &info); err != nil || len(info) == 0 {
		return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: "unexpected docker API response"}, nil
	}
	state := info[0].State
	if strings.EqualFold(state, "running") {
		return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: info[0].Status}, nil
	}
	return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: fmt.Sprintf("container state: %s (%s)", state, info[0].Status)}, nil
}

// ---------- Ping (ICMP, via OS `ping` tool so no elevated privileges needed) ----------

type PingChecker struct{ defaultTimeout time.Duration }

func NewPingChecker(timeout time.Duration) *PingChecker { return &PingChecker{defaultTimeout: timeout} }

func (c *PingChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	host := m.Host
	if host == "" {
		host = m.URL
	}
	if host == "" {
		return &CheckOutcome{Status: models.StatusDown, Message: "no host configured"}, nil
	}
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}

	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name = "ping"
		args = []string{"-n", "1", "-w", strconv.Itoa(int(timeout.Milliseconds())), host}
	} else {
		name = "ping"
		args = []string{"-c", "1", "-W", strconv.Itoa(int(timeout.Seconds())), host}
	}

	cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()

	start := time.Now()
	out, err := exec.CommandContext(cctx, name, args...).CombinedOutput()
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return &CheckOutcome{Status: models.StatusDown, Message: msg, LatencyMs: ms(latency)}, nil
	}
	// Prefer the reported round-trip time when we can parse it, else use wall time.
	if parsed := parsePingTimeMS(string(out)); parsed != nil {
		latency = *parsed
	}
	return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: "reply received"}, nil
}

// parsePingTimeMS extracts "time=12ms" / "time<1ms" style timings from ping output.
func parsePingTimeMS(s string) *float64 {
	idx := strings.Index(s, "time=")
	if idx < 0 {
		return nil
	}
	rest := s[idx+len("time="):]
	end := strings.IndexAny(rest, " \r\n<")
	if end >= 0 {
		rest = rest[:end]
	}
	rest = strings.TrimSuffix(strings.ToLower(rest), "ms")
	v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return nil
	}
	return &v
}

// ---------- Redis (minimal RESP client, no external dependency) ----------

type RedisChecker struct{ defaultTimeout time.Duration }

func NewRedisChecker(timeout time.Duration) *RedisChecker {
	return &RedisChecker{defaultTimeout: timeout}
}

func (c *RedisChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	host := m.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := m.Port
	if port == 0 {
		port = 6379
	}

	d := sysdns.NewDialer(timeout)
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(float64(time.Since(start).Milliseconds()))}, nil
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}

	reader := bufio.NewReader(conn)
	send := func(args ...string) error {
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(args))
		for _, a := range args {
			fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
		}
		_, err := conn.Write([]byte(b.String()))
		return err
	}
	readReply := func() (string, error) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	if m.RedisPassword != "" {
		if err := send("AUTH", m.RedisPassword); err != nil {
			return &CheckOutcome{Status: models.StatusDown, Message: "auth write failed: " + err.Error()}, nil
		}
		reply, err := readReply()
		if err != nil {
			return &CheckOutcome{Status: models.StatusDown, Message: "auth read failed: " + err.Error()}, nil
		}
		if strings.HasPrefix(reply, "-") {
			return &CheckOutcome{Status: models.StatusDown, Message: "auth failed: " + strings.TrimPrefix(reply, "-")}, nil
		}
	}

	cmd := []string{"PING"}
	if m.RedisKey != "" {
		cmd = []string{"EXISTS", m.RedisKey}
	}
	if err := send(cmd...); err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error()}, nil
	}
	reply, err := readReply()
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: "read failed: " + err.Error(), LatencyMs: ms(latency)}, nil
	}
	if strings.HasPrefix(reply, "-") {
		return &CheckOutcome{Status: models.StatusDown, Message: strings.TrimPrefix(reply, "-"), LatencyMs: ms(latency)}, nil
	}

	if m.RedisKey != "" {
		// EXISTS returns :0 or :1
		if reply == ":1" {
			return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: "key exists"}, nil
		}
		return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: "key does not exist"}, nil
	}
	return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: "PONG"}, nil
}

// ---------- Database query (Postgres / MySQL via database/sql) ----------

type DBChecker struct{ defaultTimeout time.Duration }

func NewDBChecker(timeout time.Duration) *DBChecker { return &DBChecker{defaultTimeout: timeout} }

func (c *DBChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	driver := strings.ToLower(m.DBDriver)
	if driver == "" {
		driver = "pgx"
	} else if driver == "postgres" || driver == "postgresql" {
		driver = "pgx"
	}
	if m.DSN == "" {
		return &CheckOutcome{Status: models.StatusDown, Message: "no connection string configured"}, nil
	}

	db, err := sql.Open(driver, m.DSN)
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: "open: " + err.Error()}, nil
	}
	defer db.Close()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	if err := db.PingContext(cctx); err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(float64(time.Since(start).Milliseconds()))}, nil
	}

	query := strings.TrimSpace(m.ExecQuery)
	if query == "" {
		// No query: a successful connection is enough.
		return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(float64(time.Since(start).Milliseconds())), Message: "connected"}, nil
	}

	rows, err := db.QueryContext(cctx, query)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: "query: " + err.Error(), LatencyMs: ms(latency)}, nil
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: "rows: " + err.Error(), LatencyMs: ms(latency)}, nil
	}

	if m.ExpectedRows > 0 && count < m.ExpectedRows {
		return &CheckOutcome{Status: models.StatusDown, LatencyMs: ms(latency), Message: fmt.Sprintf("query returned %d row(s), expected >= %d", count, m.ExpectedRows)}, nil
	}
	return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: fmt.Sprintf("query ok, %d row(s)", count)}, nil
}
