package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/ai"
	"github.com/uptime/monitoring/internal/events"
	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/store"
)

// Engine owns a goroutine per enabled monitor and reacts to config changes
// without restarting the whole process (Add/Update/Remove at runtime).
type Engine struct {
	store    *store.Store
	hub      *events.Hub
	notifier NotifierHook
	checkers map[models.MonitorType]Checker

	mu      sync.Mutex
	runners map[string]*runner // monitorID -> runner
	cancel  context.CancelFunc

	// AI root-cause analysis (optional; nil/disabled = feature off).
	ai       *ai.Client
	aiMu     sync.Mutex
	lastAIAt map[string]time.Time // monitorID -> last auto-analysis, for debouncing
}

type NotifierHook interface {
	OnStatusChange(ctx context.Context, m *models.Monitor, oldStatus, newStatus models.MonitorStatus, message string, downSince time.Time)
}

type runner struct {
	cancel context.CancelFunc
}

func NewEngine(st *store.Store, hub *events.Hub, notifier NotifierHook, defaultTimeout time.Duration) *Engine {
	return &Engine{
		store:    st,
		hub:      hub,
		notifier: notifier,
		checkers: map[models.MonitorType]Checker{
			models.TypeHTTP:   NewHTTPChecker(defaultTimeout),
			models.TypeTCP:    NewTCPChecker(defaultTimeout),
			models.TypeDNS:    NewDNSChecker(defaultTimeout),
			models.TypeCert:   NewCertChecker(defaultTimeout),
			models.TypePush:   NewPushChecker(),
			models.TypeDocker: NewDockerChecker(defaultTimeout),
			models.TypePing:   NewPingChecker(defaultTimeout),
			models.TypeRedis:  NewRedisChecker(defaultTimeout),
			models.TypeDB:     NewDBChecker(defaultTimeout),
		},
		runners:  map[string]*runner{},
		lastAIAt: map[string]time.Time{},
	}
}

// SetAIClient enables automatic AI root-cause analysis on down events.
func (e *Engine) SetAIClient(c *ai.Client) { e.ai = c }

func (e *Engine) Start(ctx context.Context) error {
	ctx, e.cancel = context.WithCancel(ctx)

	monitors, err := e.store.ListMonitors(ctx)
	if err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}
	for i := range monitors {
		m := &monitors[i]
		if !m.Enabled {
			continue
		}
		// Catch-up: a monitor that was already down when the engine started
		// (e.g. after a backend restart) still deserves an open incident.
		if m.Status == models.StatusDown {
			e.openIncident(ctx, m, &CheckOutcome{Status: m.Status, Message: m.StatusReason})
		}
		e.Run(m)
	}
	log.Printf("[engine] started %d monitor runner(s)", len(e.runners))
	return nil
}

func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, r := range e.runners {
		r.cancel()
		delete(e.runners, id)
	}
}

// Run (re)starts the runner for a monitor; safe to call on create/update.
func (e *Engine) Run(m *models.Monitor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.runners[m.ID]; ok {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.runners[m.ID] = &runner{cancel: cancel}
	go e.loop(ctx, m)
}

// Remove stops a runner (used on delete/disable).
func (e *Engine) Remove(monitorID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runners[monitorID]; ok {
		r.cancel()
		delete(e.runners, monitorID)
	}
}

func (e *Engine) loop(ctx context.Context, m *models.Monitor) {
	interval := time.Duration(m.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}

	// immediate first check
	e.checkOnce(ctx, m)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// always re-fetch latest config so updates take effect even if Run() wasn't called
			latest, err := e.store.GetMonitor(ctx, m.ID)
			if err != nil || !latest.Enabled {
				return
			}
			e.checkOnce(ctx, latest)
		}
	}
}

func (e *Engine) checkOnce(ctx context.Context, m *models.Monitor) {
	checker, ok := e.checkers[m.Type]
	if !ok {
		log.Printf("[engine] no checker for type %q (monitor %s)", m.Type, m.ID)
		return
	}

	// Notify the matching UI node that a check is starting so it can show a
	// per-node "checking" indicator. Push monitors are event-driven, so skip.
	if m.Type != models.TypePush {
		e.hub.BroadcastCheckStarted(m.ID)
	}

	timeoutSec := m.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+2)*time.Second)
	defer cancel()

	outcome, err := checker.Check(cctx, m)
	if err != nil {
		outcome = &CheckOutcome{Status: models.StatusDown, Message: "checker error: " + err.Error()}
	}

	if m.Type == models.TypePush {
		e.evaluatePush(ctx, m, outcome)
		return
	}

	applyLatencyThreshold(m, outcome)
	e.persistAndBroadcast(ctx, m, outcome)
}

