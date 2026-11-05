import type { ReactNode } from "react";

/**
 * Shared panel card used across the dashboard and monitor detail pages.
 * Keeps background, border, radius, and padding consistent everywhere.
 */
export default function Card({ children }: { children: ReactNode }) {
  return (
    <div className="bg-app-panel border border-slate-800 rounded-lg p-4">
      {children}
    </div>
  );
}

/** Standard card heading — one style for every card title. */
export function CardTitle({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <h2 className={`text-sm font-semibold text-slate-300 mb-3 ${className}`}>
      {children}
    </h2>
  );
}
