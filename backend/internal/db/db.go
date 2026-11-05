package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  username      TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monitors (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  type             TEXT NOT NULL,
  url              TEXT,
  host             TEXT,
  port             INT,
  method           TEXT,
  expected_status  INT,
  keyword          TEXT,
  dns_server       TEXT,
  record_type      TEXT,
  cert_warn_days   INT,
  container_name   TEXT,
  interval_sec     INT NOT NULL DEFAULT 60,
  timeout_sec      INT NOT NULL DEFAULT 10,
  push_token       TEXT,
  push_grace_sec   INT DEFAULT 30,
  enabled          BOOLEAN NOT NULL DEFAULT TRUE,
  escalation_id    TEXT,
  headers          JSONB,
  body             TEXT,
  basic_auth_user  TEXT,
  basic_auth_pass  TEXT,
  tags             JSONB,
  latency_warn_ms  INT,
  redis_password   TEXT,
  redis_key        TEXT,
  db_driver        TEXT,
  dsn              TEXT,
  exec_query       TEXT,
  expected_rows    INT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notifications (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL,
  config     JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS escalation_policies (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  steps      JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monitor_notifications (
  monitor_id    TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  PRIMARY KEY (monitor_id, notification_id)
);

CREATE TABLE IF NOT EXISTS checks (
  id          TEXT PRIMARY KEY,
  monitor_id  TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  status      TEXT NOT NULL,
  latency_ms  DOUBLE PRECISION,
  http_status INT,
  message     TEXT,
  meta        JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_checks_monitor_time ON checks(monitor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS status_pages (
  id          TEXT PRIMARY KEY,
  slug        TEXT UNIQUE NOT NULL,
  title       TEXT NOT NULL,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS status_page_monitors (
  status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  monitor_id     TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  position       INT NOT NULL DEFAULT 0,
  PRIMARY KEY (status_page_id, monitor_id)
);

CREATE TABLE IF NOT EXISTS incidents (
  id          TEXT PRIMARY KEY,
  monitor_id  TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  status      TEXT NOT NULL,
  headline    TEXT NOT NULL,
  body        TEXT,
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS monitor_state (
  monitor_id       TEXT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
  status           TEXT NOT NULL DEFAULT 'unknown',
  last_check_at    TIMESTAMPTZ,
  last_latency_ms  DOUBLE PRECISION,
  status_reason    TEXT,
  state_since      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_push_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_analyses (
  id         TEXT PRIMARY KEY,
  monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  status     TEXT NOT NULL,
  analysis   TEXT NOT NULL,
  model      TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ai_analyses_monitor ON ai_analyses(monitor_id, created_at DESC);

-- Additive migrations for databases created by an earlier version.
ALTER TABLE users     ADD COLUMN IF NOT EXISTS role            TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE users     ADD COLUMN IF NOT EXISTS display_name    TEXT NOT NULL DEFAULT '';
ALTER TABLE users     ADD COLUMN IF NOT EXISTS email           TEXT NOT NULL DEFAULT '';
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS headers         JSONB;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS body            TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS basic_auth_user TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS basic_auth_pass TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS tags            JSONB;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS latency_warn_ms INT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS redis_password  TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS redis_key       TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS db_driver       TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS dsn             TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS exec_query      TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS expected_rows   INT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS ignore_tls      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS slo_target_pct  DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS created_by      TEXT;
ALTER TABLE monitors  ADD COLUMN IF NOT EXISTS local_ip        TEXT;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS template_id TEXT;
`

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