// applyLatencyThreshold downgrades a healthy result to "degraded" when the
// monitor has a latency SLO configured and the observed latency exceeds it.
func applyLatencyThreshold(m *models.Monitor, outcome *CheckOutcome) {
	if outcome == nil || m.LatencyWarnMs <= 0 {
		return
	}
	if outcome.Status == models.StatusUp && outcome.LatencyMs != nil && *outcome.LatencyMs > float64(m.LatencyWarnMs) {
		outcome.Status = models.StatusDegraded
		outcome.Message = fmt.Sprintf("up but slow: %.0fms (SLO %dms)", *outcome.LatencyMs, m.LatencyWarnMs)
	}
}

func (e *Engine) evaluatePush(ctx context.Context, m *models.Monitor, outcome *CheckOutcome) {
	lastPush, err := e.store.GetLastPushAt(ctx, m.ID)
	if err != nil {
		outcome.Status = models.StatusPending
		outcome.Message = "no heartbeat received yet"
		e.persistAndBroadcast(ctx, m, outcome)
		return
	}
	deadline := time.Duration(m.IntervalSec+m.PushGraceSec) * time.Second
	if time.Since(*lastPush) > deadline {
		outcome.Status = models.StatusDown
		outcome.Message = fmt.Sprintf("no heartbeat in last %s", deadline)
	} else {
		outcome.Status = models.StatusUp
		outcome.Message = fmt.Sprintf("heartbeat received %s ago", time.Since(*lastPush).Round(time.Second))
	}
	e.persistAndBroadcast(ctx, m, outcome)
}

func (e *Engine) persistAndBroadcast(ctx context.Context, m *models.Monitor, outcome *CheckOutcome) {
	metaRaw, _ := json.Marshal(outcome.Meta)

	check := &models.CheckResult{
		MonitorID:  m.ID,
		Status:     outcome.Status,
		LatencyMs:  outcome.LatencyMs,
		HTTPStatus: outcome.HTTPStatus,
		Message:    outcome.Message,
		Meta:       metaRaw,
	}
	if err := e.store.InsertCheck(ctx, check); err != nil {
		log.Printf("[engine] insert check for %s: %v", m.ID, err)
	}

	oldStatus := m.Status
	changed := oldStatus != outcome.Status
	// Only persist "state_since" change when status actually flips.
	if err := e.store.SetMonitorState(ctx, m.ID, outcome.Status, outcome.LatencyMs, outcome.Message, changed); err != nil {
		log.Printf("[engine] set state for %s: %v", m.ID, err)
	}

	e.hub.Broadcast(events.MonitorUpdate{
		Type:      "monitor.update",
		MonitorID: m.ID,
		Name:      m.Name,
		Status:    string(outcome.Status),
		LatencyMs: outcome.LatencyMs,
		Message:   outcome.Message,
		Changed:   changed,
		At:        check.CreatedAt,
	})

	if changed && (outcome.Status == models.StatusDown || outcome.Status == models.StatusDegraded) && e.notifier != nil {
		downSince := time.Now()
		if s, err := e.store.GetStateSince(ctx, m.ID); err == nil {
			downSince = s
		}
		e.notifier.OnStatusChange(ctx, m, oldStatus, outcome.Status, outcome.Message, downSince)
	}
	// Fire an async LLM analysis only on transitions into Down (not degraded),
	// debounced per monitor so flapping services don't burn API quota.
	if changed && outcome.Status == models.StatusDown && e.ai != nil && e.ai.Enabled() && e.tryClaimAnalysis(m.ID) {
		go e.runAnalysis(context.Background(), m, outcome, "auto")
	}
	if changed && outcome.Status == models.StatusUp && e.notifier != nil {
		e.notifier.OnStatusChange(ctx, m, oldStatus, outcome.Status, outcome.Message, time.Now())
	}
	// Auto-record outages as incidents: open one when the monitor goes Down,
	// resolve it when the monitor recovers. Keeps the "latest incidents" list
	// and public status pages in sync with real downtime without manual posts.
	if changed && outcome.Status == models.StatusDown {
		e.openIncident(ctx, m, outcome)
	}
	if changed && outcome.Status == models.StatusUp {
		e.resolveIncidents(ctx, m)
	}
}

