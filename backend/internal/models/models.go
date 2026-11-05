package models

import (
	"encoding/json"
	"time"
)

// MonitorType enumerates supported check types.
type MonitorType string

const (
	TypeHTTP   MonitorType = "http"
	TypeTCP    MonitorType = "tcp"
	TypeDNS    MonitorType = "dns"
	TypeCert   MonitorType = "cert"
	TypePush   MonitorType = "push"
	TypeDocker MonitorType = "docker"
	TypePing   MonitorType = "ping"
	TypeRedis  MonitorType = "redis"
	TypeDB     MonitorType = "db"
)

// MonitorStatus is the aggregate state used for realtime dashboards.
type MonitorStatus string

const (
	StatusUp       MonitorStatus = "up"
	StatusDown     MonitorStatus = "down"
	StatusPending  MonitorStatus = "pending"
	StatusDegraded MonitorStatus = "degraded"
	StatusUnknown  MonitorStatus = "unknown"
)

// UserRole controls write access in the API. Admin can manage everything,
// viewer is read-only.
type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleViewer UserRole = "viewer"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	// DisplayName and Email are self-service profile fields (editable in
	// Settings → Profil); both may be empty.
	DisplayName string    `json:"displayName,omitempty"`
	Email       string    `json:"email,omitempty"`
	Role        UserRole  `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Monitor struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           MonitorType `json:"type"`
	URL            string      `json:"url,omitempty"`    // http/cert
	Host           string      `json:"host,omitempty"`   // tcp/dns/docker/ping/redis/db
	Port           int         `json:"port,omitempty"`   // tcp/redis
	Method         string      `json:"method,omitempty"` // http
	ExpectedStatus int         `json:"expectedStatus,omitempty"`
	Keyword        string      `json:"keyword,omitempty"`    // http body keyword check
	DNSServer      string      `json:"dnsServer,omitempty"`  // dns
	RecordType     string      `json:"recordType,omitempty"` // dns: A, AAAA, CNAME, MX...
	CertWarnDays   int         `json:"certWarnDays,omitempty"`
	ContainerName  string      `json:"containerName,omitempty"` // docker
	IntervalSec    int         `json:"intervalSec"`
	TimeoutSec     int         `json:"timeoutSec"`
	PushToken      string      `json:"pushToken,omitempty"` // heartbeat/push monitor secret token
	PushGraceSec   int         `json:"pushGraceSec,omitempty"`
	Enabled        bool        `json:"enabled"`
	// CreatedBy records the username that added this monitor, for auditing.
	// Empty for rows created before the field existed.
	CreatedBy string `json:"createdBy,omitempty"`
	// LocalIp is an optional free-text note for sysadmins recording the
	// internal/LAN IP associated with this monitor. It is display-only and is
	// never probed/pinged.
	LocalIp   string    `json:"localIp,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// HTTP advanced options
	Headers       map[string]string `json:"headers,omitempty"`
	Body          string            `json:"body,omitempty"`
	BasicAuthUser string            `json:"basicAuthUser,omitempty"`
	BasicAuthPass string            `json:"basicAuthPass,omitempty"`

	// Ops metadata
	Tags          []string `json:"tags,omitempty"`
	LatencyWarnMs int      `json:"latencyWarnMs,omitempty"` // mark degraded when latency exceeds this

	// TLS handling for HTTPS targets. When true, the HTTP checker skips
	// certificate verification (useful for sites with incomplete cert chains).
	// Defaults to false so verification stays strict.
	IgnoreTLS bool `json:"ignoreTls,omitempty"`

	// SloTargetPct is the uptime SLO/SLA target for this monitor as a percentage
	// (e.g. 99.9). 0 means no target is set. Compared against real availability
	// in the UI and on public status pages to flag compliance.
	SloTargetPct float64 `json:"sloTargetPct,omitempty"`

	// Redis
	RedisPassword string `json:"redisPassword,omitempty"`
	RedisKey      string `json:"redisKey,omitempty"` // optional EXISTS check

	// Database (postgres/mysql)
	DBDriver     string `json:"dbDriver,omitempty"` // postgres | mysql
	DSN          string `json:"dsn,omitempty"`      // connection string
	ExecQuery    string `json:"query,omitempty"`    // query to run
	ExpectedRows int    `json:"expectedRows,omitempty"`

	// runtime state (not persisted in monitors table, joined from latest check)
	Status        MonitorStatus `json:"status"`
	LastCheckAt   *time.Time    `json:"lastCheckAt,omitempty"`
	LastLatencyMs *float64      `json:"lastLatencyMs,omitempty"`
	StatusReason  string        `json:"statusReason,omitempty"`
	// StateSince is when the current status began (last up/down/degraded flip).
	// The dashboard orders monitors by this so freshly changed services float
	// to the top regardless of creation order.
	StateSince      *time.Time `json:"stateSince,omitempty"`
	NotificationIDs []string   `json:"notificationIds,omitempty"`
	EscalationID    *string    `json:"escalationId,omitempty"`
}

