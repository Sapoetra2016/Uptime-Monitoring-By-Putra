package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uptime/monitoring/internal/models"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func newID() string { return uuid.NewString() }

// ---------- Monitors ----------

func (s *Store) ListMonitors(ctx context.Context) ([]models.Monitor, error) {
	// COALESCE the nullable text columns to '' so they scan into plain string
	// fields. Older rows (and inserts that store NULL for empty values) would
	// otherwise fail with "cannot scan NULL into *string".
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.name, m.type, COALESCE(m.url,''), COALESCE(m.host,''), m.port, COALESCE(m.method,''), m.expected_status, COALESCE(m.keyword,''),
		       COALESCE(m.dns_server,''), COALESCE(m.record_type,''), m.cert_warn_days, COALESCE(m.container_name,''), m.interval_sec, m.timeout_sec,
		       COALESCE(m.push_token,''), m.push_grace_sec, m.enabled, COALESCE(m.created_by,''), COALESCE(m.local_ip,''), m.escalation_id, m.created_at, m.updated_at,
		       m.headers, m.body, m.basic_auth_user, m.basic_auth_pass, m.tags, m.latency_warn_ms,
		       m.redis_password, m.redis_key, m.db_driver, m.dsn, m.exec_query, m.expected_rows, m.ignore_tls, m.slo_target_pct,
		       st.status, st.last_check_at, st.last_latency_ms, st.status_reason, st.state_since
		FROM monitors m
		LEFT JOIN monitor_state st ON st.monitor_id = m.id
		ORDER BY m.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Monitor{}
	for rows.Next() {
		var (
			m          models.Monitor
			st         *models.MonitorStatus
			lat        *float64
			lca        *time.Time
			rsn        *string
			ssince     *time.Time
			headersRaw []byte
			bodyRaw    *string
			bauRaw     *string
			bapRaw     *string
			tagsRaw    []byte
			warnRaw    *int
			rpRaw      *string
			rkRaw      *string
			drvRaw     *string
			dsnRaw     *string
			qRaw       *string
			expRows    *int
			ignoreTLS  bool
			sloPct     float64
		)
		err := rows.Scan(
			&m.ID, &m.Name, &m.Type, &m.URL, &m.Host, &m.Port, &m.Method, &m.ExpectedStatus, &m.Keyword,
			&m.DNSServer, &m.RecordType, &m.CertWarnDays, &m.ContainerName, &m.IntervalSec, &m.TimeoutSec,
			&m.PushToken, &m.PushGraceSec, &m.Enabled, &m.CreatedBy, &m.LocalIp, &m.EscalationID, &m.CreatedAt, &m.UpdatedAt,
			&headersRaw, &bodyRaw, &bauRaw, &bapRaw, &tagsRaw, &warnRaw,
			&rpRaw, &rkRaw, &drvRaw, &dsnRaw, &qRaw, &expRows, &ignoreTLS, &sloPct,
			&st, &lca, &lat, &rsn, &ssince,
		)
		if err != nil {
			return nil, err
		}
		if st != nil {
			m.Status = *st
		} else {
			m.Status = models.StatusUnknown
		}
		m.LastCheckAt = lca
		m.LastLatencyMs = lat
		m.StateSince = ssince
		if rsn != nil {
			m.StatusReason = *rsn
		}
		if len(headersRaw) > 0 {
			_ = json.Unmarshal(headersRaw, &m.Headers)
		}
		if len(tagsRaw) > 0 {
			_ = json.Unmarshal(tagsRaw, &m.Tags)
		}
		m.Body = deref(bodyRaw)
		m.BasicAuthUser = deref(bauRaw)
		m.BasicAuthPass = deref(bapRaw)
		if warnRaw != nil {
			m.LatencyWarnMs = *warnRaw
		}
		m.RedisPassword = deref(rpRaw)
		m.RedisKey = deref(rkRaw)
		m.DBDriver = deref(drvRaw)
		m.DSN = deref(dsnRaw)
		m.ExecQuery = deref(qRaw)
		if expRows != nil {
			m.ExpectedRows = *expRows
		}
		m.IgnoreTLS = ignoreTLS
		m.SloTargetPct = sloPct
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// load notification ids per monitor
	notifRows, err := s.pool.Query(ctx, `SELECT monitor_id, notification_id FROM monitor_notifications`)
	if err != nil {
		return nil, err
	}
	defer notifRows.Close()
	byMonitor := map[string][]string{}
	for notifRows.Next() {
		var mid, nid string
		if err := notifRows.Scan(&mid, &nid); err != nil {
			return nil, err
		}
		byMonitor[mid] = append(byMonitor[mid], nid)
	}
	for i := range out {
		out[i].NotificationIDs = byMonitor[out[i].ID]
	}
	return out, nil
}

func (s *Store) GetMonitor(ctx context.Context, id string) (*models.Monitor, error) {
	list, err := s.ListMonitors(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) CreateMonitor(ctx context.Context, m *models.Monitor) error {
	m.ID = newID()
	m.CreatedAt = time.Now().UTC()
	m.UpdatedAt = m.CreatedAt
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	headersJSON := jsonArg(m.Headers)
	tagsJSON := jsonArg(m.Tags)

	_, err = tx.Exec(ctx, `
		INSERT INTO monitors (id,name,type,url,host,port,method,expected_status,keyword,dns_server,record_type,
		  cert_warn_days,container_name,interval_sec,timeout_sec,push_token,push_grace_sec,enabled,escalation_id,
		  headers,body,basic_auth_user,basic_auth_pass,tags,latency_warn_ms,redis_password,redis_key,
		  db_driver,dsn,exec_query,expected_rows,ignore_tls,slo_target_pct,created_by,local_ip,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37)`,
		m.ID, m.Name, m.Type, nullStr(m.URL), nullStr(m.Host), m.Port, nullStr(m.Method), m.ExpectedStatus, nullStr(m.Keyword),
		nullStr(m.DNSServer), nullStr(m.RecordType), m.CertWarnDays, nullStr(m.ContainerName), m.IntervalSec, m.TimeoutSec,
		nullStr(m.PushToken), m.PushGraceSec, m.Enabled, m.EscalationID,
		headersJSON, nullStr(m.Body), nullStr(m.BasicAuthUser), nullStr(m.BasicAuthPass), tagsJSON, m.LatencyWarnMs,
		nullStr(m.RedisPassword), nullStr(m.RedisKey), nullStr(m.DBDriver), nullStr(m.DSN), nullStr(m.ExecQuery), m.ExpectedRows,
		m.IgnoreTLS, m.SloTargetPct, nullStr(m.CreatedBy), nullStr(m.LocalIp), m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `INSERT INTO monitor_state (monitor_id, status) VALUES ($1,'pending') ON CONFLICT DO NOTHING`, m.ID)
	if err != nil {
		return err
	}

	for _, nid := range m.NotificationIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_notifications (monitor_id, notification_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, m.ID, nid); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) UpdateMonitor(ctx context.Context, m *models.Monitor) error {
	m.UpdatedAt = time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	headersJSON := jsonArg(m.Headers)
	tagsJSON := jsonArg(m.Tags)

	_, err = tx.Exec(ctx, `
		UPDATE monitors SET name=$2,type=$3,url=$4,host=$5,port=$6,method=$7,expected_status=$8,keyword=$9,
		  dns_server=$10,record_type=$11,cert_warn_days=$12,container_name=$13,interval_sec=$14,timeout_sec=$15,
		  push_token=$16,push_grace_sec=$17,enabled=$18,escalation_id=$19,
		  headers=$20,body=$21,basic_auth_user=$22,basic_auth_pass=$23,tags=$24,latency_warn_ms=$25,
		  redis_password=$26,redis_key=$27,db_driver=$28,dsn=$29,exec_query=$30,expected_rows=$31,ignore_tls=$32,slo_target_pct=$33,local_ip=$34,updated_at=$35
		WHERE id=$1`,
		m.ID, m.Name, m.Type, nullStr(m.URL), nullStr(m.Host), m.Port, nullStr(m.Method), m.ExpectedStatus, nullStr(m.Keyword),
		nullStr(m.DNSServer), nullStr(m.RecordType), m.CertWarnDays, nullStr(m.ContainerName), m.IntervalSec, m.TimeoutSec,
		nullStr(m.PushToken), m.PushGraceSec, m.Enabled, m.EscalationID,
		headersJSON, nullStr(m.Body), nullStr(m.BasicAuthUser), nullStr(m.BasicAuthPass), tagsJSON, m.LatencyWarnMs,
		nullStr(m.RedisPassword), nullStr(m.RedisKey), nullStr(m.DBDriver), nullStr(m.DSN), nullStr(m.ExecQuery), m.ExpectedRows,
		m.IgnoreTLS, m.SloTargetPct, nullStr(m.LocalIp), m.UpdatedAt)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM monitor_notifications WHERE monitor_id=$1`, m.ID); err != nil {
		return err
	}
	for _, nid := range m.NotificationIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_notifications (monitor_id, notification_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, m.ID, nid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteMonitor(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM monitors WHERE id=$1`, id)
	return err
}

// SetMonitorEnabled toggles a monitor between active and paused without
// rewriting the whole row (used by the Pause/Resume button).
func (s *Store) SetMonitorEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE monitors SET enabled=$2, updated_at=now() WHERE id=$1`, id, enabled)
	return err
}

func (s *Store) SetMonitorState(ctx context.Context, monitorID string, status models.MonitorStatus, latency *float64, reason string, changedState bool) error {
	now := time.Now().UTC()
	stateSince := now
	if !changedState {
		// keep existing state_since
		var cur time.Time
		err := s.pool.QueryRow(ctx, `SELECT state_since FROM monitor_state WHERE monitor_id=$1`, monitorID).Scan(&cur)
		if err == nil {
			stateSince = cur
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO monitor_state (monitor_id, status, last_check_at, last_latency_ms, status_reason, state_since)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (monitor_id) DO UPDATE SET
		  status=EXCLUDED.status,
		  last_check_at=EXCLUDED.last_check_at,
		  last_latency_ms=EXCLUDED.last_latency_ms,
		  status_reason=EXCLUDED.status_reason,
		  state_since=EXCLUDED.state_since`,
		monitorID, string(status), now, latency, nullStr(reason), stateSince)
	return err
}

