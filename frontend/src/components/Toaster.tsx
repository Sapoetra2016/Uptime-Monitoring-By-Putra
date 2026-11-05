import { useSyncExternalStore } from "react";
import { toastStore, type Toast } from "../lib/toastStore";
import { t, useI18n } from "../lib/i18n";

const STYLES: Record<
  Toast["kind"],
  { ring: string; tri: string; label: string }
> = {
  down: {
    ring: "border-red-500/50 bg-app-down/15",
    // ▼ red down-triangle
    tri: "w-0 h-0 border-x-[5px] border-x-transparent border-t-[9px] border-t-app-down",
    label: "text-red-300",
  },
  recovered: {
    ring: "border-green-500/50 bg-app-up/15",
    // ▲ green up-triangle
    tri: "w-0 h-0 mt-0.5 border-x-[5px] border-x-transparent border-b-[9px] border-b-app-up",
    label: "text-green-300",
  },
  offline: {
    ring: "border-amber-500/50 bg-amber-500/10",
    // ● amber dot (idle / stale feed)
    tri: "w-2 h-2 mt-1.5 rounded-full bg-amber-400",
    label: "text-amber-300",
  },
};

function timeText(at: number): string {
  return new Date(at).toLocaleTimeString();
}

/**
 * Bottom-right stack of status alerts. Pushed to by the live feed whenever a
 * monitor goes down or recovers. Each toast disappears on its own after 10s
 * (handled in the store); the close button just dismisses it early.
 */
export default function Toaster() {
  const { lang } = useI18n();
  void lang; // re-render when language changes (t() reads the live store)
  const toasts = useSyncExternalStore(
    toastStore.subscribe,
    toastStore.getSnapshot,
    toastStore.getSnapshot,
  );

  if (toasts.length === 0) return null;

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2 w-80 max-w-[calc(100vw-2rem)]">
      {toasts.map((toast) => {
        const s = STYLES[toast.kind];
        return (
          <div
            key={toast.id}
            className={`rounded-lg border ${s.ring} bg-app-panel/95 backdrop-blur px-4 py-3 shadow-xl shadow-black/40 animate-[toastIn_0.2s_ease-out]`}
            role="status"
          >
            <div className="flex items-start gap-3">
              <span className={`mt-1.5 shrink-0 ${s.tri}`} />
              <div className="min-w-0 flex-1">
                <div className={`text-sm font-semibold ${s.label}`}>
                  {toast.title}
                </div>
                {toast.message && (
                  <div className="text-xs text-slate-400 mt-0.5 break-words">
                    {toast.message}
                  </div>
                )}
                <div className="text-[10px] text-slate-500 mt-1">
                  {timeText(toast.at)}
                </div>
                {toast.kind === "offline" && (
                  <button
                    onClick={() => window.location.reload()}
                    className="mt-2 w-full rounded-md bg-amber-500/20 hover:bg-amber-500/30 border border-amber-500/40 text-amber-200 text-xs font-medium py-1.5"
                  >
                    {t("toast.reload")}
                  </button>
                )}
              </div>
              <button
                onClick={() => toastStore.dismiss(toast.id)}
                className="text-slate-500 hover:text-white shrink-0 -mr-1 -mt-1 px-1"
                aria-label={t("common.dismiss")}
              >
                ×
              </button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
