/**
 * Minimal external toast store (non-React).
 *
 * The websocket handler in useLive pushes alerts here whenever a monitor flips
 * to "down" or recovers to "up". The <Toaster /> component subscribes via
 * useSyncExternalStore and renders them bottom-right. Each toast auto-dismisses
 * after AUTO_DISMISS_MS (10s) so the user never has to close them manually.
 */

export type ToastKind = "down" | "recovered" | "offline";

export interface Toast {
  id: number;
  kind: ToastKind;
  title: string;
  message?: string;
  at: number;
  // Sticky toasts stay until explicitly dismissed (used by the offline alert).
  sticky?: boolean;
}

const AUTO_DISMISS_MS = 10_000;

type Listener = () => void;

class ToastStore {
  private toasts: Toast[] = [];
  private listeners = new Set<Listener>();
  private nextId = 1;

  getSnapshot = (): Toast[] => this.toasts;

  subscribe = (cb: Listener): (() => void) => {
    this.listeners.add(cb);
    return () => this.listeners.delete(cb);
  };

  push(kind: ToastKind, title: string, message?: string, sticky = false): void {
    // Only one offline alert at a time — don't stack duplicates.
    if (sticky) this.dismissKind(kind);
    const toast: Toast = {
      id: this.nextId++,
      kind,
      title,
      message,
      at: Date.now(),
      sticky,
    };
    // Newest at the bottom so the stack grows upward from the corner.
    this.toasts = [...this.toasts, toast];
    this.emit();
    if (!sticky)
      window.setTimeout(() => this.dismiss(toast.id), AUTO_DISMISS_MS);
  }

  // Remove every toast of a kind (e.g. clear the offline alert on reconnect).
  dismissKind(kind: ToastKind): void {
    if (!this.toasts.some((t) => t.kind === kind)) return;
    this.toasts = this.toasts.filter((t) => t.kind !== kind);
    this.emit();
  }

  dismiss(id: number): void {
    if (!this.toasts.some((t) => t.id === id)) return;
    this.toasts = this.toasts.filter((t) => t.id !== id);
    this.emit();
  }

  private emit(): void {
    for (const cb of this.listeners) cb();
  }
}

export const toastStore = new ToastStore();
