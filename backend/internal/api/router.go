package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/gorilla/websocket"
	"github.com/uptime/monitoring/internal/ai"
	"github.com/uptime/monitoring/internal/auth"
	"github.com/uptime/monitoring/internal/domainssl"
	"github.com/uptime/monitoring/internal/events"
	"github.com/uptime/monitoring/internal/globalping"
	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/monitor"
	"github.com/uptime/monitoring/internal/notifier"
	"github.com/uptime/monitoring/internal/report"
	"github.com/uptime/monitoring/internal/store"
	"github.com/uptime/monitoring/internal/sysdns"
)

type Server struct {
	Store          *store.Store
	Engine         *monitor.Engine
	Hub            *events.Hub
	Notifier       *notifier.Dispatcher
	Reports        *report.Scheduler
	Auth           *auth.Authenticator
	GP             *globalping.Client
	AllowedOrigins []string
	upgrader       websocket.Upgrader
}

func NewServer(st *store.Store, eng *monitor.Engine, hub *events.Hub, nf *notifier.Dispatcher, a *auth.Authenticator, allowedOrigins []string) *Server {
	s := &Server{
		Store:          st,
		Engine:         eng,
		Hub:            hub,
		Notifier:       nf,
		Auth:           a,
		GP:             globalping.New(),
		AllowedOrigins: allowedOrigins,
	}
	s.upgrader = websocket.Upgrader{
		CheckOrigin: s.originAllowed,
	}
	return s
}

// originAllowed permits the configured origin(s). "*" allows any origin (default,
// convenient for a self-hosted tool also accessed directly by IP/localhost).
func (s *Server) originAllowed(r *http.Request) bool {
	for _, o := range s.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, r.Header.Get("Origin")) {
			return true
		}
	}
	return false
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
	}))

	// public
	r.Get("/api/health", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "clients": s.Hub.ClientCount()})
	})
	r.Post("/api/login", s.handleLogin)
	r.Get("/api/status-page/{slug}", s.handlePublicStatusPage) // public status page data
	r.Get("/api/public/monitors", s.handlePublicMonitors)      // public overview of all monitors
	r.Get("/api/public/settings", s.handlePublicSettings)      // branding for the public overview page
	r.Get("/api/push/{token}", s.handlePush)                   // heartbeat ping
	r.Post("/api/push/{token}", s.handlePush)

	// websocket (token via query param, simple for browser clients)
	r.Get("/api/ws", s.handleWS)

	// protected: any authenticated role can read
	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.Get("/api/me", s.handleMe)
		r.Put("/api/me", s.handleUpdateProfile)
		r.Put("/api/me/password", s.handleChangePassword)
		r.Get("/api/settings", s.handleGetSystemSettings)
		r.Get("/api/settings/ai/status", s.handleAIStatus)
		r.Get("/api/ai/providers", s.handleAIProviders)
		r.Get("/api/monitors", s.handleListMonitors)
		r.Get("/api/monitors/{id}", s.handleGetMonitor)
		r.Get("/api/monitors/{id}/checks", s.handleRecentChecks)
		r.Get("/api/monitors/{id}/stats", s.handleMonitorStats)
		r.Get("/api/monitors/{id}/uptime", s.handleMonitorUptime)
		r.Get("/api/monitors/{id}/domain-ssl", s.handleMonitorDomainSSL)
		r.Get("/api/monitors/{id}/analyses", s.handleListAnalyses)
		r.Get("/api/monitors/{id}/domain-intel", s.handleMonitorDomainIntel)

		r.Get("/api/notifications", s.handleListNotifications)
		r.Get("/api/templates", s.handleListTemplates)
		r.Get("/api/escalations", s.handleListEscalations)
		r.Get("/api/status-pages", s.handleListStatusPages)
		r.Get("/api/incidents", s.handleListIncidents)
		r.Post("/api/multi-ping", s.handleMultiPing)

		// admin-only: everything that mutates state
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)

			r.Post("/api/monitors", s.handleCreateMonitor)
			r.Put("/api/monitors/{id}", s.handleUpdateMonitor)
			r.Delete("/api/monitors/{id}", s.handleDeleteMonitor)
			r.Post("/api/monitors/{id}/check", s.handleManualCheck)
			r.Post("/api/monitors/{id}/analyze", s.handleAnalyzeMonitor)
			r.Put("/api/monitors/{id}/enabled", s.handleSetMonitorEnabled)

			r.Post("/api/notifications", s.handleCreateNotification)
			r.Put("/api/notifications/{id}", s.handleUpdateNotification)
			r.Delete("/api/notifications/{id}", s.handleDeleteNotification)
			r.Post("/api/notifications/{id}/test", s.handleTestNotification)
			r.Post("/api/notifications/test-preview", s.handleTestNotificationPreview)

			r.Put("/api/templates", s.handleSaveTemplates)

			r.Post("/api/escalations", s.handleUpsertEscalation)
			r.Delete("/api/escalations/{id}", s.handleDeleteEscalation)

			r.Post("/api/status-pages", s.handleUpsertStatusPage)
			r.Delete("/api/status-pages/{id}", s.handleDeleteStatusPage)

			r.Post("/api/incidents", s.handleCreateIncident)
			r.Put("/api/incidents/{id}", s.handleUpdateIncident)

			r.Get("/api/users", s.handleListUsers)
			r.Post("/api/users", s.handleCreateUser)
			r.Delete("/api/users/{id}", s.handleDeleteUser)
			r.Put("/api/users/{id}/role", s.handleUpdateUserRole)
			r.Put("/api/users/{id}/password", s.handleUpdateUserPassword)

			r.Put("/api/settings", s.handleSetSystemSettings)
			r.Post("/api/settings/apply-defaults", s.handleApplyDefaults)
			r.Get("/api/settings/ai", s.handleGetAISettings)
			r.Put("/api/settings/ai", s.handleSaveAISettings)
			r.Post("/api/settings/ai/test", s.handleTestAISettings)
			r.Get("/api/settings/report", s.handleGetReportSettings)
			r.Put("/api/settings/report", s.handleSaveReportSettings)
			r.Post("/api/settings/report/send-now", s.handleSendReportNow)
			r.Get("/api/maintenance/stats", s.handleMaintenanceStats)
			r.Post("/api/maintenance/purge-history", s.handlePurgeHistory)
		})
	})

	return r
}