func (s *Store) RecordPush(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE monitor_state SET last_push_at = now()
		WHERE monitor_id = (SELECT id FROM monitors WHERE push_token = $1)`, token)
	return err
}

func (s *Store) GetStateSince(ctx context.Context, monitorID string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT state_since FROM monitor_state WHERE monitor_id=$1`, monitorID).Scan(&t)
	return t, err
}

func (s *Store) GetLastPushAt(ctx context.Context, monitorID string) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `SELECT last_push_at FROM monitor_state WHERE monitor_id=$1`, monitorID).Scan(&t)
	return t, err
}

// ---------- Checks ----------

func (s *Store) InsertCheck(ctx context.Context, c *models.CheckResult) error {
	c.ID = newID()
	c.CreatedAt = time.Now().UTC()
	meta := c.Meta
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO checks (id, monitor_id, status, latency_ms, http_status, message, meta, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.MonitorID, string(c.Status), c.LatencyMs, c.HTTPStatus, nullStr(c.Message), meta, c.CreatedAt)
	return err
}

func (s *Store) RecentChecks(ctx context.Context, monitorID string, limit int) ([]models.CheckResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, latency_ms, http_status, message, meta, created_at
		FROM checks WHERE monitor_id=$1 ORDER BY created_at DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CheckResult{}
	for rows.Next() {
		var c models.CheckResult
		var status string
		var msg *string
		var meta []byte
		if err := rows.Scan(&c.ID, &c.MonitorID, &status, &c.LatencyMs, &c.HTTPStatus, &msg, &meta, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Status = models.MonitorStatus(status)
		if msg != nil {
			c.Message = *msg
		}
		c.Meta = json.RawMessage(meta)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Availability + latency percentiles over a time window.
type Stats struct {
	Availability float64 `json:"availability"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	P50LatencyMs float64 `json:"p50LatencyMs"`
	P95LatencyMs float64 `json:"p95LatencyMs"`
	P99LatencyMs float64 `json:"p99LatencyMs"`
	TotalChecks  int     `json:"totalChecks"`
	DownChecks   int     `json:"downChecks"`
}

func (s *Store) MonitorStats(ctx context.Context, monitorID string, since time.Time) (*Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status IN ('up','degraded'))::float / NULLIF(COUNT(*),0),
		  AVG(latency_ms) FILTER (WHERE latency_ms IS NOT NULL),
		  percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms),
		  percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),
		  percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms),
		  COUNT(*),
		  COUNT(*) FILTER (WHERE status = 'down')
		FROM checks WHERE monitor_id=$1 AND created_at >= $2`, monitorID, since).Scan(
		&st.Availability, &st.AvgLatencyMs, &st.P50LatencyMs, &st.P95LatencyMs, &st.P99LatencyMs, &st.TotalChecks, &st.DownChecks)
	if errors.Is(err, pgx.ErrNoRows) {
		return &st, nil
	}
	return &st, err
}

// UptimeDaily returns per-day availability (oldest -> newest, exactly `days`
// entries; days without checks get Pct = -1) plus the overall availability
// percentage across the window. Used for public status page history strips.
func (s *Store) UptimeDaily(ctx context.Context, monitorID string, days int) ([]models.UptimePoint, float64, error) {
	if days <= 0 {
		days = 90
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC)

	rows, err := s.pool.Query(ctx, `
		SELECT to_char(date_trunc('day', created_at at time zone 'UTC'), 'YYYY-MM-DD') AS d,
		       COUNT(*) FILTER (WHERE status IN ('up','degraded'))::float / NULLIF(COUNT(*),0) * 100 AS pct
		FROM checks WHERE monitor_id=$1 AND created_at >= $2
		GROUP BY d`, monitorID, since)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	byDay := map[string]float64{}
	for rows.Next() {
		var d string
		var pct float64
		if err := rows.Scan(&d, &pct); err != nil {
			return nil, 0, err
		}
		byDay[d] = pct
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	today := time.Now().UTC()
	out := make([]models.UptimePoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		if pct, ok := byDay[key]; ok {
			out = append(out, models.UptimePoint{Date: key, Pct: pct})
		} else {
			out = append(out, models.UptimePoint{Date: key, Pct: -1})
		}
	}

	var overall float64
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(COUNT(*) FILTER (WHERE status IN ('up','degraded'))::float / NULLIF(COUNT(*),0) * 100, 100)
		FROM checks WHERE monitor_id=$1 AND created_at >= $2`, monitorID, since).Scan(&overall)
	if err != nil {
		return out, 0, err
	}
	return out, overall, nil
}

// ChecksInWindow returns raw checks for a monitor since the given time.
func (s *Store) ChecksInWindow(ctx context.Context, monitorID string, since time.Time) ([]models.CheckResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, latency_ms, http_status, message, meta, created_at
		FROM checks WHERE monitor_id=$1 AND created_at >= $2 ORDER BY created_at ASC`, monitorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CheckResult{}
	for rows.Next() {
		var c models.CheckResult
		var status string
		var msg *string
		var meta []byte
		if err := rows.Scan(&c.ID, &c.MonitorID, &status, &c.LatencyMs, &c.HTTPStatus, &msg, &meta, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Status = models.MonitorStatus(status)
		if msg != nil {
			c.Message = *msg
		}
		c.Meta = json.RawMessage(meta)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- Notifications ----------

func (s *Store) ListNotifications(ctx context.Context) ([]models.Notification, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, type, config, COALESCE(template_id,''), created_at FROM notifications ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Notification{}
	for rows.Next() {
		var n models.Notification
		var cfg []byte
		if err := rows.Scan(&n.ID, &n.Name, &n.Type, &cfg, &n.TemplateID, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Config = json.RawMessage(cfg)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) GetNotification(ctx context.Context, id string) (*models.Notification, error) {
	var n models.Notification
	var cfg []byte
	err := s.pool.QueryRow(ctx, `SELECT id, name, type, config, COALESCE(template_id,''), created_at FROM notifications WHERE id=$1`, id).
		Scan(&n.ID, &n.Name, &n.Type, &cfg, &n.TemplateID, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	n.Config = json.RawMessage(cfg)
	return &n, err
}

func (s *Store) CreateNotification(ctx context.Context, n *models.Notification) error {
	n.ID = newID()
	n.CreatedAt = time.Now().UTC()
	cfg := n.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO notifications (id,name,type,config,template_id,created_at) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)`,
		n.ID, n.Name, n.Type, cfg, n.TemplateID, n.CreatedAt)
	return err
}

func (s *Store) UpdateNotification(ctx context.Context, n *models.Notification) error {
	cfg := n.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET name=$2, type=$3, config=$4, template_id=NULLIF($5,'') WHERE id=$1`, n.ID, n.Name, n.Type, cfg, n.TemplateID)
	return err
}

func (s *Store) DeleteNotification(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notifications WHERE id=$1`, id)
	return err
}

// ---------- Escalation policies ----------

func (s *Store) ListEscalations(ctx context.Context) ([]models.EscalationPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, steps, created_at FROM escalation_policies ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.EscalationPolicy{}
	for rows.Next() {
		var p models.EscalationPolicy
		var stepsRaw []byte
		if err := rows.Scan(&p.ID, &p.Name, &stepsRaw, &p.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(stepsRaw, &p.Steps); err != nil {
			return nil, fmt.Errorf("unmarshal steps: %w", err)
		}
		p.EnsureSteps()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetEscalation(ctx context.Context, id string) (*models.EscalationPolicy, error) {
	var p models.EscalationPolicy
	var stepsRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT id, name, steps, created_at FROM escalation_policies WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &stepsRaw, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err := json.Unmarshal(stepsRaw, &p.Steps); err != nil {
		return nil, err
	}
	p.EnsureSteps()
	return &p, err
}

func (s *Store) UpsertEscalation(ctx context.Context, p *models.EscalationPolicy) error {
	p.EnsureSteps()
	steps, _ := json.Marshal(p.Steps)
	if p.ID == "" {
		p.ID = newID()
		p.CreatedAt = time.Now().UTC()
		_, err := s.pool.Exec(ctx, `INSERT INTO escalation_policies (id,name,steps,created_at) VALUES ($1,$2,$3,$4)`,
			p.ID, p.Name, steps, p.CreatedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE escalation_policies SET name=$2, steps=$3 WHERE id=$1`, p.ID, p.Name, steps)
	return err
}

func (s *Store) DeleteEscalation(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM escalation_policies WHERE id=$1`, id)
	return err
}

// ---------- Status pages ----------

func (s *Store) ListStatusPages(ctx context.Context) ([]models.StatusPage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, slug, title, description, created_at FROM status_pages ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StatusPage{}
	for rows.Next() {
		var p models.StatusPage
		var desc *string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &desc, &p.CreatedAt); err != nil {
			return nil, err
		}
		if desc != nil {
			p.Description = *desc
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Monitors = []models.StatusPageMonitor{}
		pm, err := s.pool.Query(ctx, `SELECT monitor_id, position FROM status_page_monitors WHERE status_page_id=$1 ORDER BY position`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for pm.Next() {
			var m models.StatusPageMonitor
			if err := pm.Scan(&m.MonitorID, &m.Position); err != nil {
				pm.Close()
				return nil, err
			}
			out[i].Monitors = append(out[i].Monitors, m)
		}
		pm.Close()
	}
	return out, nil
}

func (s *Store) GetStatusPageBySlug(ctx context.Context, slug string) (*models.StatusPage, error) {
	pages, err := s.ListStatusPages(ctx)
	if err != nil {
		return nil, err
	}
	for i := range pages {
		if pages[i].Slug == slug {
			pages[i].EnsureMonitors()
			return &pages[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) UpsertStatusPage(ctx context.Context, p *models.StatusPage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if p.ID == "" {
		p.ID = newID()
		p.CreatedAt = time.Now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO status_pages (id,slug,title,description,created_at) VALUES ($1,$2,$3,$4,$5)`,
			p.ID, p.Slug, p.Title, nullStr(p.Description), p.CreatedAt); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE status_pages SET slug=$2,title=$3,description=$4 WHERE id=$1`,
			p.ID, p.Slug, p.Title, nullStr(p.Description)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM status_page_monitors WHERE status_page_id=$1`, p.ID); err != nil {
			return err
		}
	}

	for _, m := range p.Monitors {
		if _, err := tx.Exec(ctx, `INSERT INTO status_page_monitors (status_page_id, monitor_id, position) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			p.ID, m.MonitorID, m.Position); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteStatusPage(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM status_pages WHERE id=$1`, id)
	return err
}

// ---------- Incidents ----------

func (s *Store) ListActiveIncidents(ctx context.Context) ([]models.Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, headline, body, started_at, resolved_at
		FROM incidents WHERE resolved_at IS NULL ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Incident{}
	for rows.Next() {
		var i models.Incident
		var body *string
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Status, &i.Headline, &body, &i.StartedAt, &i.ResolvedAt); err != nil {
			return nil, err
		}
		if body != nil {
			i.Body = *body
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) CreateIncident(ctx context.Context, i *models.Incident) error {
	i.ID = newID()
	i.StartedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `INSERT INTO incidents (id,monitor_id,status,headline,body,started_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		i.ID, i.MonitorID, i.Status, i.Headline, nullStr(i.Body), i.StartedAt)
	return err
}

func (s *Store) UpdateIncident(ctx context.Context, i *models.Incident) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET status=$2, headline=$3, body=$4, resolved_at=$5 WHERE id=$1`,
		i.ID, i.Status, i.Headline, nullStr(i.Body), i.ResolvedAt)
	return err
}

// GetOpenIncident returns the monitor's unresolved incident, if any.
func (s *Store) GetOpenIncident(ctx context.Context, monitorID string) (*models.Incident, error) {
	var i models.Incident
	var body *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, monitor_id, status, headline, body, started_at, resolved_at
		FROM incidents WHERE monitor_id=$1 AND resolved_at IS NULL ORDER BY started_at DESC LIMIT 1`,
		monitorID).Scan(&i.ID, &i.MonitorID, &i.Status, &i.Headline, &body, &i.StartedAt, &i.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if body != nil {
		i.Body = *body
	}
	return &i, nil
}

// ResolveOpenIncidents closes every unresolved incident of a monitor and
// returns the closed rows so callers can broadcast the updates.
func (s *Store) ResolveOpenIncidents(ctx context.Context, monitorID string, at time.Time) ([]models.Incident, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE incidents SET status='resolved', resolved_at=$2
		WHERE monitor_id=$1 AND resolved_at IS NULL
		RETURNING id, monitor_id, status, headline, body, started_at, resolved_at`,
		monitorID, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Incident{}
	for rows.Next() {
		var i models.Incident
		var body *string
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Status, &i.Headline, &body, &i.StartedAt, &i.ResolvedAt); err != nil {
			return nil, err
		}
		if body != nil {
			i.Body = *body
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ListIncidentsForMonitor returns the most recent incidents of one monitor,
// including resolved ones (used by the detail page timeline).
func (s *Store) ListIncidentsForMonitor(ctx context.Context, monitorID string, limit int) ([]models.Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, headline, body, started_at, resolved_at
		FROM incidents WHERE monitor_id=$1 ORDER BY started_at DESC LIMIT $2`,
		monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Incident{}
	for rows.Next() {
		var i models.Incident
		var body *string
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Status, &i.Headline, &body, &i.StartedAt, &i.ResolvedAt); err != nil {
			return nil, err
		}
		if body != nil {
			i.Body = *body
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ListIncidentsSince returns every incident whose started_at falls at or
// after the given time, including resolved ones. Used by the periodic report
// builder to summarise what happened during the reporting window.
func (s *Store) ListIncidentsSince(ctx context.Context, since time.Time) ([]models.Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, headline, body, started_at, resolved_at
		FROM incidents WHERE started_at >= $1 ORDER BY started_at DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Incident{}
	for rows.Next() {
		var i models.Incident
		var body *string
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Status, &i.Headline, &body, &i.StartedAt, &i.ResolvedAt); err != nil {
			return nil, err
		}
		if body != nil {
			i.Body = *body
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ---------- Users ----------

func (s *Store) GetUserByUsername(ctx context.Context, username string) (id, hash string, role models.UserRole, err error) {
	err = s.pool.QueryRow(ctx, `SELECT id, password_hash, role FROM users WHERE username=$1`, username).Scan(&id, &hash, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	return id, hash, role, err
}

func (s *Store) EnsureUser(ctx context.Context, username, hash string, role models.UserRole) error {
	if role == "" {
		role = models.RoleAdmin
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES ($1,$2,$3,$4) ON CONFLICT (username) DO NOTHING`,
		newID(), username, hash, string(role))
	return err
}

func (s *Store) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, username, COALESCE(display_name,''), COALESCE(email,''), role, created_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		var u models.User
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &role, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Role = models.UserRole(role)
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserProfile loads the self-service profile fields for one user id.
func (s *Store) GetUserProfile(ctx context.Context, id string) (displayName, email string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(display_name,''), COALESCE(email,'') FROM users WHERE id=$1`, id).Scan(&displayName, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return displayName, email, err
}

// UpdateUserProfile persists self-service profile edits (display name + email).
// Username and role are intentionally not editable here.
func (s *Store) UpdateUserProfile(ctx context.Context, id, displayName, email string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET display_name=$2, email=$3 WHERE id=$1`, id, displayName, email)
	return err
}

func (s *Store) CreateUser(ctx context.Context, username, hash string, role models.UserRole) error {
	if role == "" {
		role = models.RoleViewer
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES ($1,$2,$3,$4)`,
		newID(), username, hash, string(role))
	return err
}

// GetUserCredentials loads a user's password hash and role by user id, used
// by the self-service change-password endpoint.
func (s *Store) GetUserCredentials(ctx context.Context, id string) (hash string, role models.UserRole, err error) {
	err = s.pool.QueryRow(ctx, `SELECT password_hash, role FROM users WHERE id=$1`, id).Scan(&hash, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return hash, role, err
}

func (s *Store) SetUserRole(ctx context.Context, id string, role models.UserRole) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET role=$2 WHERE id=$1`, id, string(role))
	return err
}

func (s *Store) SetUserPassword(ctx context.Context, id, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

// ---------- Settings (key/value) ----------

// DefaultSystemSettings is used when a key has never been saved.
func DefaultSystemSettings() models.SystemSettings {
	return models.SystemSettings{
		DefaultCheckIntervalSec: 60,
		DefaultTimeoutSec:       10,
		DefaultSloTargetPct:     0, // 0 = no global SLO target enforced
		CheckRetentionDays:      0, // 0 = keep history forever
		PublicTitle:             "System Status",
		PublicFooter:            "Powered by Uptime Monitoring",
	}
}

func (s *Store) GetSystemSettings(ctx context.Context) (models.SystemSettings, error) {
	set := DefaultSystemSettings()
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return set, err
	}
	defer rows.Close()
	kv := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return set, err
		}
		kv[k] = v
	}
	if err := rows.Err(); err != nil {
		return set, err
	}
	atoi := func(key string, dst *int) {
		if v, ok := kv[key]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	atof := func(key string, dst *float64) {
		if v, ok := kv[key]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				*dst = f
			}
		}
	}
	atoi("default_check_interval_sec", &set.DefaultCheckIntervalSec)
	atoi("default_timeout_sec", &set.DefaultTimeoutSec)
	atof("default_slo_target_pct", &set.DefaultSloTargetPct)
	atoi("check_retention_days", &set.CheckRetentionDays)
	if v, ok := kv["public_title"]; ok {
		set.PublicTitle = v
	}
	if v, ok := kv["public_footer"]; ok {
		set.PublicFooter = v
	}
	return set, nil
}

func (s *Store) SaveSystemSettings(ctx context.Context, set models.SystemSettings) error {
	kv := map[string]string{
		"default_check_interval_sec": strconv.Itoa(set.DefaultCheckIntervalSec),
		"default_timeout_sec":        strconv.Itoa(set.DefaultTimeoutSec),
		"default_slo_target_pct":     strconv.FormatFloat(set.DefaultSloTargetPct, 'f', -1, 64),
		"check_retention_days":       strconv.Itoa(set.CheckRetentionDays),
		"public_title":               set.PublicTitle,
		"public_footer":              set.PublicFooter,
	}
	for k, v := range kv {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, k, v)
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------- AI settings ----------

// aiSettingsKey is the settings key holding the JSON AI provider config.
const aiSettingsKey = "ai_settings"

// GetAISettings returns the saved AI provider config, or a zero (disabled)
// value when the key has never been written.
func (s *Store) GetAISettings(ctx context.Context) (models.AISettings, error) {
	var raw string
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, aiSettingsKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || strings.TrimSpace(raw) == "" {
		return models.AISettings{}, nil
	}
	if err != nil {
		return models.AISettings{}, err
	}
	var set models.AISettings
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		return models.AISettings{}, err
	}
	return set, nil
}

// SaveAISettings persists the AI provider config as JSON under the settings key.
func (s *Store) SaveAISettings(ctx context.Context, set models.AISettings) error {
	b, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		aiSettingsKey, string(b))
	return err
}