type CheckResult struct {
	ID         string          `json:"id"`
	MonitorID  string          `json:"monitorId"`
	Status     MonitorStatus   `json:"status"`
	LatencyMs  *float64        `json:"latencyMs,omitempty"`
	HTTPStatus int             `json:"httpStatus,omitempty"`
	Message    string          `json:"message,omitempty"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

type EscalationStep struct {
	AfterDownForSec int    `json:"afterDownForSec"`
	NotifierID      string `json:"notifierId"`
}

type EscalationPolicy struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Steps     []EscalationStep `json:"steps"`
	CreatedAt time.Time        `json:"createdAt"`
}

// EnsureSteps replaces a nil Steps slice with an empty one so JSON encodes as
// `[]` instead of `null`. Callers that build a policy from unmarshalled DB
// bytes should invoke this before serialising to the client.
func (p *EscalationPolicy) EnsureSteps() {
	if p.Steps == nil {
		p.Steps = []EscalationStep{}
	}
}

type Notification struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"` // telegram | discord | slack | webhook | email
	Config     json.RawMessage `json:"config"`
	TemplateID string          `json:"templateId,omitempty"` // empty = channel default template
	CreatedAt  time.Time       `json:"createdAt"`
}

type StatusPageMonitor struct {
	MonitorID string `json:"monitorId"`
	Position  int    `json:"position"`
}

type StatusPage struct {
	ID          string              `json:"id"`
	Slug        string              `json:"slug"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Monitors    []StatusPageMonitor `json:"monitors"`
	CreatedAt   time.Time           `json:"createdAt"`
}

// EnsureMonitors replaces a nil Monitors slice with an empty one so JSON
// encodes as `[]` instead of `null`, avoiding null.map crashes on the client.
func (p *StatusPage) EnsureMonitors() {
	if p.Monitors == nil {
		p.Monitors = []StatusPageMonitor{}
	}
}

// UptimePoint is one day of availability for a monitor's history strip.
type UptimePoint struct {
	Date string  `json:"date"` // YYYY-MM-DD
	Pct  float64 `json:"pct"`  // 0-100 availability that day; -1 = no data
}

// MonitorUptimeStat is the per-monitor SLO/uptime payload shown on public
// status pages and analytics.
type MonitorUptimeStat struct {
	MonitorID    string        `json:"monitorId"`
	UptimePct    float64       `json:"uptimePct"`    // availability over the window
	SloTargetPct float64       `json:"sloTargetPct"` // configured target (0 = none)
	SloMet       bool          `json:"sloMet"`       // uptimePct >= sloTargetPct
	WindowDays   int           `json:"windowDays"`
	History      []UptimePoint `json:"history"`
}

func (m *MonitorUptimeStat) EnsureHistory() {
	if m.History == nil {
		m.History = []UptimePoint{}
	}
}

type Incident struct {
	ID         string     `json:"id"`
	MonitorID  string     `json:"monitorId"`
	Status     string     `json:"status"` // investigating | identified | monitoring | resolved
	Headline   string     `json:"headline"`
	Body       string     `json:"body"`
	StartedAt  time.Time  `json:"startedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// MonitorAnalysis is one LLM-generated root-cause report for a monitor,
// produced automatically on down transitions or manually on demand.
type MonitorAnalysis struct {
	ID        string        `json:"id"`
	MonitorID string        `json:"monitorId"`
	Status    MonitorStatus `json:"status"` // status that triggered the analysis
	Analysis  string        `json:"analysis"`
	Model     string        `json:"model"`
	CreatedAt time.Time     `json:"createdAt"`
}

// PublicMonitor is a sanitized projection of a Monitor intended for the
// unauthenticated public overview. It deliberately omits all configuration
// secrets (DSN, basic auth, redis password, headers, etc) and only exposes
// display fields. URL is included only for web-facing checks (see handler).
type PublicMonitor struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Type          MonitorType   `json:"type"`
	Status        MonitorStatus `json:"status"`
	URL           string        `json:"url,omitempty"`
	LastCheckAt   *time.Time    `json:"lastCheckAt,omitempty"`
	LastLatencyMs *float64      `json:"lastLatencyMs,omitempty"`
	StatusReason  string        `json:"statusReason,omitempty"`
	Enabled       bool          `json:"enabled"`
	SloTargetPct  float64       `json:"sloTargetPct,omitempty"`
}