// ---------- middleware ----------

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token := strings.TrimPrefix(h, "Bearer ")
		if token == "" || token == h && !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := s.Auth.Verify(token)
		if err != nil {
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxKey string

const ctxClaimsKey ctxKey = "claims"

// requireAdmin must run after requireAuth; it rejects non-admin callers.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := currentClaims(r)
		if claims == nil || claims.Role != string(models.RoleAdmin) {
			http.Error(w, "forbidden: admin role required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentClaims(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(ctxClaimsKey).(*auth.Claims)
	return c
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := currentClaims(r)
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	displayName, email, err := s.Store.GetUserProfile(r.Context(), claims.Sub)
	if err != nil {
		// missing row must not break the identity lookup
		displayName, email = "", ""
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username":    claims.Username,
		"role":        claims.Role,
		"displayName": displayName,
		"email":       email,
	})
}

// handleUpdateProfile lets any logged-in user edit their own display name and
// email. Username/role stay admin-managed and the password has its own
// dedicated endpoint.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	claims := currentClaims(r)
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && (!strings.Contains(req.Email, "@") || !strings.Contains(req.Email, ".") || len(req.Email) > 254) {
		http.Error(w, "invalid email address", http.StatusBadRequest)
		return
	}
	if err := s.Store.UpdateUserProfile(r.Context(), claims.Sub, req.DisplayName, req.Email); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"displayName": req.DisplayName, "email": req.Email})
}

// handleChangePassword lets any logged-in user change their own password.
// The current password must be supplied and verified first.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := currentClaims(r)
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.NewPassword == "" {
		http.Error(w, "new password is required", http.StatusBadRequest)
		return
	}
	if len(req.NewPassword) < 8 {
		http.Error(w, "new password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	hash, _, err := s.Store.GetUserCredentials(r.Context(), claims.Sub)
	if err != nil {
		httpError(w, err)
		return
	}
	if !auth.CheckPassword(hash, req.CurrentPassword) {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	nowHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), claims.Sub, nowHash); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- system settings ----------

func (s *Server) handleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.GetSystemSettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) handleSetSystemSettings(w http.ResponseWriter, r *http.Request) {
	var req models.SystemSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	defaults := store.DefaultSystemSettings()
	// Clamp numeric fields to sane ranges; zero/invalid falls back to default.
	switch {
	case req.DefaultCheckIntervalSec >= 10 && req.DefaultCheckIntervalSec <= 3600:
	case req.DefaultCheckIntervalSec == 0:
		req.DefaultCheckIntervalSec = defaults.DefaultCheckIntervalSec
	default:
		http.Error(w, "default check interval must be between 10 and 3600 seconds", http.StatusBadRequest)
		return
	}
	switch {
	case req.DefaultTimeoutSec >= 1 && req.DefaultTimeoutSec <= 120:
	case req.DefaultTimeoutSec == 0:
		req.DefaultTimeoutSec = defaults.DefaultTimeoutSec
	default:
		http.Error(w, "default timeout must be between 1 and 120 seconds", http.StatusBadRequest)
		return
	}
	if req.CheckRetentionDays < 0 || (req.CheckRetentionDays > 0 && req.CheckRetentionDays < 7) {
		http.Error(w, "retention must be 0 (keep forever) or at least 7 days", http.StatusBadRequest)
		return
	}
	if req.DefaultSloTargetPct < 0 || req.DefaultSloTargetPct > 100 {
		http.Error(w, "default SLO target must be between 0 and 100 percent", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.PublicTitle) == "" {
		req.PublicTitle = defaults.PublicTitle
	}
	if len(req.PublicTitle) > 120 {
		http.Error(w, "public title too long (max 120 characters)", http.StatusBadRequest)
		return
	}
	if len(req.PublicFooter) > 200 {
		http.Error(w, "public footer too long (max 200 characters)", http.StatusBadRequest)
		return
	}
	if err := s.Store.SaveSystemSettings(r.Context(), req); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleApplyDefaults bulk-overwrites the interval, timeout and SLO target of
// every monitor with the current global defaults (Settings → Umum), then hot
// swaps each runner so the new schedule takes effect without a restart.
func (s *Server) handleApplyDefaults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sys, err := s.Store.GetSystemSettings(ctx)
	if err != nil {
		httpError(w, err)
		return
	}
	updated, err := s.Store.ApplyDefaultsToAllMonitors(ctx, sys.DefaultCheckIntervalSec, sys.DefaultTimeoutSec, sys.DefaultSloTargetPct)
	if err != nil {
		httpError(w, err)
		return
	}
	// Reload every runner so the new interval/timeout is applied immediately.
	monitors, err := s.Store.ListMonitors(ctx)
	if err != nil {
		httpError(w, err)
		return
	}
	for i := range monitors {
		m := &monitors[i]
		s.Engine.Remove(m.ID)
		if m.Enabled {
			s.Engine.Run(m)
		}
	}
	s.broadcastMonitorList(ctx)
	writeJSON(w, http.StatusOK, map[string]int64{"updated": updated})
}

// maskKey returns a display-safe preview of an API key (last 4 chars only) so
// the settings UI can show something without ever exposing the secret.
func maskKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

// handleGetAISettings returns the AI provider config with the API key masked.
func (s *Server) handleGetAISettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.GetAISettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    set.Enabled,
		"provider":   set.Provider,
		"baseUrl":    set.BaseURL,
		"model":      set.Model,
		"hasKey":     strings.TrimSpace(set.APIKey) != "",
		"keyPreview": maskKey(set.APIKey),
	})
}

