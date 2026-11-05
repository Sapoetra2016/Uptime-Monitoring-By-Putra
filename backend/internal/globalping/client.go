// Package globalping is a small client for the open Globalping API
// (https://api.globalping.io). It runs real ping tests from community-hosted
// probes around the world — no API key required, only outbound HTTPS.
// This app uses it for the "ping from Asia" multi-country latency feature.
package globalping

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/doh"
)

const baseURL = "https://api.globalping.io/v1"

// asiaCountries is the preferred probe-country shortlist, restricted to Asia.
// Countries without online probes at request time are silently skipped.
var asiaCountries = []string{"ID", "SG", "MY", "TH", "VN", "PH", "JP", "KR", "TW", "HK", "IN"}

// fallbackAsia is used when the /locations endpoint is slow or unreachable. It
// covers the full preferred Asia shortlist (including Indonesia and Malaysia)
// so the whole region is still represented in the degraded path. If one of
// these happens to have no online probe right now, MultiPing retries once with
// conservativeAsia so the test never fails outright.
var fallbackAsia = []string{"ID", "SG", "MY", "TH", "VN", "PH", "JP", "KR", "TW", "HK", "IN"}

// conservativeAsia is the last-resort shortlist of countries that almost always
// have at least one online Globalping probe.
var conservativeAsia = []string{"SG", "JP", "IN", "HK", "KR", "TW"}

// ProbeResult is one row of the multi-country ping outcome.
type ProbeResult struct {
	Country string  `json:"country"`
	City    string  `json:"city"`
	Network string  `json:"network"`
	MinMs   float64 `json:"minMs"`
	AvgMs   float64 `json:"avgMs"`
	MaxMs   float64 `json:"maxMs"`
	LossPct float64 `json:"lossPct"`
}

type Client struct {
	http *http.Client

	// /locations changes rarely, so cache it process-wide. The slow request
	// then happens at most once per TTL instead of on every ping test.
	locsMu    sync.Mutex
	locsCache []string
	locsAt    time.Time
}

const locationsTTL = 10 * time.Minute

func New() *Client {
	// DoH-first dial: some Indonesian resolvers return a sinkholed Fastly IP
	// for api.globalping.io that silently drops TCP 443.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = doh.NewDialer(10 * time.Second).DialContext
	return &Client{http: &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		// Never follow redirects off the Globalping API host (SSRF guard).
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != "api.globalping.io" {
				return fmt.Errorf("refusing redirect to %s", req.URL.Host)
			}
			return nil
		},
	}}
}

// ---- API response shapes (only the fields we consume) ----

type location struct {
	Country string `json:"country"`
	Count   int    `json:"count"`
}

type createResp struct {
	ID string `json:"id"`
}

type measurement struct {
	Status  string `json:"status"`
	Results []struct {
		Probe struct {
			City    string `json:"city"`
			Country string `json:"country"`
			Network string `json:"network"`
		} `json:"probe"`
		Result struct {
			Stats *struct {
				Min  *float64 `json:"min"`
				Avg  *float64 `json:"avg"`
				Max  *float64 `json:"max"`
				Loss *float64 `json:"loss"`
			} `json:"stats"`
		} `json:"result"`
	} `json:"results"`
}

// availableAsia returns the preferred countries that currently have at least
// one online probe, preserving the shortlist order. Results are cached for
// locationsTTL; if /locations fails the caller falls back to fallbackAsia
// (never cached, so the next attempt retries the live list).
func (c *Client) availableAsia(ctx context.Context) ([]string, error) {
	c.locsMu.Lock()
	if c.locsCache != nil && time.Since(c.locsAt) < locationsTTL {
		out := c.locsCache
		c.locsMu.Unlock()
		return out, nil
	}
	c.locsMu.Unlock()

	var locs []location
	if err := c.getJSON(ctx, "/locations", &locs); err != nil {
		return nil, err
	}
	online := map[string]int{}
	for _, l := range locs {
		online[l.Country] += l.Count
	}
	var out []string
	for _, cc := range asiaCountries {
		if online[cc] > 0 {
			out = append(out, cc)
		}
	}
	c.locsMu.Lock()
	c.locsCache = out
	c.locsAt = time.Now()
	c.locsMu.Unlock()
	return out, nil
}

