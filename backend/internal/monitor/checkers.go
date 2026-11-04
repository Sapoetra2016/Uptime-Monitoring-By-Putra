package monitor

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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

type HTTPChecker struct {
	client         *http.Client
	insecureClient *http.Client
}

func NewHTTPChecker(timeout time.Duration) *HTTPChecker {
	strictTransport := &http.Transport{
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
	}
	// A second client that skips TLS verification, used only when a monitor has
	// "Ignore SSL" enabled. Default monitors keep strict verification.
	insecureTransport := &http.Transport{
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	return &HTTPChecker{
		client:         &http.Client{Timeout: timeout, Transport: strictTransport},
		insecureClient: &http.Client{Timeout: timeout, Transport: insecureTransport},
	}
}

func (c *HTTPChecker) Check(ctx context.Context, m *models.Monitor) (*CheckOutcome, error) {
	method := m.Method
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if m.Body != "" {
		bodyReader = strings.NewReader(m.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.URL, bodyReader)
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error()}, nil
	}
	req.Header.Set("User-Agent", "UptimeMonitor/1.0")
	for k, v := range m.Headers {
		req.Header.Set(k, v)
	}
	if m.BasicAuthUser != "" || m.BasicAuthPass != "" {
		req.SetBasicAuth(m.BasicAuthUser, m.BasicAuthPass)
	}

	start := time.Now()
	client := c.client
	if m.IgnoreTLS {
		client = c.insecureClient
	}
	resp, err := client.Do(req)
	latency := float64(time.Since(start).Milliseconds())
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
	return outcome, nil
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
	d := net.Dialer{Timeout: timeout}

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

	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, port), &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	})
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return &CheckOutcome{Status: models.StatusDown, Message: err.Error(), LatencyMs: ms(latency)}, nil
	}
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
	if daysRemaining <= warnDays {
		return &CheckOutcome{Status: models.StatusDegraded, LatencyMs: ms(latency), Message: fmt.Sprintf("certificate expires in %d day(s)", daysRemaining), Meta: meta}, nil
	}
	return &CheckOutcome{Status: models.StatusUp, LatencyMs: ms(latency), Message: fmt.Sprintf("certificate valid, %d day(s) remaining", daysRemaining), Meta: meta}, nil
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

	d := net.Dialer{Timeout: timeout}
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