// handleSaveAISettings persists the AI provider config. An empty incoming API
// key means "keep the stored one" (the UI never receives the plaintext key).
func (s *Server) handleSaveAISettings(w http.ResponseWriter, r *http.Request) {
	var req models.AISettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	existing, err := s.Store.GetAISettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		req.APIKey = existing.APIKey
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Model = strings.TrimSpace(req.Model)
	if err := s.Store.SaveAISettings(r.Context(), req); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved":      true,
		"hasKey":     strings.TrimSpace(req.APIKey) != "",
		"keyPreview": maskKey(req.APIKey),
	})
}

// handleTestAISettings sends a tiny completion using the submitted (or stored)
// config so the admin can verify the provider/key before saving.
func (s *Server) handleTestAISettings(w http.ResponseWriter, r *http.Request) {
	var req models.AISettings
	_ = json.NewDecoder(r.Body).Decode(&req)
	existing, _ := s.Store.GetAISettings(r.Context())
	if strings.TrimSpace(req.APIKey) == "" {
		req.APIKey = existing.APIKey
	}
	base, model := ai.ResolveEndpoint(req.Provider, req.BaseURL, req.Model)
	if strings.TrimSpace(req.APIKey) == "" || base == "" || model == "" {
		http.Error(w, "provider, model and API key are all required to test", http.StatusBadRequest)
		return
	}
	client := ai.NewClient(ai.Config{APIKey: req.APIKey, BaseURL: base, Model: model})
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	reply, err := client.Chat(ctx, "You are a connectivity check.", "Reply with the single word: OK")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": model, "reply": reply})
}

// handleAIStatus exposes just enough (no key) for the monitor-detail badge and
// the "not configured" hint to reflect the admin's choice. Any role can read.
func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.GetAISettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	enabled := set.Enabled && strings.TrimSpace(set.APIKey) != ""
	_, model := ai.ResolveEndpoint(set.Provider, set.BaseURL, set.Model)
	name := ""
	if p := ai.ProviderByID(set.Provider); p != nil {
		name = p.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      enabled,
		"provider":     set.Provider,
		"providerName": name,
		"model":        model,
	})
}

// handleAIProviders lists the built-in provider presets for the dropdown.
func (s *Server) handleAIProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ai.Providers)
}

// ---------- Report settings ----------

