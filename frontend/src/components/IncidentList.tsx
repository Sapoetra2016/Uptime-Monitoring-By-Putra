import { useState } from "react";
import Card, { CardTitle } from "./Card";
import { formatUptime } from "../lib/format";
import { t, useI18n } from "../lib/i18n";
import type { Incident } from "../lib/types";

/**
 * "Latest incidents" card — expandable accordion rows showing start/resolved
 * time, duration, and the raw cause captured by the monitoring engine.
 */
export default function IncidentList({ incidents }: { incidents: Incident[] }) {
  useI18n(); // re-render on language switch
  const [openId, setOpenId] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const PAGE_SIZE = 5;
  const totalPages = Math.max(1, Math.ceil(incidents.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages - 1);
  const shown = incidents.slice(
    safePage * PAGE_SIZE,
    safePage * PAGE_SIZE + PAGE_SIZE,
  );

  function go(p: number) {
    setPage(p);
    setOpenId(null); // collapse any open row when switching pages
  }

  return (
    <Card>
      <CardTitle>{t("det.latestIncidents")}</CardTitle>
      {shown.length === 0 ? (
        <div className="text-center py-8 bg-slate-900/40 border border-slate-800 rounded-lg">
          <div className="text-lg font-semibold">{t("det.noIncidents")}</div>
          <div className="text-xs text-slate-500 mt-1">
            {t("det.noIncidentsHint")}
          </div>
        </div>
      ) : (
        <>
          <ul className="space-y-2">
            {shown.map((i) => {
              const expanded = openId === i.id;
              const startMs = new Date(i.startedAt).getTime();
              const endMs = i.resolvedAt
                ? new Date(i.resolvedAt).getTime()
                : Date.now();
              const duration = formatUptime(
                Math.max(0, (endMs - startMs) / 1000),
              );
              return (
                <li
                  key={i.id}
                  className="border border-slate-800 rounded-lg overflow-hidden"
                >
                  <button
                    type="button"
                    onClick={() => setOpenId(expanded ? null : i.id)}
                    className="w-full flex items-start justify-between gap-3 p-3 text-left cursor-pointer hover:bg-slate-800/40 transition-colors"
                  >
                    <div className="min-w-0">
                      <div className="text-sm font-medium truncate">
                        {i.headline}
                      </div>
                      <div className="text-xs text-slate-500">
                        {new Date(i.startedAt).toLocaleString()}
                      </div>
                      {i.body && (
                        <div className="text-xs text-slate-400 mt-1 truncate">
                          {i.body}
                        </div>
                      )}
                    </div>
                    <div className="flex items-center gap-2 shrink-0">
                      <span
                        className={`text-xs px-2 py-0.5 rounded-full ${
                          i.status === "resolved"
                            ? "bg-app-up/15 text-app-up"
                            : "bg-amber-500/15 text-amber-400"
                        }`}
                      >
                        {i.status === "resolved"
                          ? t("incident.resolved")
                          : t("incident.open")}
                      </span>
                      <span
                        className={`text-slate-500 text-xs transition-transform ${
                          expanded ? "rotate-180" : ""
                        }`}
                      >
                        ▾
                      </span>
                    </div>
                  </button>
                  {expanded && (
                    <div className="px-3 pb-3 text-xs border-t border-slate-800/60">
                      <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 mt-2">
                        <span className="text-slate-500">
                          {t("det.incStarted")}
                        </span>
                        <span className="text-slate-300">
                          {new Date(i.startedAt).toLocaleString()}
                        </span>
                        {i.resolvedAt && (
                          <>
                            <span className="text-slate-500">
                              {t("det.incResolved")}
                            </span>
                            <span className="text-slate-300">
                              {new Date(i.resolvedAt).toLocaleString()}
                            </span>
                          </>
                        )}
                        <span className="text-slate-500">
                          {t("det.incDuration")}
                        </span>
                        <span className="text-slate-300">{duration}</span>
                      </div>
                      {i.body && (
                        <div className="mt-2">
                          <div className="text-slate-500 mb-1">
                            {t("det.incCause")}
                          </div>
                          <div className="text-slate-300 whitespace-pre-wrap break-words bg-slate-900/60 border border-slate-800 rounded p-2 font-mono">
                            {i.body}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
          {totalPages > 1 && (
            <div className="flex flex-wrap items-center justify-center gap-1 mt-3">
              <button
                type="button"
                onClick={() => go(Math.max(0, safePage - 1))}
                disabled={safePage === 0}
                className="px-2 py-1 text-xs rounded border border-slate-700 text-slate-300 hover:bg-slate-800 disabled:opacity-40 disabled:cursor-not-allowed"
                aria-label={t("common.prev")}
              >
                ‹
              </button>
              {Array.from({ length: totalPages }, (_, i) => (
                <button
                  key={i}
                  type="button"
                  onClick={() => go(i)}
                  className={`min-w-8 px-2 py-1 text-xs rounded border transition-colors ${
                    i === safePage
                      ? "bg-sky-500/20 border-sky-500/60 text-sky-300"
                      : "border-slate-700 text-slate-400 hover:bg-slate-800"
                  }`}
                >
                  {i + 1}
                </button>
              ))}
              <button
                type="button"
                onClick={() => go(Math.min(totalPages - 1, safePage + 1))}
                disabled={safePage === totalPages - 1}
                className="px-2 py-1 text-xs rounded border border-slate-700 text-slate-300 hover:bg-slate-800 disabled:opacity-40 disabled:cursor-not-allowed"
                aria-label={t("common.next")}
              >
                ›
              </button>
            </div>
          )}
        </>
      )}
    </Card>
  );
}
