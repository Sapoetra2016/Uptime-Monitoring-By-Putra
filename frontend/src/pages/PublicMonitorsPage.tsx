import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import StatusRing from "../components/StatusRing";
import { UptimeBars, pctLabel } from "../components/UptimeBars";
import { api, getToken } from "../lib/api";
import { getPrefs } from "../lib/prefs";
import { t, useI18n } from "../lib/i18n";
import type { MonitorUptimeStat, PublicMonitor } from "../lib/types";

interface Payload {
  monitors: PublicMonitor[];
  stats: MonitorUptimeStat[];
  uptime: number;
  days: number;
}

function timeAgo(iso?: string): string {
  if (!iso) return t("common.never");
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return t("timeago.s", { n: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("timeago.m", { n: min });
  const hr = Math.floor(min / 60);
  if (hr < 24) return t("timeago.h", { n: hr });
  return t("timeago.d", { n: Math.floor(hr / 24) });
}

export default function PublicMonitorsPage() {
  const { lang } = useI18n();
  void lang; // re-render when language changes (t() reads the live store)
  const [data, setData] = useState<Payload | null>(null);
  const [brand, setBrand] = useState<{ title: string; footer: string } | null>(null);
  const [error, setError] = useState("");
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [days, setDays] = useState(() => getPrefs().uptimeWindowDays);
  const loggedIn = !!getToken();

  useEffect(() => {
    api.publicSettings().then(setBrand).catch(() => {});
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const res = await api.publicMonitors(days);
        if (cancelled) return;
        setData(res);
        setLastUpdated(new Date());
        setError("");
      } catch (e: any) {
        if (!cancelled) setError(e.message || t("pub.loadFailed"));
      }
    }
    load();
    const interval = setInterval(load, 30_000);
    const onVisible = () => document.visibilityState === "visible" && load();
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      cancelled = true;
      clearInterval(interval);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [days]);

  const statsById = useMemo(() => {
    const map: Record<string, MonitorUptimeStat> = {};
    for (const s of data?.stats ?? []) map[s.monitorId] = s;
    return map;
  }, [data]);

  const monitors = data?.monitors ?? [];
  const shown = getPrefs().hidePaused ? monitors.filter((m) => m.enabled) : monitors;
  const active = shown.filter((m) => m.enabled);
  const downCount = active.filter((m) => m.status === "down").length;
  const degradedCount = active.filter((m) => m.status === "degraded").length;
  const allOperational = downCount === 0;

  return (
    <div className="min-h-screen">
      <nav className="flex items-center gap-4 px-6 py-4 bg-app-panel border-b border-slate-800">
        <span className="font-bold text-lg">{t("pub.brandFallback")}</span>
        <span className="text-sm text-slate-400">{t("pub.status")}</span>
        <div className="ml-auto">
          {loggedIn ? (
            <Link to="/" className="btn-secondary text-sm">
              {t("nav.dashboard")}
            </Link>
          ) : (
            <Link to="/login" className="btn-primary text-sm">
              {t("pub.manageLogin")}
            </Link>
          )}
        </div>
      </nav>

      <div className="max-w-3xl mx-auto px-4 py-10">
        <header className="mb-6">
          <h1 className="text-2xl font-bold">{brand?.title || t("pub.systemStatus")}</h1>
          {lastUpdated && (
            <p className="text-xs text-slate-500 mt-2">
              {t("pub.autoUpdate", { time: lastUpdated.toLocaleTimeString() })}
            </p>
          )}
        </header>

        {error && <div className="text-app-down mb-4">{error}</div>}

        {data && (
          <>
            <div
              className={`rounded-lg p-4 mb-6 ${
                allOperational ? "bg-app-up/20 text-green-300" : "bg-app-down/20 text-red-300"
              }`}
            >
              <div className="font-semibold">
                {allOperational
                  ? t("pub.allOperational")
                  : t("pub.someDown", {
                      n: downCount,
                      s: downCount > 1 ? "s" : "",
                      d: degradedCount ? t("pub.degradedPart", { n: degradedCount }) : "",
                    })}
              </div>
              <div className="text-xs mt-1 opacity-80">
                {t("pub.uptimeLine", { pct: pctLabel(data.uptime), days: data.days, n: active.length })}
              </div>
            </div>

            <div className="flex items-center justify-between mb-2">
              <h2 className="text-sm uppercase text-slate-400">{t("pub.services")}</h2>
              <div className="flex gap-1 text-xs">
                {[7, 30, 90].map((d) => (
                  <button
                    key={d}
                    onClick={() => setDays(d)}
                    className={`px-2 py-0.5 rounded ${
                      days === d ? "bg-slate-700 text-white" : "bg-slate-800 text-slate-400"
                    }`}
                  >
                    {t("pub.daysShort", { n: d })}
                  </button>
                ))}
              </div>
            </div>

            {shown.length === 0 && !error && (
              <div className="text-slate-500 py-6 text-center">{t("pub.noServices")}</div>
            )}

            <div className="space-y-2">
              {shown.map((m) => {
                const st = statsById[m.id];
                return (
                  <div key={m.id} className="bg-app-panel border border-slate-800 rounded-lg p-4">
                    <div className="flex items-center gap-3">
                      <StatusRing status={m.enabled ? m.status : "unknown"} size={14} />
                      <span className="flex-1 font-medium">{m.name}</span>
                      {!m.enabled && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-800 text-amber-400 uppercase">
                          {t("common.paused")}
                        </span>
                      )}
                      {st && st.sloTargetPct > 0 && m.enabled && (
                        <span
                          className={`text-[10px] px-1.5 py-0.5 rounded uppercase ${
                            st.sloMet ? "bg-green-500/15 text-green-400" : "bg-red-500/15 text-red-400"
                          }`}
                          title={t("pub.sloTargetTitle", { pct: st.sloTargetPct })}
                        >
                          {st.sloMet ? t("pub.sloMet") : t("pub.sloMissed")}
                        </span>
                      )}
                      {m.lastLatencyMs != null && (
                        <span className="text-xs text-slate-400">{Math.round(m.lastLatencyMs)}ms</span>
                      )}
                      <span className="text-sm w-14 text-right tabular-nums">
                        {m.enabled && st ? pctLabel(st.uptimePct) : "—"}
                      </span>
                    </div>
                    <div className="flex items-center justify-between mt-1">
                      <span className="text-xs text-slate-500 truncate">
                        {m.type.toUpperCase()}
                        {m.statusReason && m.status === "down" ? ` · ${m.statusReason}` : ""}
                      </span>
                      <span className="text-[11px] text-slate-600 ml-2 whitespace-nowrap">
                        {timeAgo(m.lastCheckAt)}
                      </span>
                    </div>
                    {m.enabled && st && <UptimeBars history={st.history} />}
                  </div>
                );
              })}
            </div>
          </>
        )}
      </div>

      <footer className="text-center text-xs text-slate-600 py-6 border-t border-slate-800">
        {brand?.footer && <div className="mb-1">{brand.footer}</div>}
        © {new Date().getFullYear()} Putra_aja
      </footer>
    </div>
  );
}