// normalizeReportSettings applies defaults and validates the submitted report
// config, returning a human-readable error when something is out of range.
func normalizeReportSettings(set *models.ReportSettings) error {
	switch set.Frequency {
	case "", "daily", "weekly", "monthly":
	default:
		return fmt.Errorf("invalid frequency %q", set.Frequency)
	}
	if set.Frequency == "" {
		set.Frequency = "weekly"
	}
	if _, err := time.Parse("15:04", set.SendTime); err != nil {
		set.SendTime = "09:00"
	}
	if set.Frequency == "weekly" {
		if _, err := parseWeekdayName(set.WeeklyDay); err != nil {
			return fmt.Errorf("invalid weeklyDay %q", set.WeeklyDay)
		}
	}
	if set.Frequency == "monthly" {
		if set.MonthlyDay < 1 || set.MonthlyDay > 28 {
			return fmt.Errorf("monthlyDay must be 1-28")
		}
	}
	switch set.FileFormat {
	case "":
		set.FileFormat = "xlsx"
	case "xlsx", "csv":
	default:
		return fmt.Errorf("invalid fileFormat %q", set.FileFormat)
	}
	// Normalise the report's own delivery channels: drop empty entries and
	// assign an id when the client created one without one.
	if set.Channels != nil {
		clean := make([]models.ReportChannel, 0, len(set.Channels))
		for i, c := range set.Channels {
			if strings.TrimSpace(c.Type) == "" {
				continue
			}
			if c.ID == "" {
				c.ID = fmt.Sprintf("rc-%d", time.Now().UnixNano()+int64(i))
			}
			if len(c.Config) == 0 {
				c.Config = json.RawMessage("{}")
			}
			clean = append(clean, c)
		}
		set.Channels = clean
	}
	return nil
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func parseWeekdayName(s string) (time.Weekday, error) {
	for i, n := range weekdayNames {
		if strings.EqualFold(s, n) {
			return time.Weekday(i), nil
		}
	}
	return 0, fmt.Errorf("unknown weekday")
}

// handleGetReportSettings returns the periodic report configuration.
func (s *Server) handleGetReportSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.GetReportSettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	if set.NotificationIDs == nil {
		set.NotificationIDs = []string{}
	}
	if set.Channels == nil {
		set.Channels = []models.ReportChannel{}
	}
	// A never-configured report defaults to delivering the Excel file so the
	// out-of-the-box behaviour matches the requested file delivery.
	if set.Frequency == "" {
		set.SendFile = true
		set.FileFormat = "xlsx"
	}
	// Never expose the Slack bot token in plaintext (mirrors the AI key).
	set.SlackBotToken = maskKey(set.SlackBotToken)
	writeJSON(w, http.StatusOK, set)
}

// handleSaveReportSettings persists the periodic report configuration. The
// scheduler-owned LastSentAt is preserved across edits.
func (s *Server) handleSaveReportSettings(w http.ResponseWriter, r *http.Request) {
	var req models.ReportSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := normalizeReportSettings(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing, err := s.Store.GetReportSettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	req.LastSentAt = existing.LastSentAt
	// The UI only ever sees a masked token; an empty or unchanged-mask value
	// means "keep the stored one" (same convention as the AI settings).
	if strings.TrimSpace(req.SlackBotToken) == "" || strings.Contains(req.SlackBotToken, "••••") {
		req.SlackBotToken = existing.SlackBotToken
	}
	if err := s.Store.SaveReportSettings(r.Context(), req); err != nil {
		httpError(w, err)
		return
	}
	req.SlackBotToken = maskKey(req.SlackBotToken)
	writeJSON(w, http.StatusOK, req)
}

// handleSendReportNow builds a report for the last 24h and dispatches it to
// the configured channels immediately ("send test now" button).
func (s *Server) handleSendReportNow(w http.ResponseWriter, r *http.Request) {
	if s.Reports == nil {
		http.Error(w, "report scheduler unavailable", http.StatusInternalServerError)
		return
	}
	res, err := s.Reports.SendNow(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "subject": res.Subject, "body": res.Body})
}

// handlePublicSettings exposes only the branding fields to unauthenticated
// visitors of the public overview page.
func (s *Server) handlePublicSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.GetSystemSettings(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"title":  set.PublicTitle,
		"footer": set.PublicFooter,
	})
}

