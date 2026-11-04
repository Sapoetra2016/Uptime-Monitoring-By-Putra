import { useSyncExternalStore } from "react";
import { toastStore, type Toast } from "../lib/toastStore";
import { t, useI18n } from "../lib/i18n";

const STYLES: Record<Toast["kind"], { ring: string; dot: string; label: string }> = {
  down: {
    ring: "border-red-500/50 bg-app-down/15",
    dot: "bg-app-down",
    label: "text-red-300",
  },
  recovered: {
    ring: "border-green-500/50 bg-app-up/15",
    dot: "bg-app-up",
    label: "text-green-300",
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
  const toasts = useSyncExternalStore(toastStore.subscribe, toastStore.getSnapshot, toastStore.getSnapshot);

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
              <span className={`mt-1.5 w-2.5 h-2.5 rounded-full shrink-0 ${s.dot}`} />
              <div className="min-w-0 flex-1">
                <div className={`text-sm font-semibold ${s.label}`}>{toast.title}</div>
                {toast.message && (
                  <div className="text-xs text-slate-400 mt-0.5 break-words">{toast.message}</div>
                )}
                <div className="text-[10px] text-slate-500 mt-1">{timeText(toast.at)}</div>
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
