import type { Monitor } from "./types";
import { t } from "./i18n";

export function targetLabel(m: Monitor): string {
  switch (m.type) {
    case "push":
      return t("fmt.token", { t: m.pushToken?.slice(0, 8) ?? "" });
    case "http":
      return m.url || "—";
    case "db":
      return `${m.dbDriver || "db"} · ${maskDsn(m.dsn)}`;
    case "docker":
      return m.containerName || "—";
    case "dns":
      return `${m.recordType || "A"} ${m.host || "—"}`;
    case "cert":
      return m.host || "—";
    default:
      return `${m.host || ""}${m.port ? ":" + m.port : ""}`;
  }
}

export function maskDsn(dsn?: string): string {
  if (!dsn) return "—";
  return dsn.replace(/\/\/[^@/]+@/, "//***@");
}

// "60" -> "1m", "20" -> "20s", "300" -> "5m", "7200" -> "2h"
export function humanInterval(sec?: number): string {
  if (!sec || sec <= 0) return "—";
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h`;
  return `${Math.round(sec / 86400)}d`;
}

// Human "time since" for a timestamp, relative to `now`.
export function timeAgo(iso?: string, now: number = Date.now()): string {
  if (!iso) return t("common.never");
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return t("common.never");
  const s = Math.max(0, Math.floor((now - ts) / 1000));
  if (s < 60) return t("timeago.s", { n: s });
  const m = Math.floor(s / 60);
  if (m < 60) return t("timeago.m", { n: m });
  const h = Math.floor(m / 60);
  if (h < 24) return t("timeago.h", { n: h });
  const d = Math.floor(h / 24);
  return t("timeago.d", { n: d });
}

// "2m 12s" style elapsed duration (h/m/s, drops leading zero units).
export function formatUptime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) sec = 0;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// Short one-line description of what a monitor watches, for the detail header.
export function describeMonitor(m: Monitor): string {
  switch (m.type) {
    case "http":
      return t("desc.http");
    case "tcp":
      return t("desc.tcp");
    case "ping":
      return t("desc.ping");
    case "dns":
      return t("desc.dns");
    case "cert":
      return t("desc.cert");
    case "push":
      return t("desc.push");
    case "docker":
      return t("desc.docker");
    case "redis":
      return t("desc.redis");
    case "db":
      return t("desc.db");
    default:
      return t("desc.generic");
  }
}

// Bare hostname from a URL (falls back to the raw string).
export function hostFromUrl(url?: string): string {
  if (!url) return "";
  try {
    return new URL(url).hostname;
  } catch {
    return url.replace(/^https?:\/\//, "").split("/")[0];
  }
}