// SystemSettings are server-wide admin preferences persisted in the settings
// key/value table. Defaults apply to newly created monitors; the branding
// fields drive the public overview page header and footer.
type SystemSettings struct {
	DefaultCheckIntervalSec int     `json:"defaultCheckIntervalSec"`
	DefaultTimeoutSec       int     `json:"defaultTimeoutSec"`
	DefaultSloTargetPct     float64 `json:"defaultSloTargetPct"`
	CheckRetentionDays      int     `json:"checkRetentionDays"`
	PublicTitle             string  `json:"publicTitle"`
	PublicFooter            string  `json:"publicFooter"`
}

// AISettings configures the LLM used for root-cause analysis. Stored as JSON
// in the settings table (key "ai_settings") so the provider/model/API key can
// be changed from the UI without a restart or env edit. APIKey is never
// returned to clients in full; the router masks it. Provider is a preset id
// (see internal/ai/providers.go); BaseURL/Model override the preset when set.
type AISettings struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

// ReportSettings configures the automatic periodic uptime report. Stored as
// JSON in the settings table (key "report_settings"). Frequency is one of
// "daily", "weekly" or "monthly"; SendTime is "HH:MM" in server-local time.
// WeeklyDay is a time.Weekday string (e.g. "Monday") and MonthlyDay a day of
// month (1-28). NotificationIDs selects which configured channels receive the
// report. SendFile attaches the generated report file (Excel/CSV) to the
// selected channels instead of the plain-text summary when supported; the
// format is chosen with FileFormat ("xlsx" or "csv"). SlackBotToken +
// SlackChannel let the report be delivered straight to a Slack channel from
// Settings (no notification channel needed); the token is masked when read
// back. LastSentAt is managed by the scheduler to guarantee at-most-once
// delivery per period across restarts.
type ReportSettings struct {
	Enabled         bool            `json:"enabled"`
	Frequency       string          `json:"frequency"`
	SendTime        string          `json:"sendTime"`
	WeeklyDay       string          `json:"weeklyDay"`
	MonthlyDay      int             `json:"monthlyDay"`
	NotificationIDs []string        `json:"notificationIds"`
	Channels        []ReportChannel `json:"channels,omitempty"`
	SendFile        bool            `json:"sendFile"`
	FileFormat      string          `json:"fileFormat"`
	SubjectTemplate string          `json:"subjectTemplate"`
	BodyTemplate    string          `json:"bodyTemplate"`
	SlackBotToken   string          `json:"slackBotToken"`
	SlackChannel    string          `json:"slackChannel"`
	LastSentAt      time.Time       `json:"lastSentAt,omitempty"`
}

// ReportChannel is a delivery destination configured directly in the report
// settings, independent of the alert Notification channels. It mirrors the
// Notification shape (Type + Config) so the same sender logic is reused, but
// it lives inside the report_settings blob and is only used for periodic
// reports. Type is telegram | discord | slack | webhook | email.
type ReportChannel struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

// AlertTemplate is a named, reusable notification message stored in the
// settings table as JSON (key "alert_templates"). Channel is "email" or
// "chat"; email templates also render Subject. Each body supports
// {placeholders} substituted by the dispatcher at send time.
type AlertTemplate struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Channel        string `json:"channel"` // email | chat
	Subject        string `json:"subject,omitempty"`
	BodyDown       string `json:"bodyDown"`
	BodyUp         string `json:"bodyUp"`
	BodyDegraded   string `json:"bodyDegraded"`
	BodyEscalation string `json:"bodyEscalation"`
	IsDefault      bool   `json:"isDefault"`
	Builtin        bool   `json:"builtin"` // seeded default, offered as "restore"
}
