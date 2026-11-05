// Package doh provides a TCP dialer that resolves hostnames through public
// DNS-over-HTTPS (dns.google) before falling back to the system resolver.
//
// Background: some Indonesian resolvers return sinkholed IPs (e.g.
// 121.46.26.158 for Fastly-fronted services) that silently drop or refuse
// connections. Asking Google's DoH JSON API directly gives the real anycast
// addresses, and dns.google itself is never filtered.
package doh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// cacheTTL is how long a DoH answer is reused per host.
const cacheTTL = 5 * time.Minute

// Dialer dials TCP connections with DoH-first name resolution. The zero value
// is not usable; construct with NewDialer.
type Dialer struct {
	Fallback *net.Dialer // used when DoH fails or yields no connect

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	addrs []string
	at    time.Time
}

// NewDialer returns a Dialer whose plain fallback dial uses the given timeout.
func NewDialer(timeout time.Duration) *Dialer {
	return &Dialer{
		Fallback: &net.Dialer{Timeout: timeout},
		cache:    map[string]cacheEntry{},
	}
}

// dohClient resolves dns.google itself with system DNS (that host is not
// filtered), so a plain client is fine here.
var dohClient = &http.Client{Timeout: 10 * time.Second}

// DialContext tries every IPv4 that DoH returns for host, in order, then
// falls back to a normal system-DNS dial of the original address.
func (d *Dialer) DialContext(ctx context.Context, _, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.Fallback.DialContext(ctx, "tcp", addr)
	}
	if addrs, lerr := d.lookup(ctx, host); lerr == nil {
		for _, ip := range addrs {
			conn, derr := d.Fallback.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if derr == nil {
				return conn, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
	}
	return d.Fallback.DialContext(ctx, "tcp", addr)
}

// lookup returns cached or fresh DoH IPv4 answers for host.
func (d *Dialer) lookup(ctx context.Context, host string) ([]string, error) {
	d.mu.Lock()
	if e, ok := d.cache[host]; ok && time.Since(e.at) < cacheTTL {
		d.mu.Unlock()
		return e.addrs, nil
	}
	d.mu.Unlock()

	ctx2, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodGet,
		"https://dns.google/resolve?name="+url.QueryEscape(host)+"&type=A", nil)
	if err != nil {
		return nil, err
	}
	resp, err := dohClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Answer []struct {
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, err
	}
	var addrs []string
	for _, a := range out.Answer {
		if ip := net.ParseIP(a.Data); ip != nil && ip.To4() != nil {
			addrs = append(addrs, a.Data)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("doh returned no IPv4 answers for %s", host)
	}
	d.mu.Lock()
	d.cache[host] = cacheEntry{addrs: addrs, at: time.Now()}
	d.mu.Unlock()
	return addrs, nil
}
