export type MonitorType =
  "http" | "tcp" | "dns" | "cert" | "push" | "docker" | "ping" | "redis" | "db";
export type MonitorStatus = "up" | "down" | "pending" | "degraded" | "unknown";
export type UserRole = "admin" | "viewer";

export interface User {
  id: string;
  username: string;
  role: UserRole;
  createdAt?: string;
}

export interface Monitor {
  id: string;
  name: string;
  type: MonitorType;
  url?: string;
  host?: string;
  port?: number;
  method?: string;
  expectedStatus?: number;
  keyword?: string;
  dnsServer?: string;
  recordType?: string;
  certWarnDays?: number;
  containerName?: string;
  intervalSec: number;
  timeoutSec: number;
  pushToken?: string;
  pushGraceSec?: number;
  enabled: boolean;
  escalationId?: string | null;
  notificationIds?: string[];
  // http advanced
  headers?: Record<string, string>;
  body?: string;
  basicAuthUser?: string;
  basicAuthPass?: string;
  // skip TLS certificate verification for HTTPS targets (default: false = strict)
  ignoreTls?: boolean;
  // uptime SLO/SLA target as a percentage (e.g. 99.9). 0/undefined = no target
  sloTargetPct?: number;
  // ops metadata
  tags?: string[];
  latencyWarnMs?: number;
  // redis
  redisPassword?: string;
  redisKey?: string;
  // database
  dbDriver?: string;
  dsn?: string;
  query?: string;
  expectedRows?: number;
  // audit: username that added this monitor (empty for legacy rows)
  createdBy?: string;
  createdAt?: string;
  // optional sysadmin note: internal/LAN IP for this monitor (display-only, never probed)
  localIp?: string;
  // runtime state
  status: MonitorStatus;
  lastCheckAt?: string;
  lastLatencyMs?: number;
  statusReason?: string;
  // when the current status began (last up/down/degraded flip) — drives the
  // dashboard ordering so recently changed services float to the top
  stateSince?: string;
}

export interface CheckResult {
  id: string;
  monitorId: string;
  status: MonitorStatus;
  latencyMs?: number;
  httpStatus?: number;
  message?: string;
  meta?: Record<string, unknown>;
  createdAt: string;
}

export interface Notification {
  id: string;
  name: string;
  type: "telegram" | "discord" | "slack" | "webhook" | "email";
  config: Record<string, unknown>;
  templateId?: string; // empty = channel default template
}

// Periodic uptime report config (backend settings key "report_settings").
// frequency daily|weekly|monthly; sendTime "HH:MM" server-local; weeklyDay a
// weekday name; monthlyDay 1-28. Delivery uses the report's own Channels list
// (channels), configured directly in Settings → Laporan and fully independent
// of the alert Notification channels. notificationIds/slackBotToken/slackChannel
// are legacy fields kept optional for backward compatibility but no longer used.
export interface ReportChannel {
  id: string;
  name: string;
  type: Notification["type"];
  config: Record<string, unknown>;
}

export interface ReportSettings {
  enabled: boolean;
  frequency: "daily" | "weekly" | "monthly" | "";
  sendTime: string;
  weeklyDay: string;
  monthlyDay: number;
  notificationIds?: string[];
  channels?: ReportChannel[];
  sendFile: boolean;
  fileFormat: "xlsx" | "csv" | "";
  subjectTemplate: string;
  bodyTemplate: string;
  slackBotToken?: string;
  slackChannel?: string;
  lastSentAt?: string;
}

// Reusable notification message template (see backend internal/notifier +
// the "alert_templates" settings key). Channel is "email" or "chat"; email
// templates also carry a Subject. Bodies support {placeholders}.
export interface AlertTemplate {
  id: string;
  name: string;
  channel: "email" | "chat";
  subject?: string;
  bodyDown: string;
  bodyUp: string;
  bodyDegraded: string;
  bodyEscalation: string;
  isDefault: boolean;
  builtin?: boolean;
}

export interface EscalationStep {
  afterDownForSec: number;
  notifierId: string;
}

export interface EscalationPolicy {
  id?: string;
  name: string;
  steps: EscalationStep[];
}

export interface StatusPage {
  id?: string;
  slug: string;
  title: string;
  description?: string;
  monitors: { monitorId: string; position: number }[];
}

export interface Incident {
  id: string;
  monitorId: string;
  status: "investigating" | "identified" | "monitoring" | "resolved";
  headline: string;
  body?: string;
  startedAt: string;
  resolvedAt?: string;
}

// One row of the multi-country (Globalping) ping test.
export interface MultiPingResult {
  country: string;
  city: string;
  network: string;
  minMs: number;
  avgMs: number;
  maxMs: number;
  lossPct: number;
}

export interface UptimePoint {
  date: string; // YYYY-MM-DD
  pct: number; // 0-100, or -1 when no data that day
}

