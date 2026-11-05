import { useCallback, useSyncExternalStore } from "react";
import { monitorStore, type GlobalStats } from "../lib/monitorStore";
import type { Monitor } from "../lib/types";

/**
 * Fine-grained subscriptions over the external monitor store. Each hook only
 * triggers a re-render for the specific slice of data it reads, so a websocket
 * update to one monitor re-renders just that node.
 */

/** Subscribe to a single monitor. Re-renders only when THAT monitor changes. */
export function useMonitor(id: string): Monitor | undefined {
  const subscribe = useCallback(
    (cb: () => void) => monitorStore.subscribeNode(id, cb),
    [id],
  );
  const getSnapshot = useCallback(() => monitorStore.getSnapshot(id), [id]);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/** Subscribe to the ordered list of monitor ids. Re-renders only when monitors
 *  are added / removed / reordered — NOT on per-node status pushes. */
export function useMonitorIds(): string[] {
  const subscribe = useCallback(
    (cb: () => void) => monitorStore.subscribeStructural(cb),
    [],
  );
  const getSnapshot = useCallback(() => monitorStore.getIdsSnapshot(), []);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/** Aggregate up/down/degraded counts. Re-renders on status flips and on
 *  add/remove, but not on latency-only updates. */
export function useMonitorStats(): GlobalStats {
  const subscribe = useCallback(
    (cb: () => void) => monitorStore.subscribeStats(cb),
    [],
  );
  const getSnapshot = useCallback(() => monitorStore.getStatsSnapshot(), []);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/** Whether a specific monitor is currently mid-check (drives the per-node
 *  "checking…" spinner). Independent from the monitor data itself. */
export function useIsChecking(id: string): boolean {
  const subscribe = useCallback(
    (cb: () => void) => monitorStore.subscribeChecking(id, cb),
    [id],
  );
  const getSnapshot = useCallback(() => monitorStore.isChecking(id), [id]);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
