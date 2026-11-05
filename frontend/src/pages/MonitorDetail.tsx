import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import LatencyChart from "../components/LatencyChart";
import StatusRing from "../components/StatusRing";
import DomainIntelCard from "../components/DomainIntelCard";
import MultiPingCard from "../components/MultiPingCard";
import IncidentList from "../components/IncidentList";
import Card, { CardTitle } from "../components/Card";
import Modal from "../components/Modal";
import MonitorForm from "../components/MonitorForm";
import { useIsChecking, useMonitor } from "../hooks/useMonitor";
import { api, getRole } from "../lib/api";
import { monitorStore } from "../lib/monitorStore";
import { t, useI18n } from "../lib/i18n";
import {
  describeMonitor,
  formatUptime,
  hostFromUrl,
  humanInterval,
  targetLabel,
  timeAgo,
} from "../lib/format";
import { useLive } from "../hooks/useLive";
import type {
  AIStatus,
  CheckResult,
  DomainSSL,
  EscalationPolicy,
  Incident,
  Monitor,
  MonitorAnalysis,
  MonitorStats,
  MonitorStatus,
  MonitorUptimeStat,
  Notification,
} from "../lib/types";

const STATUS_HEX: Record<MonitorStatus, string> = {
  up: "#22c55e",
  down: "#ef4444",
  degraded: "#eab308",
  pending: "#94a3b8",
  unknown: "#64748b",
};

// rank so a bucket takes the "worst" status seen inside it
const RANK: Record<MonitorStatus, number> = {
  down: 4,
  degraded: 3,
  pending: 2,
  unknown: 1,
  up: 0,
};

