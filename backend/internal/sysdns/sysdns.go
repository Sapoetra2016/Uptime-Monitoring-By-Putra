// Package sysdns makes outbound checks follow the server's own DNS setup
// (/etc/resolv.conf) and survive transient resolver hiccups.
//
// Background: the binary is cross-compiled with CGO_ENABLED=0, so Go's
// pure resolver talks to systemd-resolved/upstream servers directly and has
// no retry path when a nameserver briefly answers SERVFAIL ("server
// misbehaving") — even though ping/curl on the same box resolve fine. The
// Dialer here layers: system dial → direct queries against the nameservers
// listed in resolv.conf (retried) → DNS-over-HTTPS as a last resort.
package sysdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/uptime/monitoring/internal/doh"
)

// conf is the slice of /etc/resolv.conf we honor, re-read when stale.
type conf struct {
	servers []string
	search  []string
	at      time.Time
}

var (
	confMu     sync.Mutex
	cached     *conf
	confTTL    = 60 * time.Second
	resolvPath = "/etc/resolv.conf"
)

func loadConf() *conf {
	confMu.Lock()
	defer confMu.Unlock()
	if cached != nil && time.Since(cached.at) < confTTL {
		return cached
	}
	c := &conf{at: time.Now()}
	if data, err := os.ReadFile(resolvPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") || strings.HasPrefix(fields[0], ";") {
				continue
			}
			switch fields[0] {
			case "nameserver":
				c.servers = append(c.servers, fields[1])
			case "search":
				for _, s := range fields[1:] {
					// "search ." means no search domains.
					if s != "." {
						c.search = append(c.search, s)
					}
				}
			}
		}
	}
	if len(c.servers) == 0 {
		c.servers = []string{"127.0.0.53"}
	}
	cached = c
	return c
}

// LookupHost resolves host through the resolv.conf nameservers directly,
// trying each server up to twice (UDP, TCP fallback on truncation) before
// moving on, and applying the resolv.conf search list.
func LookupHost(ctx context.Context, host string) ([]string, error) {
	c := loadConf()
	names := []string{dns.Fqdn(host)}
	if !strings.HasSuffix(host, ".") {
		for _, s := range c.search {
			names = append(names, dns.Fqdn(host+"."+s))
		}
	}
	var lastErr error
	for _, name := range names {
		for _, server := range c.servers {
			for attempt := 0; attempt < 2; attempt++ {
				addrs, err := queryA(ctx, name, server)
				if err != nil {
					lastErr = err
					continue
				}
				if len(addrs) > 0 {
					return addrs, nil
				}
				lastErr = fmt.Errorf("%s: no A record for %s", server, name)
			}
		}
	}
	if lastErr == nil {
		lastErr = &net.DNSError{Err: "no such host", Name: host}
	}
	return nil, lastErr
}

// queryA asks one nameserver for A records of name, following the CNAME
// chain inside the answer section and retrying over TCP when truncated.
func queryA(ctx context.Context, name, server string) ([]string, error) {
	if !strings.Contains(server, ":") {
		server += ":53"
	}
	msg := new(dns.Msg)
	msg.SetQuestion(name, dns.TypeA)
	msg.RecursionDesired = true

	client := &dns.Client{Timeout: 4 * time.Second}
	resp, _, err := client.ExchangeContext(ctx, msg, server)
	if err == nil && resp.Truncated {
		client.Net = "tcp"
		resp, _, err = client.ExchangeContext(ctx, msg, server)
	}
	if err != nil {
		return nil, err
	}
	if resp.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("%s: %s", server, dns.RcodeToString[resp.Rcode])
	}
	// Collect A records whose owner is the queried name or any CNAME it
	// points to (the answer section carries the whole chain).
	reachable := map[string]bool{strings.ToLower(name): true}
	for changed := true; changed; {
		changed = false
		for _, rr := range resp.Answer {
			if cn, ok := rr.(*dns.CNAME); ok {
				key := strings.ToLower(rr.Header().Name)
				if reachable[key] && !reachable[strings.ToLower(cn.Target)] {
					reachable[strings.ToLower(cn.Target)] = true
					changed = true
				}
			}
		}
	}
	var out []string
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok && reachable[strings.ToLower(rr.Header().Name)] {
			out = append(out, a.A.String())
		}
	}
	return out, nil
}

// Dialer dials TCP with the layered strategy described in the package doc.
// The zero value is not usable; construct with NewDialer.
type Dialer struct {
	Fallback *net.Dialer // plain dial (system resolver / IP literals)

	dohD *doh.Dialer

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	addrs []string
	at    time.Time
}

// lookupTTL is short on purpose: monitor checks run every minute, and we do
// not want to pin a stale internal IP for long.
const lookupTTL = 15 * time.Second

// NewDialer returns a Dialer whose plain dials use the given timeout.
func NewDialer(timeout time.Duration) *Dialer {
	return &Dialer{
		Fallback: &net.Dialer{Timeout: timeout},
		dohD:     doh.NewDialer(timeout),
		cache:    map[string]cacheEntry{},
	}
}

// DialContext tries the system resolver first; when name resolution fails it
// queries the resolv.conf nameservers directly (with retries), and only then
// falls back to public DoH (useful where local DNS is sinkholed).
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.Fallback.DialContext(ctx, network, addr)
	if err == nil {
		return conn, nil
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return nil, err // connect error on a resolved address: report as-is
	}
	host, port, serr := net.SplitHostPort(addr)
	if serr != nil {
		return nil, err
	}
	if ips, lerr := d.lookupCached(ctx, host); lerr == nil {
		for _, ip := range ips {
			conn, derr := d.Fallback.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if derr == nil {
				return conn, nil
			}
			err = derr
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
	}
	// Last resort: DoH (also covers poisoned local DNS returning dead IPs
	// after the direct query agreed with them).
	return d.dohD.DialContext(ctx, network, addr)
}

func (d *Dialer) lookupCached(ctx context.Context, host string) ([]string, error) {
	if net.ParseIP(host) != nil {
		return []string{host}, nil
	}
	d.mu.Lock()
	if e, ok := d.cache[host]; ok && time.Since(e.at) < lookupTTL {
		d.mu.Unlock()
		return e.addrs, nil
	}
	d.mu.Unlock()

	addrs, err := LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.cache[host] = cacheEntry{addrs: addrs, at: time.Now()}
	d.mu.Unlock()
	return addrs, nil
}