export interface MonitorUptimeStat {
  monitorId: string;
  uptimePct: number;
  sloTargetPct: number;
  sloMet: boolean;
  windowDays: number;
  history: UptimePoint[];
}

// Sanitized monitor projection returned by the unauthenticated public overview.
export interface PublicMonitor {
  id: string;
  name: string;
  type: MonitorType;
  status: MonitorStatus;
  url?: string;
  lastCheckAt?: string;
  lastLatencyMs?: number;
  statusReason?: string;
  enabled: boolean;
  sloTargetPct?: number;
}

export interface SystemSettings {
  defaultCheckIntervalSec: number;
  defaultTimeoutSec: number;
  defaultSloTargetPct: number; // 0 = no global SLO target enforced
  checkRetentionDays: number; // 0 = keep forever
  publicTitle: string;
  publicFooter: string;
}

// AI provider presets and the admin-configurable LLM settings (Settings → AI).
export interface AIProvider {
  id: string;
  name: string;
  baseUrl: string;
  defaultModel: string;
}

// Returned by GET /settings/ai — the API key is never sent in full, only a
// preview + hasKey flag.
export interface AISettingsView {
  enabled: boolean;
  provider: string;
  baseUrl: string;
  model: string;
  hasKey: boolean;
  keyPreview: string;
}

// Payload for PUT /settings/ai and POST /settings/ai/test. An empty apiKey
// means "keep the stored key".
export interface AISettingsInput {
  enabled: boolean;
  provider: string;
  baseUrl: string;
  model: string;
  apiKey: string;
}

export interface AIStatus {
  enabled: boolean;
  provider: string;
  providerName: string;
  model: string;
}

// TLS-certificate and domain-registration expiry, resolved on demand by the
// monitor detail page (see backend internal/domainssl).
export interface SSLInfo {
  subject: string;
  issuer: string;
  notBefore?: string | null;
  notAfter?: string | null;
  daysRemaining: number;
  ok: boolean;
  error?: string;
}

export interface DomainInfo {
  registrar?: string;
  expiryDate?: string | null;
  daysRemaining?: number | null;
  ok: boolean;
  error?: string;
}

export interface DomainSSL {
  host: string;
  domainName?: string;
  publicIp?: string;
  ssl: SSLInfo;
  domain: DomainInfo;
}

// Outside-in domain audit (see backend internal/domainssl/domainintel.go).
export interface SSLDetail {
  subject: string;
  issuer: string;
  serial?: string;
  protocol?: string;
  keyAlgorithm?: string;
  keyBits?: number;
  sans?: string[];
  notBefore?: string | null;
  notAfter?: string | null;
  daysRemaining: number;
  expiresSoon: boolean;
  expired: boolean;
  hostnameMatch: boolean;
  chainTrusted: boolean;
  selfSigned: boolean;
  score: number;
  ok: boolean;
  error?: string;
}

export interface DNSRecords {
  a?: string[];
  aaaa?: string[];
  cname?: string;
  mx?: { host: string; pref: number }[];
  ns?: string[];
  txt?: string[];
  error?: string;
}

export interface EmailSecurity {
  spf?: { present: boolean; record?: string; ok: boolean };
  dmarc?: {
    present: boolean;
    policy?: string;
    pct: number;
    reportTo?: string;
    record?: string;
    ok: boolean;
  };
  dkim?: { selector: string; present: boolean; ok: boolean }[];
  error?: string;
}

export interface HeaderCheck {
  name: string;
  present: boolean;
  value?: string;
  good: boolean;
  note: string;
}

export interface SecurityHeaders {
  checked: string;
  checks: HeaderCheck[];
  score: number;
  error?: string;
}

export interface DomainIntel {
  host: string;
  domain?: string;
  error?: string;
  ssl?: SSLDetail;
  dns?: DNSRecords;
  email?: EmailSecurity;
  headers?: SecurityHeaders;
}

// One LLM (Qwen) root-cause report generated when a monitor goes down or on
// demand from the detail page.
export interface MonitorAnalysis {
  id: string;
  monitorId: string;
  status: MonitorStatus;
  analysis: string;
  model: string;
  createdAt: string;
}

export interface MonitorStats {
  availability: number;
  avgLatencyMs: number;
  p50LatencyMs: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
  totalChecks: number;
  downChecks: number;
}

// Messages pushed over the live websocket feed
export type LiveEvent =
  | {
      type: "monitor.update";
      monitorId: string;
      name: string;
      status: MonitorStatus;
      latencyMs?: number;
      message?: string;
      changed: boolean;
      at: string;
    }
  | { type: "monitor.check_started"; monitorId: string }
  | { type: "monitors.refresh" }
  | {
      type: "incident.update";
      incidentId: string;
      monitorId: string;
      status: string;
      headline: string;
    }
  | { type: "snapshot"; monitors: Monitor[] };