// ---------- Report settings ----------

// reportSettingsKey is the settings key holding the JSON periodic report config.
const reportSettingsKey = "report_settings"

// GetReportSettings returns the saved report config, or a zero (disabled)
// value when the key has never been written.
func (s *Store) GetReportSettings(ctx context.Context) (models.ReportSettings, error) {
	var raw string
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, reportSettingsKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || strings.TrimSpace(raw) == "" {
		return models.ReportSettings{}, nil
	}
	if err != nil {
		return models.ReportSettings{}, err
	}
	var set models.ReportSettings
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		return models.ReportSettings{}, err
	}
	return set, nil
}

// SaveReportSettings persists the report config as JSON under the settings key.
func (s *Store) SaveReportSettings(ctx context.Context, set models.ReportSettings) error {
	b, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		reportSettingsKey, string(b))
	return err
}

// ApplyDefaultsToAllMonitors bulk-overwrites the interval, timeout and SLO
// target of every monitor with the given global defaults. A non-positive
// argument means "leave that column untouched". Returns the number of rows
// updated.
func (s *Store) ApplyDefaultsToAllMonitors(ctx context.Context, intervalSec, timeoutSec int, sloPct float64) (int64, error) {
	if intervalSec <= 0 && timeoutSec <= 0 && sloPct <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE monitors SET
			interval_sec    = CASE WHEN $1::int > 0 THEN $1::int ELSE interval_sec END,
			timeout_sec     = CASE WHEN $2::int > 0 THEN $2::int ELSE timeout_sec END,
			slo_target_pct  = CASE WHEN $3::float8 > 0 THEN $3::float8 ELSE slo_target_pct END,
			updated_at      = now()`,
		intervalSec, timeoutSec, sloPct)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------- Alert templates ----------

// alertTemplatesKey is the settings key holding the JSON template library.
const alertTemplatesKey = "alert_templates"

// DefaultAlertTemplates are the built-in templates seeded when none are saved
// yet. They mirror the historical hardcoded wording so existing behaviour is
// preserved until an admin customises them.
func DefaultAlertTemplates() []models.AlertTemplate {
	return []models.AlertTemplate{
		{
			ID: "builtin-chat", Name: "Default (Chat)", Channel: "chat",
			BodyDown:       "🔴 {monitor} DOWN\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
			BodyUp:         "✅ {monitor} UP again\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nDown for     : {duration}",
			BodyDegraded:   "⚠️ {monitor} DEGRADED\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
			BodyEscalation: "🔴 [ESCALATION step {step} - down {duration}] {monitor} masih DOWN\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
			IsDefault:      true, Builtin: true,
		},
		{
			ID: "builtin-email", Name: "Default (Email)", Channel: "email",
			Subject:        "[{status}] {monitor}",
			BodyDown:       "{monitor} is DOWN at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}\nTarget       : {url}",
			BodyUp:         "{monitor} is UP again at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nDown for     : {duration}",
			BodyDegraded:   "{monitor} is DEGRADED at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nPesan        : {message}",
			BodyEscalation: "[ESCALATION step {step}] {monitor} has been DOWN for {duration}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
			IsDefault:      true, Builtin: true,
		},
	}
}

// GetAlertTemplates returns the saved library, seeding built-in defaults (not
// persisted) when the key has never been written.
func (s *Store) GetAlertTemplates(ctx context.Context) ([]models.AlertTemplate, error) {
	var raw string
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, alertTemplatesKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || strings.TrimSpace(raw) == "" {
		return DefaultAlertTemplates(), nil
	}
	if err != nil {
		return nil, err
	}
	var list []models.AlertTemplate
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return DefaultAlertTemplates(), nil
	}
	return list, nil
}

// SaveAlertTemplates persists the whole library as JSON under the settings key.
// It fills missing IDs and keeps at most one default per channel (the first
// flagged one wins; if none is flagged the first of each channel becomes the
// default) so the dispatcher always has a template to fall back to.
func (s *Store) SaveAlertTemplates(ctx context.Context, list []models.AlertTemplate) error {
	if list == nil {
		list = []models.AlertTemplate{}
	}
	seenDefault := map[string]bool{}
	for i := range list {
		if list[i].ID == "" {
			list[i].ID = newID()
		}
		if list[i].IsDefault {
			if seenDefault[list[i].Channel] {
				list[i].IsDefault = false
			} else {
				seenDefault[list[i].Channel] = true
			}
		}
	}
	for _, ch := range []string{"chat", "email"} {
		if !seenDefault[ch] {
			for i := range list {
				if list[i].Channel == ch {
					list[i].IsDefault = true
					break
				}
			}
		}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, alertTemplatesKey, string(b))
	return err
}

// ---------- Check-history maintenance ----------

func (s *Store) CountChecks(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM checks`).Scan(&n)
	return n, err
}

