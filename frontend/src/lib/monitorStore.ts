import type { Monitor, MonitorStatus } from "./types";

/**
 * Fine-grained monitor store.
 *
 * The old design kept a single `Monitor[]` array in React state inside the
 * LiveProvider. Every websocket event produced a brand-new array, so the whole
 * dashboard/table re-rendered whenever *any* monitor changed.
 *
 * This store decouples that: monitors live in a plain (non-React) map keyed by
 * id, and each UI "node" subscribes ONLY to the specific monitor(s) it renders.
 * A status change to one node re-renders just that node — the rest of the tree
 * is untouched. There are four independent subscription channels:
 *
 *   - per-node    (useMonitor)     : fires when that one monitor's data changes
 *   - structural  (useMonitorIds)  : fires when monitors are added / removed
 *   - stats       (useMonitorStats): fires on status flips / add / remove
 *   - checking    (useIsChecking)  : fires when a node starts/finishes a check
 *
 * `updateOne` (a websocket status push) deliberately touches ONLY the per-node
 * map entry and the stats cache — it does NOT rebuild the list/ids caches, so
 * list-rendering components don't re-render on every tick.
 */

type Listener = () => void;

export interface GlobalStats {
  total: number;
  up: number;
  down: number;
  degraded: number;
  pending: number;
  unknown: number;
}

const EMPTY_STATS: GlobalStats = {
  total: 0,
  up: 0,
  down: 0,
  degraded: 0,
  pending: 0,
  unknown: 0,
};

class MonitorStore {
  private monitors = new Map<string, Monitor>();
  private order: string[] = [];

  private nodeListeners = new Map<string, Set<Listener>>();
  private structuralListeners = new Set<Listener>();
  private statsListeners = new Set<Listener>();
  private checkingListeners = new Map<string, Set<Listener>>();

  private checking = new Set<string>();

  // Cached snapshots — must keep a stable reference between changes so
  // useSyncExternalStore doesn't loop re-rendering.
  private idsCache: string[] = [];
  private statsCache: GlobalStats = EMPTY_STATS;

  // ---------- snapshot getters (referentially stable) ----------

  getSnapshot(id: string): Monitor | undefined {
    return this.monitors.get(id);
  }

  getIdsSnapshot(): string[] {
    return this.idsCache;
  }

  getStatsSnapshot(): GlobalStats {
    return this.statsCache;
  }

  isChecking(id: string): boolean {
    return this.checking.has(id);
  }

  /** Full current list, ordered. Not used by reactive components directly
   *  (they use ids + per-node) — handy for imperative lookups. */
  getAll(): Monitor[] {
    return this.order.map((id) => this.monitors.get(id)).filter((m): m is Monitor => !!m);
  }

  getById(id: string): Monitor | undefined {
    return this.monitors.get(id);
  }

  // ---------- subscriptions ----------

  subscribeNode(id: string, cb: Listener): () => void {
    let set = this.nodeListeners.get(id);
    if (!set) {
      set = new Set();
      this.nodeListeners.set(id, set);
    }
    set.add(cb);
    return () => set!.delete(cb);
  }

  subscribeStructural(cb: Listener): () => void {
    this.structuralListeners.add(cb);
    return () => this.structuralListeners.delete(cb);
  }

  subscribeStats(cb: Listener): () => void {
    this.statsListeners.add(cb);
    return () => this.statsListeners.delete(cb);
  }

  subscribeChecking(id: string, cb: Listener): () => void {
    let set = this.checkingListeners.get(id);
    if (!set) {
      set = new Set();
      this.checkingListeners.set(id, set);
    }
    set.add(cb);
    return () => set!.delete(cb);
  }

  // ---------- mutations ----------

  /** Replace the entire set (initial snapshot / after CRUD refresh). */
  setAll(monitors: Monitor[]) {
    const hadIds = new Set(this.monitors.keys());
    const nextIds = new Set(monitors.map((m) => m.id));

    this.monitors = new Map(monitors.map((m) => [m.id, m]));
    this.order = monitors.map((m) => m.id);
    this.rebuildCaches();

    const structuralChanged =
      hadIds.size !== nextIds.size ||
      [...hadIds].some((id) => !nextIds.has(id)) ||
      [...nextIds].some((id) => !hadIds.has(id));

    // Every node's data may differ now.
    for (const id of nextIds) this.emitNode(id);
    this.emitStats();
    if (structuralChanged) this.emitStructural();
  }

  /** Apply a partial update to a single monitor (websocket push). Only that
   *  node re-renders; the list/ids caches stay referentially stable. */
  updateOne(id: string, patch: Partial<Monitor>) {
    const cur = this.monitors.get(id);
    if (!cur) return; // unknown node: a structural refresh will bring it in
    const next: Monitor = { ...cur, ...patch };
    this.monitors.set(id, next);
    this.emitNode(id);
    if (cur.status !== next.status) {
      this.recomputeStats();
      this.emitStats();
    }
  }

  markChecking(id: string, value: boolean) {
    if (!this.monitors.has(id)) return;
    const had = this.checking.has(id);
    if (value === had) return;
    if (value) this.checking.add(id);
    else this.checking.delete(id);
    this.emitChecking(id);
  }

  remove(id: string) {
    if (!this.monitors.has(id)) return;
    this.monitors.delete(id);
    this.order = this.order.filter((x) => x !== id);
    this.checking.delete(id);
    this.rebuildCaches();
    this.emitStats();
    this.emitStructural();
  }

  clear() {
    this.monitors = new Map();
    this.order = [];
    this.checking.clear();
    this.rebuildCaches();
    this.emitStats();
    this.emitStructural();
  }

  // ---------- internals ----------

  private rebuildCaches() {
    this.idsCache = [...this.order];
    this.recomputeStats();
  }

  private recomputeStats() {
    const s: GlobalStats = { total: 0, up: 0, down: 0, degraded: 0, pending: 0, unknown: 0 };
    for (const m of this.monitors.values()) {
      s.total++;
      const status = m.status as MonitorStatus;
      if (status in s) (s as unknown as Record<string, number>)[status]++;
    }
    this.statsCache = s;
  }

  private emitNode(id: string) {
    const set = this.nodeListeners.get(id);
    if (set) for (const cb of set) cb();
  }
  private emitStructural() {
    for (const cb of this.structuralListeners) cb();
  }
  private emitStats() {
    for (const cb of this.statsListeners) cb();
  }
  private emitChecking(id: string) {
    const set = this.checkingListeners.get(id);
    if (set) for (const cb of set) cb();
  }
}

export const monitorStore = new MonitorStore();
