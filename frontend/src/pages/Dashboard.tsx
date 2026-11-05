import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import MonitorCard from "../components/MonitorCard";
import { useMonitorIds, useMonitorStats } from "../hooks/useMonitor";
import { api } from "../lib/api";
import { monitorStore } from "../lib/monitorStore";
import { t, useI18n } from "../lib/i18n";
import type { CheckResult, MonitorStatus } from "../lib/types";

type StatusFilter = MonitorStatus | "all";

const STATUS_HEX: Record<string, string> = {
  up: "#22c55e",
  degraded: "#eab308",
  down: "#ef4444",
  pending: "#94a3b8",
  unknown: "#94a3b8",
};

// Sort weight — lower floats to the top, so Down and Degraded surface first.
const SEVERITY: Record<string, number> = {
  down: 0,
  degraded: 1,
  pending: 2,
  unknown: 3,
  up: 4,
};

function timeAgo(iso?: string): string {
  if (!iso) return "—";
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return t("timeago.s", { n: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("timeago.m", { n: min });
  const hr = Math.floor(min / 60);
  if (hr < 24) return t("timeago.h", { n: hr });
  return t("timeago.d", { n: Math.floor(hr / 24) });
}

// Status values are stored in English; show them localized in the UI.
function statusLabel(s: string): string {
  const known = ["up", "down", "degraded", "pending", "unknown"];
  return known.includes(s) ? t(`status.${s}`) : s;
}

export default function Dashboard() {
  const { lang } = useI18n();
  const ids = useMonitorIds();
  const stats = useMonitorStats();
  const [recent, setRecent] = useState<Record<string, CheckResult[]>>({});
  const [filter, setFilter] = useState<StatusFilter>("all");

  const idsKey = useMemo(() => ids.join(","), [ids]);

  // Fetch recent checks for sparkline bars — fires only when the list of
  // monitors changes (structural), NOT on per-node status pushes.
  useEffect(() => {
    if (!idsKey) return;
    let cancelled = false;
    (async () => {
      const list = idsKey.split(",");
      const results = await Promise.all(
        list.map((id) =>
          api.recentChecks(id, 24).catch(() => [] as CheckResult[]),
        ),
      );
      if (cancelled) return;
      const map: Record<string, CheckResult[]> = {};
      list.forEach((id, i) => (map[id] = results[i]));
      setRecent(map);
    })();
    return () => {
      cancelled = true;
    };
  }, [idsKey]);

  // Clicking a stat card filters the list; clicking the active one resets to all.
  function toggleFilter(f: StatusFilter) {
    setFilter((cur) => (cur === f ? "all" : f));
  }

  // `stats` re-renders this page on status flips, so reading the current
  // status imperatively from the store here is safe and keeps subscriptions coarse.
  // Group by severity first — anything still Down (then Degraded) stays on top
  // no matter how old the outage is — and within each status group order by
  // most-recent STATUS CHANGE, so a service that just flipped (newly down OR
  // just recovered) floats to the top of its group instead of keeping the
  // original creation order.
  // Paused monitors (enabled === false) are hidden from the dashboard entirely
  // — they are managed on the Monitors page. Status counts exclude them too, so
  // a paused node never surfaces as DOWN/UP in the stat cards or uptime bar.
  const activeIds = useMemo(
    () => ids.filter((id) => monitorStore.getById(id)?.enabled !== false),
    [ids, stats],
  );

  const counts = useMemo(() => {
    let up = 0;
    let down = 0;
    let degraded = 0;
    for (const id of activeIds) {
      const s = monitorStore.getById(id)?.status;
      if (s === "up") up++;
      else if (s === "down") down++;
      else if (s === "degraded") degraded++;
    }
    return { total: activeIds.length, up, down, degraded };
  }, [activeIds, stats]);

  const visibleIds = useMemo(() => {
    const base =
      filter === "all"
        ? activeIds
        : activeIds.filter((id) => monitorStore.getById(id)?.status === filter);
    // last status change wins; fall back to last check, then creation time
    const since = (id: string): number => {
      const m = monitorStore.getById(id);
      const iso = m?.stateSince ?? m?.lastCheckAt ?? m?.createdAt;
      return iso ? +new Date(iso) : 0;
    };
    const sev = (id: string): number =>
      SEVERITY[monitorStore.getById(id)?.status ?? "unknown"] ?? 5;
    return [...base].sort((a, b) => {
      const s = sev(a) - sev(b); // DOWN group first, then DEGRADED, …
      if (s !== 0) return s;
      return since(b) - since(a); // newest status change first within the group
    });
  }, [activeIds, stats, filter]);

  // Flatten the last few checks of every monitor into a chronological feed.
  const activity = useMemo(() => {
    const rows: {
      id: string;
      name: string;
      status: MonitorStatus;
      at: string;
    }[] = [];
    for (const [id, checks] of Object.entries(recent)) {
      const name = monitorStore.getById(id)?.name || "Monitor";
      for (const c of checks.slice(0, 6)) {
        rows.push({ id, name, status: c.status, at: c.createdAt });
      }
    }
    rows.sort((a, b) => +new Date(b.at) - +new Date(a.at));
    return rows.slice(0, 8);
  }, [recent, ids]);

  // Top 5 slowest by last latency — recomputed on status flips / add-remove.
  const slowest = useMemo(() => {
    return monitorStore
      .getAll()
      .filter((m) => m.lastLatencyMs != null)
      .sort((a, b) => (b.lastLatencyMs || 0) - (a.lastLatencyMs || 0))
      .slice(0, 5);
  }, [ids, stats]);

  const total = counts.total || 0;
  const uptimePct = total ? Math.round((counts.up / total) * 100) : 0;
  const seg = (n: number) => (total ? `${(n / total) * 100}%` : "0%");
  void lang; // re-render when the UI language changes

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{t("dash.title")}</h1>
        <span className="text-sm text-slate-400">{t("dash.subtitle")}</span>
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_320px] gap-6 items-start">
        {/* Main column */}
        <div className="space-y-6 min-w-0">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <Stat
              label={t("dash.statMonitors")}
              value={counts.total}
              active={filter === "all"}
              onClick={() => toggleFilter("all")}
            />
            <Stat
              label={t("dash.statUp")}
              value={counts.up}
              color="#22c55e"
              active={filter === "up"}
              onClick={() => toggleFilter("up")}
            />
            <Stat
              label={t("dash.statDegraded")}
              value={counts.degraded}
              color="#eab308"
              active={filter === "degraded"}
              onClick={() => toggleFilter("degraded")}
            />
            <Stat
              label={t("dash.statDown")}
              value={counts.down}
              color="#ef4444"
              active={filter === "down"}
              onClick={() => toggleFilter("down")}
            />
          </div>

          <div className="grid grid-cols-1 gap-3">
            {ids.length === 0 && (
              <div className="text-slate-400 text-sm">
                {t("dash.noMonitors")}{" "}
                <Link className="underline" to="/monitors">
                  {t("dash.createOne")}
                </Link>
                .
              </div>
            )}
            {activeIds.length > 0 && visibleIds.length === 0 && (
              <div className="text-slate-400 text-sm">
                {t("dash.noStatus", { status: statusLabel(filter) })}{" "}
                <button
                  className="underline text-slate-300"
                  onClick={() => setFilter("all")}
                >
                  {t("dash.showAll")}
                </button>
                .
              </div>
            )}
            {visibleIds.map((id) => (
              <Link key={id} to={`/monitors/${id}`} className="block">
                <MonitorCard id={id} recentChecks={recent[id]} />
              </Link>
            ))}
          </div>
        </div>

        {/* Sidebar — fills the empty right column on wide screens */}
        <aside className="space-y-4 xl:sticky xl:top-6">
          <Panel title={t("dash.panelOverview")}>
            <div className="flex items-end justify-between mb-3">
              <div>
                <div className="text-3xl font-bold leading-none">
                  {uptimePct}%
                </div>
                <div className="text-xs text-slate-400 mt-1">
                  {t("dash.availabilityNow")}
                </div>
              </div>
              <div className="text-right text-xs text-slate-400 space-y-0.5">
                <div>{t("dash.countUp", { n: counts.up })}</div>
                <div>{t("dash.countDegraded", { n: counts.degraded })}</div>
                <div>{t("dash.countDown", { n: counts.down })}</div>
              </div>
            </div>
            <div className="flex h-2 rounded-full overflow-hidden bg-slate-800">
              <span style={{ width: seg(counts.up), background: "#22c55e" }} />
              <span
                style={{ width: seg(counts.degraded), background: "#eab308" }}
              />
              <span
                style={{ width: seg(counts.down), background: "#ef4444" }}
              />
            </div>
          </Panel>

          <Panel title={t("dash.panelActivity")}>
            {activity.length === 0 ? (
              <div className="text-sm text-slate-500">
                {t("dash.noActivity")}
              </div>
            ) : (
              <div className="space-y-0.5">
                {activity.map((r, i) => (
                  <Link
                    key={`${r.id}-${i}`}
                    to={`/monitors/${r.id}`}
                    className="flex items-center gap-2 py-1.5 px-1 -mx-1 rounded hover:bg-slate-800/50"
                  >
                    <span
                      className="w-0 h-0 shrink-0 border-x-[4px] border-x-transparent"
                      style={
                        r.status === "up"
                          ? { borderBottom: `7px solid ${STATUS_HEX.up}` }
                          : r.status === "down"
                            ? { borderTop: `7px solid ${STATUS_HEX.down}` }
                            : {
                                width: 8,
                                height: 8,
                                borderRadius: "50%",
                                background: STATUS_HEX[r.status],
                              }
                      }
                    />
                    <span className="text-sm truncate flex-1">{r.name}</span>
                    <span className="text-[11px] text-slate-500 whitespace-nowrap">
                      {timeAgo(r.at)}
                    </span>
                  </Link>
                ))}
              </div>
            )}
          </Panel>

          <Panel title={t("dash.panelSlowest")}>
            {slowest.length === 0 ? (
              <div className="text-sm text-slate-500">
                {t("dash.noLatency")}
              </div>
            ) : (
              <div className="space-y-0.5">
                {slowest.map((m) => (
                  <Link
                    key={m.id}
                    to={`/monitors/${m.id}`}
                    className="flex items-center gap-2 py-1.5 px-1 -mx-1 rounded hover:bg-slate-800/50"
                  >
                    <span
                      className="w-0 h-0 shrink-0 border-x-[4px] border-x-transparent"
                      style={
                        m.status === "up"
                          ? { borderBottom: `7px solid ${STATUS_HEX.up}` }
                          : m.status === "down"
                            ? { borderTop: `7px solid ${STATUS_HEX.down}` }
                            : {
                                width: 8,
                                height: 8,
                                borderRadius: "50%",
                                background: STATUS_HEX[m.status],
                              }
                      }
                    />
                    <span className="text-sm truncate flex-1">{m.name}</span>
                    <span className="text-xs text-slate-400 tabular-nums">
                      {Math.round(m.lastLatencyMs || 0)} ms
                    </span>
                  </Link>
                ))}
              </div>
            )}
          </Panel>
        </aside>
      </div>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="bg-app-panel border border-slate-800 rounded-lg p-4">
      <h2 className="text-xs uppercase text-slate-400 mb-3">{title}</h2>
      {children}
    </section>
  );
}

function Stat({
  label,
  value,
  color,
  active,
  onClick,
}: {
  label: string;
  value: number;
  color?: string;
  active?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`w-full text-left bg-app-panel border rounded-lg p-4 transition-colors ${
        active
          ? "border-sky-500 ring-1 ring-sky-500/40"
          : "border-slate-800 hover:border-slate-600"
      }`}
      title={active ? t("dash.filterAll") : t("dash.filterOnly", { label })}
    >
      <div className="text-xs text-slate-400 uppercase">{label}</div>
      <div
        className="text-2xl font-semibold mt-1"
        style={color ? { color } : undefined}
      >
        {value}
      </div>
    </button>
  );
}