// DeleteOldChecks removes check rows older than maxAgeDays. A non-positive
// window is treated as "keep forever" and deletes nothing (safety guard).
func (s *Store) DeleteOldChecks(ctx context.Context, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays)
	tag, err := s.pool.Exec(ctx, `DELETE FROM checks WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------- AI analyses ----------

// InsertAnalysis stores one LLM root-cause report for a monitor.
func (s *Store) InsertAnalysis(ctx context.Context, a *models.MonitorAnalysis) error {
	a.ID = newID()
	a.CreatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ai_analyses (id, monitor_id, status, analysis, model, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ID, a.MonitorID, string(a.Status), a.Analysis, a.Model, a.CreatedAt)
	return err
}

// ListAnalyses returns the newest AI analyses for a monitor.
func (s *Store) ListAnalyses(ctx context.Context, monitorID string, limit int) ([]models.MonitorAnalysis, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, monitor_id, status, analysis, model, created_at
		FROM ai_analyses WHERE monitor_id=$1 ORDER BY created_at DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.MonitorAnalysis{}
	for rows.Next() {
		var a models.MonitorAnalysis
		var status string
		if err := rows.Scan(&a.ID, &a.MonitorID, &status, &a.Analysis, &a.Model, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Status = models.MonitorStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// jsonArg marshals v to JSON for a JSONB column, returning NULL for empty
// maps/slices so the column stays clean instead of storing "null".
func jsonArg(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]string:
		if len(t) == 0 {
			return nil
		}
	case []string:
		if len(t) == 0 {
			return nil
		}
	case nil:
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
