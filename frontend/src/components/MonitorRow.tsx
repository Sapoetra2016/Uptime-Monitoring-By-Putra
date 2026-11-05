import { memo } from "react";
import { Link } from "react-router-dom";
import { useIsChecking, useMonitor } from "../hooks/useMonitor";
import { targetLabel } from "../lib/format";
import { t, useI18n } from "../lib/i18n";
import type { Monitor } from "../lib/types";
import StatusRing from "./StatusRing";

/**
 * Self-subscribing table row for the monitors list. Only re-renders when its
 * own monitor's data or checking-state changes — the rest of the table is
 * untouched during websocket pushes.
 */
function MonitorRowInner({
  id,
  isAdmin,
  compact,
  onEdit,
  onDelete,
  onToggle,
  toggling,
}: {
  id: string;
  isAdmin: boolean;
  compact?: boolean;
  onEdit: (m: Monitor) => void;
  onDelete: (m: Monitor) => void;
  onToggle: (m: Monitor) => void;
  toggling?: boolean;
}) {
  const monitor = useMonitor(id);
  const checking = useIsChecking(id);
  const { lang } = useI18n();

  if (!monitor) return null;
  void lang; // re-render rows when the UI language changes

  const paused = !monitor.enabled;
  const pad = compact ? "p-1.5" : "p-3";

  return (
    <tr
      className={`border-t border-slate-800 transition-colors duration-300 ${
        paused ? "opacity-60" : ""
      } ${checking ? "bg-sky-500/5 border-l-2 border-l-sky-500" : ""}`}
    >
      <td className={pad}>
        <div className="relative inline-block">
          <StatusRing status={monitor.status} />
          {checking && (
            <span className="absolute inset-0 rounded-full animate-ping bg-sky-400/40" />
          )}
        </div>
      </td>
      <td className={pad}>
        <Link
          to={`/monitors/${monitor.id}`}
          className="text-slate-100 hover:text-sky-400 font-medium"
        >
          {monitor.name}
        </Link>
      </td>
      <td className={`${pad} uppercase text-xs`}>{monitor.type}</td>
      <td className={`${pad} text-slate-400 truncate max-w-xs`}>
        {checking ? (
          <span className="text-sky-400">{t("common.checking")}</span>
        ) : (
          <Link to={`/monitors/${monitor.id}`} className="hover:text-sky-400">
            {targetLabel(monitor)}
          </Link>
        )}
      </td>
      <td className={pad}>
        <div className="flex flex-wrap gap-1">
          {(monitor.tags ?? []).map((tag) => (
            <span
              key={tag}
              className="text-[10px] px-1.5 py-0.5 rounded bg-slate-800 text-slate-400"
            >
              {tag}
            </span>
          ))}
        </div>
      </td>
      <td className={`${pad} text-slate-400`}>{monitor.intervalSec}s</td>
      <td className={pad}>
        {monitor.enabled ? (
          t("common.yes")
        ) : (
          <span className="text-amber-400">{t("common.paused")}</span>
        )}
      </td>
      <td
        className={`${pad} text-slate-400`}
        title={
          monitor.createdBy
            ? t("mon.addedBy", { name: monitor.createdBy })
            : undefined
        }
      >
        {monitor.createdBy || <span className="text-slate-600">—</span>}
      </td>
      {isAdmin && (
        <td className={`${pad} text-right space-x-2 whitespace-nowrap`}>
          <button
            className="btn-secondary"
            disabled={toggling}
            onClick={() => onToggle(monitor)}
            title={monitor.enabled ? t("mon.pauseTitle") : t("mon.resumeTitle")}
          >
            {toggling
              ? "…"
              : monitor.enabled
                ? t("common.pause")
                : t("common.resume")}
          </button>
          <button className="btn-secondary" onClick={() => onEdit(monitor)}>
            {t("common.edit")}
          </button>
          <button className="btn-danger" onClick={() => onDelete(monitor)}>
            {t("common.delete")}
          </button>
        </td>
      )}
    </tr>
  );
}

export const MonitorRow = memo(MonitorRowInner);
export default MonitorRow;
