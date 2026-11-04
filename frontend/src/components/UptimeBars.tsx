import type { UptimePoint } from "../lib/types";

/** Color for a single day bar based on its availability percentage. */
export function barColor(pct: number): string {
  if (pct < 0) return "#1e293b"; // no data
  if (pct >= 99) return "#22c55e";
  if (pct >= 95) return "#84cc16";
  if (pct >= 80) return "#eab308";
  if (pct >= 50) return "#f97316";
  return "#ef4444";
}

/** Format an availability percentage for compact display. */
export function pctLabel(pct: number): string {
  if (pct < 0) return "—";
  return `${pct.toFixed(pct >= 99.95 ? 2 : 1)}%`;
}

/** A horizontal strip of per-day uptime bars (oldest -> newest). */
export function UptimeBars({ history }: { history: UptimePoint[] }) {
  const h = history ?? [];
  if (h.length === 0) return null;
  const fmt = (d: string) => {
    const dt = new Date(d + "T00:00:00Z");
    return dt.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
  };
  return (
    <div className="flex items-end gap-[2px] h-8 mt-3">
      {h.map((p) => (
        <div
          key={p.date}
          className="flex-1 rounded-sm min-w-[3px] transition-all"
          style={{ height: "100%", backgroundColor: barColor(p.pct) }}
          title={`${fmt(p.date)}: ${p.pct < 0 ? "no data" : p.pct.toFixed(2) + "% up"}`}
        />
      ))}
    </div>
  );
}
