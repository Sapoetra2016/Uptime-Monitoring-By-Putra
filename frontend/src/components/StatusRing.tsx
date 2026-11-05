import type { MonitorStatus } from "../lib/types";

const colorMap: Record<MonitorStatus, string> = {
  up: "#22c55e",
  down: "#ef4444",
  degraded: "#eab308",
  pending: "#94a3b8",
  unknown: "#64748b",
};

export default function StatusRing({
  status,
  size = 14,
}: {
  status: MonitorStatus;
  size?: number;
}) {
  const color = colorMap[status] || colorMap.unknown;
  // Up/down get directional triangles: ▲ green, ▼ red. Other states stay a
  // neutral dot (degraded keeps its own amber dot so it never looks "down").
  const clip =
    status === "up"
      ? "polygon(50% 0, 100% 100%, 0 100%)"
      : status === "down"
        ? "polygon(0 0, 100% 0, 50% 100%)"
        : null;
  return (
    <span
      className="inline-flex items-center justify-center relative"
      style={{ width: size, height: size }}
      title={status}
    >
      {status === "down" && (
        <span
          className="absolute inset-0 rounded-full animate-ping"
          style={{ backgroundColor: color, opacity: 0.45 }}
        />
      )}
      <span
        className={clip ? undefined : "rounded-full"}
        style={{
          width: size,
          height: size,
          backgroundColor: color,
          clipPath: clip ?? undefined,
        }}
      />
    </span>
  );
}