// MultiPing pings target once from each available Asian country and waits
// (up to ~30s) for the results. Partial results are returned on timeout. If a
// country in the preferred list happens to have no online probe right now,
// Globalping rejects the whole measurement (HTTP 422); in that case we retry
// once with the conservative always-has-probes list so the test still runs.
func (c *Client) MultiPing(ctx context.Context, target string) ([]ProbeResult, error) {
	countries, err := c.availableAsia(ctx)
	if err != nil || len(countries) == 0 {
		// /locations unreachable or returned nothing usable — use the
		// region-wide fallback (includes Indonesia/Malaysia) instead of
		// failing the test, and cache that outcome briefly so we don't
		// re-probe every request.
		countries = fallbackAsia
		c.locsMu.Lock()
		if c.locsCache == nil {
			c.locsCache = fallbackAsia
			c.locsAt = time.Now().Add(-locationsTTL + 2*time.Minute)
		}
		c.locsMu.Unlock()
	}

	res, merr := c.measure(ctx, target, countries)
	if merr != nil && strings.Contains(merr.Error(), "(422)") && !sameList(countries, conservativeAsia) {
		// A requested country had no online probe; drop down to the
		// conservative list so Indonesia-less countries still return a result.
		res, merr = c.measure(ctx, target, conservativeAsia)
	}
	return res, merr
}

// measure creates and polls a single Globalping measurement for the given
// countries, returning one row per probe that reported back.
func (c *Client) measure(ctx context.Context, target string, countries []string) ([]ProbeResult, error) {
	locations := make([]map[string]interface{}, 0, len(countries))
	for _, cc := range countries {
		locations = append(locations, map[string]interface{}{"country": cc, "limit": 1})
	}
	body, _ := json.Marshal(map[string]interface{}{
		"type":               "ping",
		"target":             target,
		"locations":          locations,
		"measurementOptions": map[string]interface{}{"packets": 4},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/measurements", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("globalping unreachable: %w", err)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("globalping create failed (%d): %s", resp.StatusCode, string(data))
	}
	var cr createResp
	if err := json.Unmarshal(data, &cr); err != nil || !validMeasurementID(cr.ID) {
		return nil, fmt.Errorf("globalping returned no measurement id")
	}

	// Poll until the measurement finishes (or give up after 30s and serve
	// whatever partial results exist).
	deadline := time.Now().Add(30 * time.Second)
	var m measurement
	for {
		if err := c.getJSON(ctx, "/measurements/"+cr.ID, &m); err != nil {
			return nil, err
		}
		if m.Status == "finished" || m.Status == "failed" || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
	if len(m.Results) == 0 {
		return nil, fmt.Errorf("globalping returned no results (status %s)", m.Status)
	}

	out := make([]ProbeResult, 0, len(m.Results))
	for _, r := range m.Results {
		pr := ProbeResult{Country: r.Probe.Country, City: r.Probe.City, Network: r.Probe.Network}
		if s := r.Result.Stats; s != nil {
			if s.Min != nil {
				pr.MinMs = *s.Min
			}
			if s.Avg != nil {
				pr.AvgMs = *s.Avg
			}
			if s.Max != nil {
				pr.MaxMs = *s.Max
			}
			if s.Loss != nil {
				pr.LossPct = *s.Loss
			}
		}
		out = append(out, pr)
	}
	return out, nil
}

// sameList reports whether two country shortlists are element-wise identical.
func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validMeasurementID keeps the poll URL path safe: Globalping ids are short
// alphanumeric tokens, anything else is rejected instead of interpolated.
func validMeasurementID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (c *Client) getJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("globalping unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("globalping %s failed (%d): %s", path, resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}
