import { memo } from "react";
import { useIsChecking, useMonitor } from "../hooks/useMonitor";
import { t, useI18n } from "../lib/i18n";
import type { CheckResult } from "../lib/types";
import StatusRing from "./StatusRing";

function timeAgo(iso?: string) {
  if (!iso) return t("common.never");
  const diff = Date.now() - new Date(iso).getTime();
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return t("timeago.s", { n: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("timeago.m", { n: min });
  const hr = Math.floor(min / 60);
  return t("timeago.h", { n: hr });
}

/**
 * Self-subscribing monitor card — only re-renders when its OWN monitor data
 * or checking-state changes, not when another monitor updates.
 */
function MonitorCardInner({
  id,
  recentChecks,
}: {
  id: string;
  recentChecks?: CheckResult[];
}) {
  const monitor = useMonitor(id);
  const checking = useIsChecking(id);
  const { lang } = useI18n();

  // Monitor removed — render nothing (parent will drop this from ids list).
  if (!monitor) return null;
  void lang; // re-render card when the UI language changes

  return (
    <div
      className={`bg-app-panel border rounded-lg p-4 flex items-center gap-4 transition-colors duration-300 ${
        checking ? "border-sky-500/60 node-checking" : "border-slate-800"
      } ${!monitor.enabled ? "opacity-60" : ""}`}
    >
      <div className="relative">
        <StatusRing status={monitor.status} size={16} />
        {checking && (
          <span className="absolute inset-0 rounded-full animate-ping bg-sky-400/40" />
        )}
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-baseline gap-2">
          <span className="font-medium truncate">{monitor.name}</span>
          <span className="text-xs uppercase text-slate-500">
            {monitor.type}
          </span>
          {!monitor.enabled && (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-800 text-amber-400 uppercase">
              {t("common.paused")}
            </span>
          )}
        </div>
        <div className="text-xs text-slate-400 truncate">
          {checking ? (
            <span className="text-sky-400">{t("common.checking")}</span>
          ) : (
            monitor.statusReason || monitor.url || monitor.host || "—"
          )}
        </div>
        {monitor.tags && monitor.tags.length > 0 && (
          <div className="flex flex-wrap gap-1 mt-1">
            {monitor.tags.map((tag) => (
              <span
                key={tag}
                className="text-[10px] px-1.5 py-0.5 rounded bg-slate-800 text-slate-400"
              >
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>
      {recentChecks && recentChecks.length > 0 && (
        <div
          className="hidden md:flex items-end gap-0.5 h-8"
          title={t("card.lastChecks")}
        >
          {recentChecks
            .slice(0, 24)
            .reverse()
            .map((c) => (
              <div
                key={c.id}
                className="w-1.5 rounded-sm"
                style={{
                  height:
                    c.status === "up"
                      ? "40%"
                      : c.status === "degraded"
                        ? "65%"
                        : "90%",
                  backgroundColor:
                    c.status === "up"
                      ? "#22c55e"
                      : c.status === "down"
                        ? "#ef4444"
                        : c.status === "degraded"
                          ? "#eab308"
                          : "#94a3b8",
                }}
              />
            ))}
        </div>
      )}
      <div className="text-right text-xs text-slate-400 w-24">
        {monitor.lastLatencyMs != null && (
          <div>{Math.round(monitor.lastLatencyMs)} ms</div>
        )}
        <div>{timeAgo(monitor.lastCheckAt)}</div>
      </div>
    </div>
  );
}

// Memo prevents parent re-renders from cascading into every card. Since the
// card self-subscribes, the parent only needs to pass a stable `id` and
// optional `recentChecks` — status pushes don't touch those props.
export const MonitorCard = memo(MonitorCardInner);
export default MonitorCard;
