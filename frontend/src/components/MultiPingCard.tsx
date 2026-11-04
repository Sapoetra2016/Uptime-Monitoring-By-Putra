import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { MultiPingResult } from "../lib/types";

// Flag emoji per negara Asia yang ada di shortlist backend. Dipakai escape
// \u{...} supaya tidak rusak oleh encoding file/editor.
const FLAGS: Record<string, string> = {
  ID: "\u{1F1EE}\u{1F1E9}",
  SG: "\u{1F1F8}\u{1F1EC}",
  MY: "\u{1F1F2}\u{1F1FE}",
  TH: "\u{1F1F9}\u{1F1ED}",
  VN: "\u{1F1FB}\u{1F1F3}",
  PH: "\u{1F1F5}\u{1F1ED}",
  JP: "\u{1F1EF}\u{1F1F5}",
  KR: "\u{1F1F0}\u{1F1F7}",
  TW: "\u{1F1F9}\u{1F1FC}",
  HK: "\u{1F1ED}\u{1F1F0}",
  IN: "\u{1F1EE}\u{1F1F3}",
};

function fmtMs(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "—";
  return v < 10 ? `${v.toFixed(1)} ms` : `${Math.round(v)} ms`;
}

/**
 * Multi-country ping (Asia) — runs a real ping against the monitor's host
 * from one Globalping probe per country and shows which location had the
 * lowest latency. Auto-runs when opened (and when switching monitor); the
 * user can always re-run it manually. Compact layout for the right sidebar.
 */
export default function MultiPingCard({ monitorId }: { monitorId: string }) {
  const { t: tr } = useI18n();
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState("");
  const [results, setResults] = useState<MultiPingResult[] | null>(null);
  const [error, setError] = useState("");
  const cancelled = useRef(false);

  const run = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.multiPing(monitorId);
      if (cancelled.current) return;
      setTarget(res.target);
      setResults(res.results);
    } catch (e: any) {
      if (!cancelled.current) setError(e?.message || tr("mp.failed"));
    } finally {
      if (!cancelled.current) setBusy(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [monitorId]);

  // Auto-run on mount and whenever the monitor changes — no manual click.
  useEffect(() => {
    cancelled.current = false;
    setResults(null);
    setError("");
    setTarget("");
    void run();
    return () => {
      cancelled.current = true;
    };
  }, [run]);

  const countryName = (cc: string) => {
    const key = `mp.c.${cc}`;
    const name = tr(key);
    return name === key ? cc : name;
  };

  return (
    <div className="bg-app-panel border border-slate-800 rounded-lg p-4">
      <div className="flex items-center justify-between gap-2 mb-1">
        <h2 className="text-sm font-semibold text-slate-300">{tr("mp.title")}</h2>
        <button
          onClick={run}
          disabled={busy}
          className="text-xs text-slate-400 hover:text-slate-200 shrink-0 disabled:opacity-50"
          title={tr("mp.run")}
        >
          {busy ? tr("mp.running") : `↻ ${tr("mp.run")}`}
        </button>
      </div>

      {error && (
        <div className="text-xs text-app-down mt-2">
          {tr("mp.failed")}: {error}
        </div>
      )}

      {busy && !results && (
        <div className="text-xs text-slate-500 animate-pulse py-4 text-center">{tr("mp.waiting")}</div>
      )}

      {results && results.length > 0 && (
        <div className="mt-2">
          <div className="text-[10px] text-slate-500 mb-2 truncate">
            {tr("mp.target")}: <span className="text-slate-400 font-mono">{target}</span>
          </div>
          <ul className="space-y-1.5">
            {results.map((r, idx) => (
              <li
                key={`${r.country}-${r.city}`}
                className={`rounded-md px-2 py-1.5 ${idx === 0 ? "bg-app-up/10" : ""}`}
              >
                <div className="flex items-center justify-between gap-2 text-xs">
                  <span className="min-w-0 truncate">
                    <span className="mr-1.5">{FLAGS[r.country] ?? "\u{1F3F3}️"}</span>
                    <span className={idx === 0 ? "text-green-300 font-medium" : "text-slate-300"}>
                      {countryName(r.country)}
                    </span>
                    <span className="text-slate-500"> · {r.city}</span>
                  </span>
                  <span className="flex items-center gap-1.5 shrink-0">
                    {idx === 0 && (
                      <span className="text-[9px] px-1.5 py-0.5 rounded-full bg-app-up/20 text-app-up uppercase">
                        {tr("mp.fastest")}
                      </span>
                    )}
                    <span
                      className={`tabular-nums ${
                        idx === 0 ? "text-green-300 font-semibold" : "text-slate-200"
                      }`}
                    >
                      {fmtMs(r.avgMs)}
                    </span>
                    {r.lossPct > 0 && <span className="text-amber-400 tabular-nums">{r.lossPct}%</span>}
                  </span>
                </div>
                <div className="text-[10px] text-slate-600 truncate pl-6">{r.network}</div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
