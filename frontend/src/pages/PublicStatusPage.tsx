import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import StatusRing from "../components/StatusRing";
import { UptimeBars, pctLabel } from "../components/UptimeBars";
import { api } from "../lib/api";
import { getPrefs } from "../lib/prefs";
import { t, useI18n } from "../lib/i18n";
import type {
  Incident,
  Monitor,
  MonitorUptimeStat,
  StatusPage,
} from "../lib/types";

interface Payload {
  page: StatusPage;
  monitors: Monitor[];
  incidents: Incident[];
  stats: MonitorUptimeStat[];
  uptime: number;
  days: number;
}

export default function PublicStatusPage() {
  const { lang } = useI18n();
  void lang; // re-render when language changes (t() reads the live store)
  const { slug = "" } = useParams();
  const [data, setData] = useState<Payload | null>(null);
  const [error, setError] = useState("");
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [days, setDays] = useState(() => getPrefs().uptimeWindowDays);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const res = await api.publicStatusPage(slug, days);
        if (cancelled) return;
        setData(res);
        setLastUpdated(new Date());
      } catch (e: any) {
        if (!cancelled) setError(e.message || t("psp.notFound"));
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
  }, [slug, days]);

  const statsById = useMemo(() => {
    const map: Record<string, MonitorUptimeStat> = {};
    for (const s of data?.stats ?? []) map[s.monitorId] = s;
    return map;
  }, [data]);

  const overallUp = (data?.monitors ?? []).every(
    (m) => m.status === "up" || m.status === "pending",
  );
  const pageUptime = data?.uptime ?? 100;

  return (
    <div className="min-h-screen max-w-3xl mx-auto px-4 py-10">
      <header className="mb-8">
        <h1 className="text-2xl font-bold">
          {data?.page?.title ?? t("psp.titleFallback")}
        </h1>
        {data?.page?.description && (
          <p className="text-slate-400 mt-1">{data.page.description}</p>
        )}
        {lastUpdated && (
          <p className="text-xs text-slate-500 mt-2">
            {t("psp.autoUpdate", { time: lastUpdated.toLocaleTimeString() })}
          </p>
        )}
      </header>

      {error && <div className="text-app-down">{error}</div>}

      {data && (
        <>
          <div
            className={`rounded-lg p-4 mb-6 ${
              overallUp
                ? "bg-app-up/20 text-green-300"
                : "bg-app-down/20 text-red-300"
            }`}
          >
            <div className="font-semibold">
              {overallUp ? t("psp.allOperational") : t("psp.issues")}
            </div>
            <div className="text-xs mt-1 opacity-80">
              {t("psp.uptimeLine", {
                pct: pctLabel(pageUptime),
                days: data.days,
              })}
            </div>
          </div>

          {(data.incidents?.length ?? 0) > 0 && (
            <div className="mb-6 space-y-2">
              <h2 className="text-sm uppercase text-slate-400">
                {t("psp.activeIncidents")}
              </h2>
              {(data.incidents ?? []).map((i) => (
                <div
                  key={i.id}
                  className="bg-app-panel border border-slate-800 rounded p-3 text-sm"
                >
                  <span className="font-medium">{i.headline}</span>
                  <span className="text-slate-400 ml-2">({i.status})</span>
                  {i.body && (
                    <div className="text-slate-400 mt-1">{i.body}</div>
                  )}
                </div>
              ))}
            </div>
          )}

          <div className="flex items-center justify-between mb-2">
            <h2 className="text-sm uppercase text-slate-400">
              {t("pub.services")}
            </h2>
            <div className="flex gap-1 text-xs">
              {[7, 30, 90].map((d) => (
                <button
                  key={d}
                  onClick={() => setDays(d)}
                  className={`px-2 py-0.5 rounded ${
                    days === d
                      ? "bg-slate-700 text-white"
                      : "bg-slate-800 text-slate-400"
                  }`}
                >
                  {t("pub.daysShort", { n: d })}
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-2">
            {(data.monitors ?? []).map((m) => {
              const st = statsById[m.id];
              return (
                <div
                  key={m.id}
                  className="bg-app-panel border border-slate-800 rounded-lg p-4"
                >
                  <div className="flex items-center gap-3">
                    <StatusRing status={m.status} size={14} />
                    <span className="flex-1 font-medium">{m.name}</span>
                    {st && st.sloTargetPct > 0 && (
                      <span
                        className={`text-[10px] px-1.5 py-0.5 rounded uppercase ${
                          st.sloMet
                            ? "bg-green-500/15 text-green-400"
                            : "bg-red-500/15 text-red-400"
                        }`}
                        title={t("pub.sloTargetTitle", {
                          pct: st.sloTargetPct.toFixed(2),
                        })}
                      >
                        {st.sloMet ? t("pub.sloMet") : t("pub.sloMissed")}
                      </span>
                    )}
                    {m.lastLatencyMs != null && (
                      <span className="text-xs text-slate-400">
                        {Math.round(m.lastLatencyMs)}ms
                      </span>
                    )}
                    <span className="text-sm w-14 text-right tabular-nums">
                      {st ? pctLabel(st.uptimePct) : "—"}
                    </span>
                  </div>
                  {st && <UptimeBars history={st.history} />}
                </div>
              );
            })}
          </div>

          <footer className="text-xs text-slate-600 mt-8 text-center">
            © {new Date().getFullYear()} Putra_aja
          </footer>
        </>
      )}
    </div>
  );
}