// openIncident creates an active incident for a fresh outage. Deduplicated:
// a monitor never has more than one open incident.
func (e *Engine) openIncident(ctx context.Context, m *models.Monitor, outcome *CheckOutcome) {
	open, err := e.store.GetOpenIncident(ctx, m.ID)
	if err != nil {
		log.Printf("[engine] lookup open incident for %s: %v", m.ID, err)
		return
	}
	if open != nil {
		return // this outage is already tracked
	}
	inc := &models.Incident{
		MonitorID: m.ID,
		Status:    "identified",
		Headline:  fmt.Sprintf("%s is DOWN", m.Name),
		Body:      incidentCause(outcome),
	}
	if err := e.store.CreateIncident(ctx, inc); err != nil {
		log.Printf("[engine] open incident for %s: %v", m.ID, err)
		return
	}
	e.hub.BroadcastIncident(events.IncidentUpdate{
		Type: "incident.update", IncidentID: inc.ID, MonitorID: inc.MonitorID, Status: inc.Status, Headline: inc.Headline,
	})
}

// incidentCause builds the incident detail text: the raw checker message plus
// the last observed HTTP code / latency, so the timeline shows *why* it failed
// (timeout, connection refused, HTTP 503, ...).
func incidentCause(outcome *CheckOutcome) string {
	cause := outcome.Message
	if cause == "" {
		cause = "check failed"
	}
	if outcome.HTTPStatus != 0 {
		cause += fmt.Sprintf(" (HTTP %d)", outcome.HTTPStatus)
	}
	if outcome.LatencyMs != nil {
		cause += fmt.Sprintf(" · latency %.0fms", *outcome.LatencyMs)
	}
	return cause
}

// resolveIncidents closes any open incident of a monitor that just recovered.
func (e *Engine) resolveIncidents(ctx context.Context, m *models.Monitor) {
	resolved, err := e.store.ResolveOpenIncidents(ctx, m.ID, time.Now().UTC())
	if err != nil {
		log.Printf("[engine] resolve incidents for %s: %v", m.ID, err)
		return
	}
	for _, inc := range resolved {
		e.hub.BroadcastIncident(events.IncidentUpdate{
			Type: "incident.update", IncidentID: inc.ID, MonitorID: inc.MonitorID, Status: inc.Status, Headline: inc.Headline,
		})
	}
}

// ManualCheck triggers an immediate check for a monitor (used by API "check now" button).
func (e *Engine) ManualCheck(m *models.Monitor) {
	go e.checkOnce(context.Background(), m)
}

// aiAnalysisGap is how often an auto-analysis may run per monitor.
const aiAnalysisGap = 15 * time.Minute

// tryClaimAnalysis returns true (and records the timestamp) when the monitor
// has not been auto-analyzed within the debounce window.
func (e *Engine) tryClaimAnalysis(monitorID string) bool {
	e.aiMu.Lock()
	defer e.aiMu.Unlock()
	if last, ok := e.lastAIAt[monitorID]; ok && time.Since(last) < aiAnalysisGap {
		return false
	}
	e.lastAIAt[monitorID] = time.Now()
	return true
}

// AIEnabled reports whether the LLM analysis feature is configured.
func (e *Engine) AIEnabled() bool { return e.ai != nil && e.ai.Enabled() }