// handleMaintenanceStats returns raw row counts for the Settings → Data tab.
func (s *Server) handleMaintenanceStats(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountChecks(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// handlePurgeHistory deletes check rows older than the given window. It is
// rate-limit-free but admin-only and refuses to run with a non-positive
// window (that means "keep forever").
func (s *Server) handlePurgeHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OlderThanDays int `json:"olderThanDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OlderThanDays <= 0 {
		http.Error(w, "olderThanDays must be a positive number of days", http.StatusBadRequest)
		return
	}
	deleted, err := s.Store.DeleteOldChecks(r.Context(), req.OlderThanDays)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, hash, role, err := s.Store.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !auth.CheckPassword(hash, req.Password) {
		// do not reveal whether the username exists
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	token, err := s.Auth.Sign(auth.Claims{Sub: userID, Username: req.Username, Role: string(role)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "username": req.Username, "role": string(role)})
}

// ---------- monitors ----------

func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMonitor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var m models.Monitor
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if m.IntervalSec <= 0 || m.TimeoutSec <= 0 || m.SloTargetPct <= 0 {
		// Fill unset fields from the server-wide defaults (Settings → Umum).
		sys, err := s.Store.GetSystemSettings(r.Context())
		if err == nil {
			if m.IntervalSec <= 0 {
				m.IntervalSec = sys.DefaultCheckIntervalSec
			}
			if m.TimeoutSec <= 0 {
				m.TimeoutSec = sys.DefaultTimeoutSec
			}
			if m.SloTargetPct <= 0 {
				m.SloTargetPct = sys.DefaultSloTargetPct
			}
		} else {
			if m.IntervalSec <= 0 {
				m.IntervalSec = 60
			}
			if m.TimeoutSec <= 0 {
				m.TimeoutSec = 10
			}
		}
	}
	if m.Type == models.TypePush && m.PushToken == "" {
		m.PushToken = auth.GeneratePushToken()
	}
	// Record who added this monitor for auditing (best-effort; ignore if claims
	// are somehow missing).
	if claims := currentClaims(r); claims != nil {
		m.CreatedBy = claims.Username
	}
	if err := s.Store.CreateMonitor(r.Context(), &m); err != nil {
		httpError(w, err)
		return
	}
	if m.Enabled {
		s.Engine.Run(&m)
	}
	s.broadcastMonitorList(r.Context())
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		httpError(w, err)
		return
	}
	var m models.Monitor
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.ID = existing.ID
	m.CreatedAt = existing.CreatedAt
	if m.Type == models.TypePush && m.PushToken == "" {
		m.PushToken = existing.PushToken
	}
	if err := s.Store.UpdateMonitor(r.Context(), &m); err != nil {
		httpError(w, err)
		return
	}
	// hot-swap the runner with the new config, no full process restart needed
	s.Engine.Remove(id)
	if m.Enabled {
		s.Engine.Run(&m)
	}
	s.broadcastMonitorList(r.Context())
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.Engine.Remove(id)
	if err := s.Store.DeleteMonitor(r.Context(), id); err != nil {
		httpError(w, err)
		return
	}
	s.broadcastMonitorList(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleManualCheck(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMonitor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, err)
		return
	}
	s.Engine.ManualCheck(m)
	writeJSON(w, http.StatusOK, map[string]string{"status": "check scheduled"})
}

// handleSetMonitorEnabled pauses (enabled=false) or resumes (enabled=true) a
// monitor without a full config rewrite. The engine runner is started/stopped
// to match, then the change is broadcast so the UI updates live.
func (s *Server) handleSetMonitorEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		httpError(w, err)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		http.Error(w, "missing 'enabled' field", http.StatusBadRequest)
		return
	}
	enabled := *req.Enabled
	if err := s.Store.SetMonitorEnabled(r.Context(), id, enabled); err != nil {
		httpError(w, err)
		return
	}
	m.Enabled = enabled
	if enabled {
		s.Engine.Run(m)
	} else {
		s.Engine.Remove(id)
	}
	s.broadcastMonitorList(r.Context())
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleRecentChecks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	checks, err := s.Store.RecentChecks(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checks)
}

// handleMonitorUptime returns a single monitor's daily-aggregated uptime
// history + overall availability for the requested window (default 365d). Any
// authenticated role can read it — it powers the per-monitor detail page.
func (s *Server) handleMonitorUptime(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		httpError(w, err)
		return
	}
	days := 365
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	hist, overall, err := s.Store.UptimeDaily(r.Context(), id, days)
	if err != nil {
		httpError(w, err)
		return
	}
	st := models.MonitorUptimeStat{
		MonitorID:    id,
		UptimePct:    overall,
		SloTargetPct: m.SloTargetPct,
		WindowDays:   days,
		History:      hist,
	}
	if st.SloTargetPct > 0 {
		st.SloMet = overall >= st.SloTargetPct
	} else {
		st.SloMet = true
	}
	st.EnsureHistory()
	writeJSON(w, http.StatusOK, st)
}

// handleMonitorDomainSSL reports the TLS-certificate and domain-registration
// expiry for a web-facing monitor. SSL is read from a live handshake; domain
// expiry comes from WHOIS. Both degrade to null-ish values on failure.
func (s *Server) handleMonitorDomainSSL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		httpError(w, err)
		return
	}

	host, port := domainSSLHost(m)
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	resp := map[string]interface{}{
		"host": host,
	}
	if host == "" {
		resp["ssl"] = map[string]interface{}{"ok": false, "error": "not a web-facing monitor"}
		resp["domain"] = map[string]interface{}{"ok": false, "error": "not a web-facing monitor"}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	ssl := domainssl.CheckSSL(r.Context(), host, port, timeout)
	resp["ssl"] = ssl

	// Public IP the host currently resolves to (display-only). Bare-IP hosts
	// report themselves; domains go through sysdns (resolv.conf-aware + DoH
	// fallback), preferring an IPv4 answer.
	publicIP := ""
	if ip := net.ParseIP(host); ip != nil {
		publicIP = host
	} else if addrs, derr := sysdns.LookupHost(r.Context(), host); derr == nil {
		for _, a := range addrs {
			if net.ParseIP(a).To4() != nil {
				publicIP = a
				break
			}
		}
		if publicIP == "" && len(addrs) > 0 {
			publicIP = addrs[0]
		}
	}
	resp["publicIp"] = publicIP

	// WHOIS only makes sense for a real domain (skip bare IPs).
	if ip := net.ParseIP(host); ip == nil {
		domain := domainssl.RegistrableDomain(host)
		resp["domain"] = domainssl.CheckDomainExpiry(r.Context(), domain, timeout)
		resp["domainName"] = domain
	} else {
		resp["domain"] = map[string]interface{}{"ok": false, "error": "host is an IP address"}
	}

	writeJSON(w, http.StatusOK, resp)
}

// domainSSLHost picks the TLS host/port to probe from a monitor's config.
func domainSSLHost(m *models.Monitor) (host, port string) {
	switch m.Type {
	case models.TypeHTTP:
		if m.URL == "" {
			return "", ""
		}
		u, err := url.Parse(m.URL)
		if err != nil || u.Hostname() == "" {
			return "", ""
		}
		p := u.Port()
		if p == "" {
			if u.Scheme == "http" {
				// plain http has no TLS — still probe 443 for the cert
				p = "443"
			} else {
				p = "443"
			}
		}
		return u.Hostname(), p
	case models.TypeCert:
		h, p, err := net.SplitHostPort(m.Host)
		if err != nil {
			return m.Host, "443"
		}
		return h, p
	case models.TypeDNS:
		return m.Host, "443"
	default:
		if m.Host != "" {
			h, p, err := net.SplitHostPort(m.Host)
			if err != nil {
				return m.Host, "443"
			}
			return h, p
		}
		return "", ""
	}
}

// handleListAnalyses returns the stored AI root-cause analyses for a monitor
// (newest first). Any authenticated role can read them.
func (s *Server) handleListAnalyses(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.Store.ListAnalyses(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAnalyzeMonitor runs an on-demand LLM analysis for a monitor and
// returns the persisted result. Admin-only; blocks until the model responds
// (tens of seconds is normal), so the client should use a generous timeout.
func (s *Server) handleAnalyzeMonitor(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMonitor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, err)
		return
	}
	a, err := s.Engine.AnalyzeSync(r.Context(), m)
	if err != nil {
		// Not configured → 404-style guidance; upstream failure → 502.
		if strings.Contains(err.Error(), "not configured") {
			http.Error(w, "AI analysis is not configured \u2014 open Settings \u2192 AI to choose a provider and paste an API key", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleMonitorDomainIntel returns a full outside-in domain audit: DNS records,
// email authentication (SPF/DMARC/DKIM), security headers and detailed SSL.
// Each probe runs independently and degrades to an error string on failure.
func (s *Server) handleMonitorDomainIntel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		httpError(w, err)
		return
	}

	host, port := domainSSLHost(m)
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	resp := map[string]interface{}{"host": host}
	if host == "" {
		resp["error"] = "not a domain-facing monitor"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	domain := host
	if ip := net.ParseIP(host); ip == nil {
		domain = domainssl.RegistrableDomain(host)
	}
	resp["domain"] = domain

	// Detailed SSL handshake info (protocol, SANs, key algo).
	resp["ssl"] = domainssl.CheckSSLDetailed(r.Context(), host, port, timeout)
	// DNS + email only make sense for real hostnames, not bare IPs.
	if net.ParseIP(host) == nil {
		resp["dns"] = domainssl.CheckDNS(r.Context(), host, domain)
		resp["email"] = domainssl.CheckEmailSecurity(r.Context(), domain)
	}
	// Security headers need an HTTP(S) target.
	target := m.URL
	if target == "" {
		target = "https://" + net.JoinHostPort(host, port)
	}
	resp["headers"] = domainssl.CheckSecurityHeaders(r.Context(), target, timeout)

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMonitorStats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if parsed, err := strconv.Atoi(h); err == nil {
			hours = parsed
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	stats, err := s.Store.MonitorStats(r.Context(), id, since)
	if err != nil {
		httpError(w, err)
		return
	}
	checks, err := s.Store.ChecksInWindow(r.Context(), id, since)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"stats": stats, "checks": checks})
}

// ---------- notifications ----------

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListNotifications(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var n models.Notification
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.CreateNotification(r.Context(), &n); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNotification(w http.ResponseWriter, r *http.Request) {
	n := models.Notification{ID: chi.URLParam(r, "id")}
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n.ID = chi.URLParam(r, "id")
	if err := s.Store.UpdateNotification(r.Context(), &n); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNotification(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteNotification(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.GetNotification(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, err)
		return
	}
	if err := s.Notifier.TestSend(r.Context(), n); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// handleTestNotificationPreview sends a test alert through an unsaved
// notification definition so the user can verify credentials before saving.
// The payload is the same shape as a stored notification; nothing is
// persisted here.
func (s *Server) handleTestNotificationPreview(w http.ResponseWriter, r *http.Request) {
	var n models.Notification
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(n.Type) == "" {
		http.Error(w, "notification type is required", http.StatusBadRequest)
		return
	}
	if err := s.Notifier.TestSend(r.Context(), &n); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// ---------- alert templates ----------

// handleListTemplates returns the reusable notification message templates
// (email + chat), seeding built-in defaults when none have been saved yet.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.GetAlertTemplates(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleSaveTemplates replaces the whole template library. IDs are assigned and
// exactly one default per channel is enforced inside the store.
func (s *Server) handleSaveTemplates(w http.ResponseWriter, r *http.Request) {
	var list []models.AlertTemplate
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.SaveAlertTemplates(r.Context(), list); err != nil {
		httpError(w, err)
		return
	}
	// Re-read so the client sees normalised IDs / default flags.
	saved, err := s.Store.GetAlertTemplates(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// ---------- escalations ----------

func (s *Server) handleListEscalations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEscalations(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpsertEscalation(w http.ResponseWriter, r *http.Request) {
	var p models.EscalationPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.UpsertEscalation(r.Context(), &p); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteEscalation(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteEscalation(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- status pages ----------

func (s *Server) handleListStatusPages(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListStatusPages(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpsertStatusPage(w http.ResponseWriter, r *http.Request) {
	var p models.StatusPage
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.UpsertStatusPage(r.Context(), &p); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteStatusPage(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePublicStatusPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	page, err := s.Store.GetStatusPageBySlug(r.Context(), slug)
	if err != nil {
		httpError(w, err)
		return
	}
	monitors := make([]models.Monitor, 0, len(page.Monitors))
	all, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	byID := map[string]models.Monitor{}
	for _, m := range all {
		byID[m.ID] = m
	}
	for _, pm := range page.Monitors {
		if m, ok := byID[pm.MonitorID]; ok {
			monitors = append(monitors, m)
		}
	}
	incidents, err := s.Store.ListActiveIncidents(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}

	// Build per-monitor SLO/uptime stats over the requested window (default 90d).
	days := 90
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	stats := make([]models.MonitorUptimeStat, 0, len(monitors))
	var sum, sumN float64
	for i := range monitors {
		hist, overall, err := s.Store.UptimeDaily(r.Context(), monitors[i].ID, days)
		if err != nil {
			continue
		}
		st := models.MonitorUptimeStat{
			MonitorID:    monitors[i].ID,
			UptimePct:    overall,
			SloTargetPct: monitors[i].SloTargetPct,
			WindowDays:   days,
			History:      hist,
		}
		if st.SloTargetPct > 0 {
			st.SloMet = overall >= st.SloTargetPct
		} else {
			st.SloMet = true
		}
		st.EnsureHistory()
		stats = append(stats, st)
		sum += overall
		sumN++
	}
	pageUptime := 100.0
	if sumN > 0 {
		pageUptime = sum / sumN
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"page":      page,
		"monitors":  monitors,
		"incidents": incidents,
		"stats":     stats,
		"uptime":    pageUptime,
		"days":      days,
	})
}

// handlePublicMonitors returns a sanitized, unauthenticated overview of every
// monitored service: name, type, current status, uptime history and SLO state.
// Only web-facing checks expose their URL; internal hosts/ports and all config
// secrets are omitted. Management endpoints stay behind requireAuth/Admin.
func (s *Server) handlePublicMonitors(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	all, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	monitors := make([]models.PublicMonitor, 0, len(all))
	stats := make([]models.MonitorUptimeStat, 0, len(all))
	var sum, sumN float64
	for i := range all {
		m := all[i]
		pm := models.PublicMonitor{
			ID:            m.ID,
			Name:          m.Name,
			Type:          m.Type,
			Status:        m.Status,
			LastCheckAt:   m.LastCheckAt,
			LastLatencyMs: m.LastLatencyMs,
			StatusReason:  m.StatusReason,
			Enabled:       m.Enabled,
			SloTargetPct:  m.SloTargetPct,
		}
		// NOTE: the monitored URL/domain is intentionally NOT exposed on the
		// public (unauthenticated) overview — it is only visible after login.
		monitors = append(monitors, pm)

		hist, overall, err := s.Store.UptimeDaily(r.Context(), m.ID, days)
		if err != nil {
			continue
		}
		st := models.MonitorUptimeStat{
			MonitorID:    m.ID,
			UptimePct:    overall,
			SloTargetPct: m.SloTargetPct,
			WindowDays:   days,
			History:      hist,
		}
		if st.SloTargetPct > 0 {
			st.SloMet = overall >= st.SloTargetPct
		} else {
			st.SloMet = true
		}
		st.EnsureHistory()
		stats = append(stats, st)
		sum += overall
		sumN++
	}
	overallUptime := 100.0
	if sumN > 0 {
		overallUptime = sum / sumN
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"monitors": monitors,
		"stats":    stats,
		"uptime":   overallUptime,
		"days":     days,
	})
}

// ---------- incidents ----------

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	// ?monitorId= returns that monitor's recent incidents including resolved
	// ones (detail-page timeline); without it, only open incidents.
	if mid := r.URL.Query().Get("monitorId"); mid != "" {
		list, err := s.Store.ListIncidentsForMonitor(r.Context(), mid, 20)
		if err != nil {
			httpError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
		return
	}
	list, err := s.Store.ListActiveIncidents(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var i models.Incident
	if err := json.NewDecoder(r.Body).Decode(&i); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.CreateIncident(r.Context(), &i); err != nil {
		httpError(w, err)
		return
	}
	s.Hub.BroadcastIncident(events.IncidentUpdate{
		Type: "incident.update", IncidentID: i.ID, MonitorID: i.MonitorID, Status: i.Status, Headline: i.Headline,
	})
	writeJSON(w, http.StatusCreated, i)
}

func (s *Server) handleUpdateIncident(w http.ResponseWriter, r *http.Request) {
	i := models.Incident{ID: chi.URLParam(r, "id")}
	if err := json.NewDecoder(r.Body).Decode(&i); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	i.ID = chi.URLParam(r, "id")
	if err := s.Store.UpdateIncident(r.Context(), &i); err != nil {
		httpError(w, err)
		return
	}
	s.Hub.BroadcastIncident(events.IncidentUpdate{
		Type: "incident.update", IncidentID: i.ID, MonitorID: i.MonitorID, Status: i.Status, Headline: i.Headline,
	})
	writeJSON(w, http.StatusOK, i)
}

// ---------- multi-country ping (Globalping) ----------

// pingTarget extracts the hostname a monitor probes, for external ping tests.
// Push monitors have no network target of their own.
func pingTarget(m *models.Monitor) string {
	switch m.Type {
	case models.TypePush:
		return ""
	case models.TypeHTTP:
		u, err := url.Parse(m.URL)
		if err != nil {
			return ""
		}
		return u.Hostname()
	case models.TypeCert:
		// cert stores "host:port" — ping needs the bare host
		if h, _, err := net.SplitHostPort(m.Host); err == nil {
			return h
		}
		return m.Host
	default:
		return m.Host
	}
}

// handleMultiPing runs a real ping test against the monitor's host from one
// Globalping probe per available Asian country, sorted fastest first.
func (s *Server) handleMultiPing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MonitorID string `json:"monitorId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MonitorID == "" {
		http.Error(w, "monitorId required", http.StatusBadRequest)
		return
	}
	m, err := s.Store.GetMonitor(r.Context(), req.MonitorID)
	if err != nil {
		httpError(w, err)
		return
	}
	target := pingTarget(m)
	if target == "" {
		http.Error(w, "this monitor has no pingable host target", http.StatusBadRequest)
		return
	}

	// The measurement itself takes ~5-15s; allow headroom for slow upstream
	// round-trips (locations lookup, poll interval) before giving up.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	results, err := s.GP.MultiPing(ctx, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	sort.Slice(results, func(i, j int) bool { return results[i].AvgMs < results[j].AvgMs })
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"target":  target,
		"results": results,
	})
}

