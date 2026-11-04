import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { CheckResult } from "../lib/types";

export default function LatencyChart({ checks }: { checks: CheckResult[] }) {
  const data = checks.map((c) => ({
    time: new Date(c.createdAt).toLocaleTimeString(),
    latency: c.latencyMs ?? 0,
    status: c.status,
  }));

  return (
    <div style={{ width: "100%", height: 240 }}>
      <ResponsiveContainer>
        <LineChart data={data}>
          <CartesianGrid stroke="#1e293b" strokeDasharray="3 3" />
          <XAxis dataKey="time" stroke="#64748b" tick={{ fontSize: 11 }} minTickGap={30} />
          <YAxis stroke="#64748b" tick={{ fontSize: 11 }} unit="ms" />
          <Tooltip
            contentStyle={{ backgroundColor: "#111a2e", border: "1px solid #334155", borderRadius: 6 }}
          />
          <Line type="monotone" dataKey="latency" stroke="#38bdf8" dot={false} strokeWidth={2} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