// AnalyzeSync runs an on-demand analysis (admin "Re-analyze" button) and
// persists the result. It blocks until the LLM responds.
func (e *Engine) AnalyzeSync(ctx context.Context, m *models.Monitor) (*models.MonitorAnalysis, error) {
	if !e.AIEnabled() {
		return nil, fmt.Errorf("AI analysis is not configured on this server")
	}
	outcome, err := e.runAnalysis(ctx, m, nil, "manual")
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

// runAnalysis collects monitor context + recent checks, asks the LLM for a
// root-cause report, stores it, and returns the persisted analysis.
func (e *Engine) runAnalysis(ctx context.Context, m *models.Monitor, outcome *CheckOutcome, trigger string) (*models.MonitorAnalysis, error) {
	checks, _ := e.store.RecentChecks(ctx, m.ID, 20)
	sys, user := buildAnalysisPrompt(m, outcome, checks)

	// Give the LLM a generous budget of 60s on top of its own http timeout.
	actx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	text, err := e.ai.Chat(actx, sys, user)
	if err != nil {
		log.Printf("[ai] analysis for monitor %s (%s): %v", m.ID, trigger, err)
		return nil, err
	}
	status := m.Status
	if outcome != nil {
		status = outcome.Status
	}
	a := &models.MonitorAnalysis{
		MonitorID: m.ID,
		Status:    status,
		Analysis:  text,
		Model:     e.ai.Model(),
	}
	if err := e.store.InsertAnalysis(actx, a); err != nil {
		log.Printf("[ai] store analysis for %s: %v", m.ID, err)
		return nil, err
	}
	log.Printf("[ai] stored %s analysis for monitor %s (%d chars)", trigger, m.ID, len(text))
	return a, nil
}

const aiSystemPrompt = `Anda adalah analis senior SRE untuk sistem uptime monitoring.
Pengguna memberi Anda konfigurasi monitor dan hasil pemeriksaan terbaru saat servis berstatus DOWN.
Tulis analisis dalam Bahasa Indonesia, ringkas dan praktis, dalam format markdown sederhana:
1. **Kemungkinan penyebab** (urutkan dari yang paling mungkin, 2-4 butir)
2. **Langkah diagnosis** (perintah/konkret, misalnya curl, cek DNS, cek log container)
3. **Tindakan perbaikan yang disarankan**
Perhatikan kode status HTTP, pesan error (timeout/TLS/DNS/connection refused), dan pola latensi.
Jangan mengarang detail yang tidak ada di data. Jangan sertakan dugaan yang berkaitan dengan kredensial/rahasia.`

// buildAnalysisPrompt renders the monitor context + recent checks into the
// user message. Config secrets (DSN, basic auth, redis password, headers,
// body) are deliberately excluded.
func buildAnalysisPrompt(m *models.Monitor, outcome *CheckOutcome, checks []models.CheckResult) (system, user string) {
	var b strings.Builder
	b.WriteString("Detail monitor:\n")
	fmt.Fprintf(&b, "- Nama: %s\n", m.Name)
	fmt.Fprintf(&b, "- Tipe: %s\n", m.Type)
	if m.URL != "" {
		fmt.Fprintf(&b, "- URL: %s\n", m.URL)
	}
	if m.Host != "" {
		fmt.Fprintf(&b, "- Host: %s (port %d)\n", m.Host, m.Port)
	}
	if m.Method != "" {
		fmt.Fprintf(&b, "- Metode: %s, expected status: %d\n", m.Method, m.ExpectedStatus)
	}
	if m.Keyword != "" {
		fmt.Fprintf(&b, "- Keyword yang dicari: %q\n", m.Keyword)
	}
	fmt.Fprintf(&b, "- Interval cek: %ds, timeout: %ds\n", m.IntervalSec, m.TimeoutSec)
	if m.LocalIp != "" {
		fmt.Fprintf(&b, "- Catatan IP lokal (admin): %s\n", m.LocalIp)
	}

	if outcome != nil {
		fmt.Fprintf(&b, "\nStatus saat ini: %s, pesan: %s\n", outcome.Status, outcome.Message)
		if outcome.HTTPStatus != 0 {
			fmt.Fprintf(&b, "Kode status HTTP terakhir: %d\n", outcome.HTTPStatus)
		}
		if outcome.LatencyMs != nil {
			fmt.Fprintf(&b, "Latensi terakhir: %.0f ms\n", *outcome.LatencyMs)
		}
	}

	if len(checks) > 0 {
		b.WriteString("\nPemeriksaan terbaru (terbaru → terlama):\n")
		for _, c := range checks {
			lat := "-"
			if c.LatencyMs != nil {
				lat = fmt.Sprintf("%.0fms", *c.LatencyMs)
			}
			http := "-"
			if c.HTTPStatus != 0 {
				http = fmt.Sprintf("%d", c.HTTPStatus)
			}
			fmt.Fprintf(&b, "- [%s] %s | http %s | %s | %s\n",
				c.CreatedAt.Format("2006-01-02 15:04:05"), c.Status, http, lat, c.Message)
		}
	}
	return aiSystemPrompt, b.String()
}