// ---------- push / heartbeat ----------

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := s.Store.RecordPush(r.Context(), token); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- websocket ----------

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if _, err := s.Auth.Verify(token); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}
	defer conn.Close()

	send := s.Hub.Register()
	defer s.Hub.Unregister(send)

	// send initial snapshot so the UI doesn't need a separate polling call
	ctx := context.Background()
	monitors, err := s.Store.ListMonitors(ctx)
	if err == nil {
		snapshot, _ := json.Marshal(map[string]interface{}{"type": "snapshot", "monitors": monitors})
		conn.WriteMessage(websocket.TextMessage, snapshot)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// pong to keep connection alive through proxies
	pinger := time.NewTicker(45 * time.Second)
	defer pinger.Stop()

	for {
		select {
		case <-done:
			return
		case <-pinger.C:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case msg := <-send:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

func (s *Server) broadcastMonitorList(ctx context.Context) {
	monitors, err := s.Store.ListMonitors(ctx)
	if err != nil {
		return
	}
	s.Hub.Broadcast(events.MonitorUpdate{Type: "monitors.refresh"})
	_ = monitors
}

// ---------- users (admin only) ----------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListUsers(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	role := models.UserRole(req.Role)
	if role != models.RoleAdmin && role != models.RoleViewer {
		role = models.RoleViewer
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.CreateUser(r.Context(), req.Username, hash, role); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		httpError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username, "role": string(role)})
}

func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	role := models.UserRole(req.Role)
	if role != models.RoleAdmin && role != models.RoleViewer {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	// prevent demoting the last admin
	claims := currentClaims(r)
	if role != models.RoleAdmin && claims != nil && claims.Sub == id {
		n, _ := s.Store.CountAdmins(r.Context())
		if n <= 1 {
			http.Error(w, "cannot demote the last admin", http.StatusBadRequest)
			return
		}
	}
	if err := s.Store.SetUserRole(r.Context(), id, role); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateUserPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), id, hash); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := currentClaims(r)
	if claims != nil && claims.Sub == id {
		http.Error(w, "cannot delete your own account", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

func httpError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	}
	msg := fmt.Sprintf("%v", err)
	if status >= 500 {
		log.Printf("[api] error: %v", err)
	}
	http.Error(w, msg, status)
}