export default function MonitorDetail() {
  const { id = "" } = useParams();
  const monitor = useMonitor(id);
  const checking = useIsChecking(id);
  const { t } = useI18n();
  const isAdmin = getRole() === "admin";
  const { refresh } = useLive();

  const [checks, setChecks] = useState<CheckResult[]>([]);
  const [stats, setStats] = useState<MonitorStats | null>(null);
  const [uptime, setUptime] = useState<MonitorUptimeStat | null>(null);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [domainSsl, setDomainSsl] = useState<DomainSSL | null>(null);
  const [domainSslLoading, setDomainSslLoading] = useState(false);
  const [analyses, setAnalyses] = useState<MonitorAnalysis[]>([]);
  const [analysesLoaded, setAnalysesLoaded] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [analyzeError, setAnalyzeError] = useState("");
  const [aiStatus, setAiStatus] = useState<AIStatus | null>(null);
  const [hours, setHours] = useState(24);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [toggling, setToggling] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editError, setEditError] = useState("");
  const [escalations, setEscalations] = useState<EscalationPolicy[]>([]);

  // Escalation policies feed the edit form's dropdown (fetched once).
  useEffect(() => {
    api
      .listEscalations()
      .then(setEscalations)
      .catch(() => {});
  }, []);

  // Live "time ago" / "currently up for" ticker.
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);

  // If the store doesn't have this monitor on first paint, ask for a fresh
  // snapshot — a direct URL visit shouldn't stay stuck on "Loading…".
  useEffect(() => {
    if (!monitor && id) refresh();
  }, [monitor, id, refresh]);

  // History + aggregated uptime + incidents + notification targets.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    setLoading(true);
    setLoadError("");
    Promise.all([
      api.monitorStats(id, hours),
      api.monitorUptime(id, 365).catch(() => null),
      api.listIncidents(id).catch(() => [] as Incident[]),
      api.listNotifications().catch(() => [] as Notification[]),
    ])
      .then(([stat, up, inc, notif]) => {
        if (cancelled) return;
        setChecks(stat?.checks ?? []);
        setStats(stat?.stats ?? null);
        setUptime(up);
        setIncidents(inc ?? []);
        setNotifications(notif ?? []);
      })
      .catch((e: any) => {
        if (cancelled) return;
        setLoadError(e?.message || t("det.loadHistoryFailed"));
      })
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, hours, monitor?.lastCheckAt]);

  // Domain & SSL expiry is resolved lazily — a WHOIS round-trip can take a
  // few seconds, so it loads independently of the fast history above.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    setDomainSsl(null);
    setDomainSslLoading(true);
    api
      .domainSsl(id)
      .then((res) => !cancelled && setDomainSsl(res))
      .catch(() => !cancelled && setDomainSsl(null))
      .finally(() => !cancelled && setDomainSslLoading(false));
    return () => {
      cancelled = true;
    };
  }, [id]);

  // AI analyses are (re)loaded on mount and whenever the monitor flips to
  // down — the backend generates a fresh root-cause report automatically then.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    api
      .listAnalyses(id)
      .then((res) => !cancelled && setAnalyses(res ?? []))
      .catch(() => !cancelled && setAnalyses([]))
      .finally(() => !cancelled && setAnalysesLoaded(true));
    return () => {
      cancelled = true;
    };
  }, [id, monitor?.status, monitor?.lastCheckAt]);

  // Which provider/model the admin configured (Settings → AI). Read once; it
  // drives the badge and the "not configured" hint.
  useEffect(() => {
    let cancelled = false;
    api
      .aiStatus()
      .then((s) => !cancelled && setAiStatus(s))
      .catch(() => !cancelled && setAiStatus(null));
    return () => {
      cancelled = true;
    };
  }, []);

  const checksAsc = useMemo(
    () =>
      [...checks].sort(
        (a, b) =>
          new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime(),
      ),
    [checks],
  );

  // ---- derived metrics -------------------------------------------------
  const isUpState = (s?: MonitorStatus) => s === "up" || s === "degraded";

  // How long we've been in the current up/down state (bounded by the window).
  const runSecs = useMemo(() => {
    if (!checksAsc.length) return null;
    const targetUp = isUpState(monitor?.status);
    let startT: number | null = null;
    for (let i = checksAsc.length - 1; i >= 0; i--) {
      if (isUpState(checksAsc[i].status) !== targetUp) {
        startT = new Date(
          checksAsc[i + 1]?.createdAt ?? checksAsc[i].createdAt,
        ).getTime();
        break;
      }
    }
    if (startT === null) startT = new Date(checksAsc[0].createdAt).getTime();
    return Math.max(0, (now - startT) / 1000);
  }, [checksAsc, monitor?.status, now]);

  // 24-hour segmented bar (30 buckets, oldest → newest).
  const last24 = useMemo(() => {
    const windowMs = 24 * 3600 * 1000;
    const count = 30;
    const buckets: (MonitorStatus | null)[] = Array(count).fill(null);
    let downChecks = 0;
    let failures = 0;
    let prevUp = true;
    const from = now - windowMs;
    for (const c of checksAsc) {
      const t = new Date(c.createdAt).getTime();
      if (t < from) continue;
      const idx = Math.min(
        count - 1,
        Math.floor(((t - from) / windowMs) * count),
      );
      const cur = buckets[idx];
      if (!cur || RANK[c.status] > RANK[cur]) buckets[idx] = c.status;
      if (c.status === "down") downChecks++;
      const up = isUpState(c.status);
      if (prevUp && !up) failures++;
      prevUp = up;
    }
    const interval = monitor?.intervalSec ?? 60;
    const downMinutes = Math.round((downChecks * interval) / 60);
    const pct =
      buckets.some((b) => b === "down") || failures > 0
        ? (100 *
            checksAsc.filter(
              (c) =>
                new Date(c.createdAt).getTime() >= from && c.status !== "down",
            ).length) /
          Math.max(
            1,
            checksAsc.filter((c) => new Date(c.createdAt).getTime() >= from)
              .length,
          )
        : 100;
    return { buckets, failures, downMinutes, pct };
  }, [checksAsc, now, monitor?.intervalSec]);

  // Response-time summary over the selected window.
  const resp = useMemo(() => {
    const vals = checksAsc
      .map((c) => c.latencyMs)
      .filter((v): v is number => v != null);
    if (!vals.length) return { avg: null, min: null, max: null };
    return {
      avg: vals.reduce((a, b) => a + b, 0) / vals.length,
      min: Math.min(...vals),
      max: Math.max(...vals),
    };
  }, [checksAsc]);

  const pctFor = (days: number): number | null => {
    if (!uptime?.history?.length) return null;
    const slice = uptime.history.slice(-days).filter((p) => p.pct >= 0);
    if (!slice.length) return null;
    return slice.reduce((a, p) => a + p.pct, 0) / slice.length;
  };

  // ---- actions ---------------------------------------------------------
  async function checkNow() {
    try {
      await api.manualCheck(id);
    } catch {
      /* surfaced via live feed */
    }
  }

  async function toggleEnabled() {
    if (!monitor) return;
    const next = !monitor.enabled;
    setToggling(true);
    monitorStore.updateOne(id, { enabled: next });
    try {
      await api.setMonitorEnabled(id, next);
      refresh();
    } catch {
      monitorStore.updateOne(id, { enabled: monitor.enabled });
    } finally {
      setToggling(false);
    }
  }

  async function testNotification() {
    const nid = monitor?.notificationIds?.[0];
    if (!nid) return;
    try {
      await api.testNotification(nid);
    } catch {
      /* ignore */
    }
  }

  // On-demand LLM analysis (admin). The request blocks until Qwen replies.
  async function reanalyze() {
    setAnalyzing(true);
    setAnalyzeError("");
    try {
      const a = await api.analyzeMonitor(id);
      setAnalyses((prev) => [a, ...prev].slice(0, 5));
    } catch (e: any) {
      setAnalyzeError(e?.message || t("det.aiFailed"));
    } finally {
      setAnalyzing(false);
    }
  }

  // Open the same edit form used on the monitors list, right here on the
  // detail page, instead of bouncing the user back to /monitors first.
  async function saveEdit(m: Partial<Monitor>) {
    if (!monitor) return;
    setSaving(true);
    setEditError("");
    try {
      await api.updateMonitor(monitor.id, m);
      setEditing(false);
      refresh();
    } catch (e: any) {
      setEditError(e.message || t("mon.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  // ---- render ----------------------------------------------------------
  if (!monitor) {
    return (
      <div className="space-y-4">
        <div className="text-slate-400">{t("det.loading")}</div>
        <div className="text-xs text-slate-500">
          {t("det.loadingHint")}{" "}
          <Link className="underline" to="/monitors">
            {t("det.backMonitors")}
          </Link>
          .
        </div>
      </div>
    );
  }

  const paused = !monitor.enabled;
  const headline = paused ? t("common.paused") : t(`status.${monitor.status}`);
  const headlineColor = paused
    ? "text-amber-400"
    : monitor.status === "up"
      ? "text-app-up"
      : monitor.status === "down"
        ? "text-app-down"
        : monitor.status === "degraded"
          ? "text-amber-400"
          : "text-slate-300";
  const linkedNotifs = notifications.filter((n) =>
    (monitor.notificationIds ?? []).includes(n.id),
  );

  return (
    <div className="space-y-6">
      {/* Back link */}
      <div>
        <Link
          to="/monitors"
          className="inline-flex items-center gap-1 text-sm text-slate-400 hover:text-white bg-app-panel border border-slate-800 rounded-md px-3 py-1.5"
        >
          <span aria-hidden>‹</span> {t("det.back")}
        </Link>
      </div>

      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <StatusRing
              status={paused ? "pending" : monitor.status}
              size={20}
            />
            {checking && (
              <span className="absolute inset-0 rounded-full animate-ping bg-sky-400/40" />
            )}
          </div>
          <div>
            <h1 className="text-2xl font-bold flex items-center gap-2">
              {monitor.name}
              {monitor.type === "http" && monitor.url && (
                <a
                  href={monitor.url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-slate-500 hover:text-sky-400 text-lg"
                  title={t("det.openSite")}
                >
                  ↗
                </a>
              )}
            </h1>
            <div className="text-sm text-slate-400">
              {describeMonitor(monitor)} {t("det.forTarget")}{" "}
              <span className="text-sky-400 break-all">
                {targetLabel(monitor)}
              </span>
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {isAdmin && (
            <button
              className="btn-secondary"
              onClick={testNotification}
              disabled={!linkedNotifs.length}
              title={
                linkedNotifs.length
                  ? t("det.testTitle")
                  : t("det.testTitleNone")
              }
            >
              {t("det.testNotification")}
            </button>
          )}
          {isAdmin && (
            <button
              className="btn-secondary"
              onClick={toggleEnabled}
              disabled={toggling}
            >
              {toggling ? "…" : paused ? t("common.resume") : t("common.pause")}
            </button>
          )}
          {isAdmin && (
            <button className="btn-secondary" onClick={() => setEditing(true)}>
              {t("common.edit")}
            </button>
          )}
          {isAdmin && (
            <button
              className="btn-secondary"
              onClick={checkNow}
              title={t("det.checkNowTitle")}
            >
              {t("det.checkNow")}
            </button>
          )}
        </div>
      </div>

      {loadError && <div className="text-sm text-app-down">{loadError}</div>}

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_360px] gap-6">
        {/* ===== Main column ===== */}
        <div className="space-y-6 min-w-0">
          {/* Top stat cards */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Card>
              <div className="text-xs text-slate-400">
                {t("det.currentStatus")}
              </div>
              <div className={`text-2xl font-bold mt-1 ${headlineColor}`}>
                {headline}
              </div>
              <div className="text-xs text-slate-500 mt-2">
                {paused
                  ? t("det.monitoringPaused")
                  : runSecs != null
                    ? t(
                        monitor.status === "down" ? "det.downFor" : "det.upFor",
                        { dur: formatUptime(runSecs) },
                      )
                    : "—"}
              </div>
            </Card>

            <Card>
              <div className="text-xs text-slate-400">{t("det.lastCheck")}</div>
              <div className="text-2xl font-bold mt-1">
                {checking ? (
                  <span className="text-sky-400">{t("common.checking")}</span>
                ) : (
                  timeAgo(monitor.lastCheckAt, now)
                )}
              </div>
              <div className="text-xs text-slate-500 mt-2">
                {t("det.checkedEvery", {
                  interval: humanInterval(monitor.intervalSec),
                })}
              </div>
            </Card>

            <Card>
              <div className="flex items-center justify-between">
                <span className="text-xs text-slate-400">
                  {t("det.last24")}
                </span>
                <span className="text-sm font-semibold">
                  {last24.pct.toFixed(2)}%
                </span>
              </div>
              <div className="flex items-end gap-[3px] mt-2 h-9">
                {last24.buckets.map((b, i) => (
                  <div
                    key={i}
                    className="flex-1 rounded-[2px]"
                    style={{
                      height: "100%",
                      backgroundColor: b ? STATUS_HEX[b] : "#1e293b",
                    }}
                    title={b ?? t("det.noData")}
                  />
                ))}
              </div>
              <div className="text-xs text-slate-500 mt-2">
                {t("det.incidentsCount", {
                  n: last24.failures,
                  s: last24.failures === 1 ? "" : "s",
                })}
                , {t("det.mDown", { n: last24.downMinutes })}
              </div>
            </Card>
          </div>

          {/* Uptime summary row */}
          <Card>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <UptimeStat
                label={t("det.last7days")}
                pct={pctFor(7)}
                history={uptime?.history}
                days={7}
              />
              <UptimeStat
                label={t("det.last30days")}
                pct={pctFor(30)}
                history={uptime?.history}
                days={30}
              />
              <UptimeStat
                label={t("det.last90days")}
                pct={pctFor(90)}
                history={uptime?.history}
                days={90}
              />
              <div>
                <div className="text-xs text-slate-400 mb-1">
                  {t("det.sloTarget")}
                </div>
                {monitor.sloTargetPct && monitor.sloTargetPct > 0 ? (
                  <>
                    <div
                      className={`text-xl font-bold ${
                        uptime && !uptime.sloMet
                          ? "text-app-down"
                          : "text-app-up"
                      }`}
                    >
                      {monitor.sloTargetPct.toFixed(2)}%
                    </div>
                    <div className="text-xs text-slate-500">
                      {uptime
                        ? uptime.sloMet
                          ? t("det.onTrack")
                          : t("det.belowTarget")
                        : "—"}
                    </div>
                  </>
                ) : (
                  <div className="text-xl font-bold text-slate-500">—</div>
                )}
              </div>
            </div>
          </Card>

          {/* Response time */}
          <Card>
            <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
              <CardTitle className="mb-0">{t("det.responseTime")}</CardTitle>
              <select
                className="input w-auto text-sm"
                value={hours}
                onChange={(e) => setHours(Number(e.target.value))}
              >
                <option value={1}>{t("det.window1h")}</option>
                <option value={24}>{t("det.window24h")}</option>
                <option value={168}>{t("det.window7d")}</option>
              </select>
            </div>
            {loading ? (
              <div className="text-slate-500 text-sm py-10 text-center">
                {t("common.loading")}
              </div>
            ) : !checksAsc.length ? (
              <div className="text-center py-10">
                <div className="text-slate-300 font-medium">
                  {t("det.noRespTitle")}
                </div>
                <div className="text-xs text-slate-500 mt-1">
                  {t("det.noRespHint")}
                </div>
              </div>
            ) : (
              <LatencyChart checks={checksAsc} />
            )}
            <div className="grid grid-cols-3 gap-4 mt-4 pt-4 border-t border-slate-800">
              <Metric
                label={t("det.average")}
                value={resp.avg ?? stats?.avgLatencyMs ?? null}
              />
              <Metric label={t("det.minimum")} value={resp.min} />
              <Metric label={t("det.maximum")} value={resp.max} />
            </div>
          </Card>

          {/* Latest incidents */}
          <IncidentList incidents={incidents} />

          {/* Outside-in domain audit: DNS + email security + headers + SSL */}
          {(monitor.type === "http" ||
            monitor.type === "cert" ||
            monitor.type === "dns") && <DomainIntelCard id={id} />}
        </div>

        {/* ===== Sidebar ===== */}
        <div className="space-y-6">
          <Card>
            <CardTitle>{t("det.domainSsl")}</CardTitle>
            {domainSslLoading ? (
              <div className="text-sm text-slate-500">{t("det.checking")}</div>
            ) : !domainSsl ? (
              <div className="text-sm text-slate-500">
                {t("det.notAvailable")}
              </div>
            ) : (
              <div className="space-y-3">
                <div className="flex items-center justify-between gap-3 border-b border-slate-800 pb-3">
                  <div className="min-w-0">
                    <div className="text-xs text-slate-500 mb-0.5">
                      {t("det.publicIp")}
                    </div>
                    <div className="font-mono text-sm text-slate-200 truncate">
                      {domainSsl.publicIp || "—"}
                    </div>
                  </div>
                  <div className="min-w-0 text-right">
                    <div className="text-xs text-slate-500 mb-0.5">
                      {t("det.rowLocalIp")}
                    </div>
                    <div className="font-mono text-sm text-slate-200 truncate">
                      {monitor.localIp || "—"}
                    </div>
                  </div>
                </div>
                <div>
                  <div className="text-xs text-slate-500 mb-1">
                    {t("det.sslValidUntil")}
                  </div>
                  {domainSsl.ssl.ok && domainSsl.ssl.notAfter ? (
                    <>
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-sm text-slate-200">
                          {new Date(
                            domainSsl.ssl.notAfter,
                          ).toLocaleDateString()}
                        </span>
                        <DaysBadge days={domainSsl.ssl.daysRemaining} />
                      </div>
                      {domainSsl.ssl.issuer && (
                        <div className="text-xs text-slate-500 mt-0.5">
                          {t("det.issuedBy", { issuer: domainSsl.ssl.issuer })}
                        </div>
                      )}
                    </>
                  ) : (
                    <div className="text-sm text-slate-500">
                      —{" "}
                      <span className="text-xs break-all">
                        {domainSsl.ssl.error}
                      </span>
                    </div>
                  )}
                </div>
                <div className="border-t border-slate-800 pt-3">
                  <div className="text-xs text-slate-500 mb-1">
                    {t("det.domainValidUntil")}
                    {domainSsl.domainName ? ` · ${domainSsl.domainName}` : ""}
                  </div>
                  {domainSsl.domain.ok && domainSsl.domain.expiryDate ? (
                    <>
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-sm text-slate-200">
                          {new Date(
                            domainSsl.domain.expiryDate,
                          ).toLocaleDateString()}
                        </span>
                        <DaysBadge days={domainSsl.domain.daysRemaining ?? 0} />
                      </div>
                      {domainSsl.domain.registrar && (
                        <div className="text-xs text-slate-500 mt-0.5">
                          {domainSsl.domain.registrar}
                        </div>
                      )}
                    </>
                  ) : (
                    <div className="text-sm text-slate-500">
                      —{" "}
                      <span className="text-xs break-all">
                        {domainSsl.domain.error}
                      </span>
                    </div>
                  )}
                </div>
              </div>
            )}
          </Card>

          {/* Multi-country ping (Asia) — real probes via Globalping, auto-run */}
          {monitor.type !== "push" && <MultiPingCard monitorId={id} />}

          {/* AI root-cause analysis — sidebar, below Domain & SSL */}
          <Card>
            <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
              <CardTitle className="mb-0 flex items-center gap-2">
                {t("det.aiTitle")}
                <span className="text-[10px] px-1.5 py-0.5 rounded bg-sky-500/15 text-sky-400 font-semibold">
                  {aiStatus?.enabled
                    ? aiStatus.providerName || aiStatus.model || "AI"
                    : t("det.aiOff")}
                </span>
              </CardTitle>
              {isAdmin && (
                <button
                  className="btn-secondary"
                  onClick={reanalyze}
                  disabled={analyzing}
                >
                  {analyzing ? t("det.aiAnalyzing") : t("det.aiReanalyze")}
                </button>
              )}
            </div>
            {analyzing && (
              <div className="text-xs text-slate-500 mb-2 animate-pulse">
                {t("det.aiProgress")}
              </div>
            )}
            {analyzeError && (
              <div className="text-xs text-app-down mb-2">{analyzeError}</div>
            )}
            {!analysesLoaded ? (
              <div className="text-sm text-slate-500">
                {t("common.loading")}
              </div>
            ) : analyses.length === 0 ? (
              aiStatus && !aiStatus.enabled ? (
                <div className="text-sm text-slate-500">
                  {t("det.aiNotConfigured")}{" "}
                  {isAdmin && (
                    <Link
                      to="/settings?tab=ai"
                      className="text-sky-400 hover:underline"
                    >
                      {t("det.aiConfigure")}
                    </Link>
                  )}
                </div>
              ) : (
                <div className="text-sm text-slate-500">
                  {t("det.aiEmpty")}{" "}
                  <span className="text-slate-300">{t("det.aiReanalyze")}</span>{" "}
                  {t("det.aiEmptyEnd")}
                </div>
              )
            ) : (
              <div className="space-y-3">
                <AnalysisBody a={analyses[0]} now={now} />
                {analyses.length > 1 && (
                  <details className="text-sm">
                    <summary className="cursor-pointer text-slate-500 hover:text-slate-300 text-xs">
                      {t("det.aiHistory", { n: analyses.length - 1 })}
                    </summary>
                    <div className="mt-2 space-y-2">
                      {analyses.slice(1).map((a) => (
                        <div
                          key={a.id}
                          className="border border-slate-800 rounded-lg p-3"
                        >
                          <div className="text-xs text-slate-500 mb-1">
                            {new Date(a.createdAt).toLocaleString()} · {a.model}
                          </div>
                          <div className="text-xs text-slate-400 line-clamp-3 whitespace-pre-wrap">
                            {a.analysis}
                          </div>
                        </div>
                      ))}
                    </div>
                  </details>
                )}
              </div>
            )}
          </Card>

          <Card>
            <CardTitle>{t("det.details")}</CardTitle>
            <dl className="space-y-2 text-sm">
              {monitor.type === "http" && monitor.url && (
                <Row label={t("det.rowDomain")}>
                  <a
                    href={monitor.url}
                    target="_blank"
                    rel="noreferrer"
                    className="text-sky-400 break-all hover:underline"
                  >
                    {hostFromUrl(monitor.url)}
                  </a>
                </Row>
              )}
              <Row label={t("det.rowTarget")}>
                <span className="break-all text-slate-300">
                  {targetLabel(monitor)}
                </span>
              </Row>
              <Row label={t("det.rowType")}>
                <span className="uppercase text-xs">{monitor.type}</span>
              </Row>
              <Row label={t("det.rowInterval")}>
                {humanInterval(monitor.intervalSec)}
              </Row>
              <Row label={t("det.rowTimeout")}>{monitor.timeoutSec}s</Row>
              {monitor.type === "http" && (
                <Row label={t("det.rowSsl")}>
                  {monitor.ignoreTls ? (
                    <span className="text-amber-400">
                      {t("det.sslIgnored")}
                    </span>
                  ) : (
                    <span className="text-app-up">{t("det.sslStrict")}</span>
                  )}
                </Row>
              )}
              {domainSsl?.publicIp && (
                <Row label={t("det.publicIp")}>
                  <span className="font-mono text-xs text-slate-300">
                    {domainSsl.publicIp}
                  </span>
                </Row>
              )}
              {monitor.localIp && (
                <Row label={t("det.rowLocalIp")}>
                  <span className="font-mono text-xs text-slate-300">
                    {monitor.localIp}
                  </span>
                </Row>
              )}
              {monitor.createdBy && (
                <Row label={t("det.rowAddedBy")}>{monitor.createdBy}</Row>
              )}
            </dl>
            {monitor.tags && monitor.tags.length > 0 && (
              <div className="flex flex-wrap gap-1 mt-3">
                {monitor.tags.map((tag) => (
                  <span
                    key={tag}
                    className="text-xs px-2 py-0.5 rounded-full bg-slate-800 text-slate-300"
                  >
                    #{tag}
                  </span>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardTitle>{t("det.notifiedTitle")}</CardTitle>
            {linkedNotifs.length === 0 ? (
              <div className="text-sm text-slate-500">
                {t("det.notifiedNone")}
              </div>
            ) : (
              <ul className="space-y-1.5 text-sm">
                {linkedNotifs.map((n) => (
                  <li key={n.id} className="flex items-center gap-2">
                    <span className="w-2 h-2 rounded-full bg-sky-400" />
                    <span className="text-slate-200">{n.name}</span>
                    <span className="text-xs text-slate-500">({n.type})</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>

      {editing && isAdmin && (
        <Modal
          title={t("mon.editTitle")}
          onClose={() => setEditing(false)}
          wide
        >
          <MonitorForm
            initial={monitor}
            notifications={notifications}
            escalations={escalations}
            saving={saving}
            onSave={saveEdit}
            onCancel={() => setEditing(false)}
          />
          {editError && (
            <div className="text-app-down text-sm mt-3">{editError}</div>
          )}
        </Modal>
      )}
    </div>
  );
}

function Row({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="text-slate-500">{label}</dt>
      <dd className="text-right text-slate-200">{children}</dd>
    </div>
  );
}

function DaysBadge({ days }: { days: number }) {
  const color =
    days < 0
      ? "bg-app-down/15 text-app-down"
      : days <= 14
        ? "bg-amber-500/15 text-amber-400"
        : "bg-app-up/15 text-app-up";
  const label =
    days < 0
      ? t("det.expiredAgo", { n: -days })
      : t("det.daysLeft", { n: days });
  return (
    <span className={`text-xs px-2 py-0.5 rounded-full shrink-0 ${color}`}>
      {label}
    </span>
  );
}

// AnalysisBody renders one AI analysis. The model output is plain-ish markdown
// (bold markers, bullet lists); we render it pre-wrapped and lightly convert
// **bold** and "- " bullets so it reads well without a markdown dependency.
function AnalysisBody({ a, now }: { a: MonitorAnalysis; now: number }) {
  return (
    <div className="bg-slate-900/40 border border-slate-800 rounded-lg p-4">
      <div className="flex items-center justify-between gap-2 text-xs text-slate-500 mb-2">
        <span>
          {timeAgo(a.createdAt, now)} · {new Date(a.createdAt).toLocaleString()}
        </span>
        <span className="flex items-center gap-2">
          <span
            className={`px-1.5 py-0.5 rounded-full ${
              a.status === "down"
                ? "bg-app-down/15 text-app-down"
                : "bg-slate-800 text-slate-400"
            }`}
          >
            {a.status === "down"
              ? t("status.down")
              : a.status === "up"
                ? t("status.up")
                : a.status}
          </span>
          <span className="text-slate-500">{a.model}</span>
        </span>
      </div>
      <div className="text-sm text-slate-300 space-y-1">
        {a.analysis.split("\n").map((line, i) => {
          const t = line.trim();
          if (!t) return null;
          const bullet = t.startsWith("- ") || t.startsWith("• ");
          const text = bullet ? t.slice(2) : t;
          return (
            <div key={i} className={bullet ? "flex gap-2" : ""}>
              {bullet && <span className="text-slate-600 shrink-0">•</span>}
              <span className="min-w-0">{renderInline(text)}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// renderInline converts **bold** segments to <strong>; everything else stays text.
function renderInline(s: string): React.ReactNode {
  const parts = s.split(/\*\*(.+?)\*\*/g);
  if (parts.length === 1) return s;
  return parts.map((p, i) =>
    i % 2 === 1 ? (
      <strong key={i} className="text-slate-100">
        {p}
      </strong>
    ) : (
      <span key={i}>{p}</span>
    ),
  );
}

function Metric({ label, value }: { label: string; value: number | null }) {
  return (
    <div>
      <div className="text-lg font-semibold">
        {value != null ? `${Math.round(value)} ms` : "N/A ms"}
      </div>
      <div className="text-xs text-slate-500">{label}</div>
    </div>
  );
}

function UptimeStat({
  label,
  pct,
  history,
  days,
}: {
  label: string;
  pct: number | null;
  history?: MonitorUptimeStat["history"];
  days: number;
}) {
  const recent = (history ?? []).slice(-days);
  return (
    <div>
      <div className="text-xs text-slate-400 mb-1">{label}</div>
      <div
        className={`text-xl font-bold ${pct == null ? "text-slate-500" : pct >= 99.9 ? "text-app-up" : pct >= 99 ? "text-amber-400" : "text-app-down"}`}
      >
        {pct == null ? "— — —%" : `${pct.toFixed(pct >= 99.95 ? 2 : 2)}%`}
      </div>
      <div className="flex items-end gap-[2px] mt-1.5 h-6">
        {recent.map((p, i) => (
          <div
            key={i}
            className="flex-1 rounded-[1px]"
            style={{
              height: "100%",
              backgroundColor:
                p.pct < 0
                  ? "#1e293b"
                  : p.pct >= 99.9
                    ? "#22c55e"
                    : p.pct >= 99
                      ? "#4ade80"
                      : p.pct >= 90
                        ? "#eab308"
                        : "#ef4444",
            }}
            title={`${p.date}: ${p.pct < 0 ? "no data" : p.pct.toFixed(2) + "%"}`}
          />
        ))}
      </div>
    </div>
  );
}
