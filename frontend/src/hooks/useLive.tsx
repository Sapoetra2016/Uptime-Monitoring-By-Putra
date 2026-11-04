import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api, AUTH_CHANGED_EVENT, getToken } from "../lib/api";
import { monitorStore } from "../lib/monitorStore";
import { toastStore } from "../lib/toastStore";
import { t } from "../lib/i18n";
import type { LiveEvent } from "../lib/types";

interface LiveContextValue {
  connected: boolean;
  refresh: () => void;
}

const LiveContext = createContext<LiveContextValue>({
  connected: false,
  refresh: () => {},
});

/**
 * Live feed connection + connection-state context.
 *
 * IMPORTANT: monitor data is NOT stored in React state here anymore. All
 * websocket events are dispatched straight into `monitorStore`, whose
 * fine-grained subscriptions (useMonitor / useMonitorIds / useMonitorStats /
 * useIsChecking) decide which individual UI nodes re-render. useLive() now only
 * exposes connection status and the imperative `refresh()`.
 */
export function useLive() {
  return useContext(LiveContext);
}

export function LiveProvider({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const [reconnectKey, setReconnectKey] = useState(0);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeout = useRef<number | undefined>(undefined);

  // Re-establish the live feed whenever auth (token) changes, e.g. after login
  // or logout, without needing a full page reload.
  useEffect(() => {
    const onAuthChanged = () => setReconnectKey((k) => k + 1);
    window.addEventListener(AUTH_CHANGED_EVENT, onAuthChanged);
    return () => window.removeEventListener(AUTH_CHANGED_EVENT, onAuthChanged);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const list = await api.listMonitors();
      monitorStore.setAll(list ?? []);
    } catch (e) {
      console.error("failed to refresh monitors", e);
    }
  }, []);

  useEffect(() => {
    let disposed = false;

    function applyUpdate(data: Extract<LiveEvent, { type: "monitor.update" }>) {
      const prev = monitorStore.getById(data.monitorId);
      const prevStatus = prev?.status;
      monitorStore.updateOne(data.monitorId, {
        status: data.status,
        lastLatencyMs: data.latencyMs ?? monitorStore.getById(data.monitorId)?.lastLatencyMs,
        statusReason: data.message ?? monitorStore.getById(data.monitorId)?.statusReason,
        lastCheckAt: data.at,
      });
      // A finished check clears the per-node "checking" indicator.
      monitorStore.markChecking(data.monitorId, false);

      // Pop a bottom-right alert on real status transitions (not on the initial
      // snapshot, which never reaches here). Auto-dismisses after 10s.
      const name = data.name || prev?.name || "Monitor";
      if (data.status === "down" && prevStatus && prevStatus !== "down") {
        toastStore.push("down", t("toast.down", { name }), data.message || prev?.statusReason);
      } else if (data.status === "up" && prevStatus === "down") {
        toastStore.push("recovered", t("toast.recovered", { name }), t("toast.recoveredMsg"));
      }
    }

    function connect() {
      if (disposed) return;
      const token = getToken();
      const proto = window.location.protocol === "https:" ? "wss" : "ws";
      const url = `${proto}://${window.location.host}/api/ws${token ? `?token=${token}` : ""}`;
      const ws = new WebSocket(url);
      wsRef.current = ws;

      ws.onopen = () => setConnected(true);
      ws.onclose = () => {
        setConnected(false);
        if (!disposed) {
          reconnectTimeout.current = window.setTimeout(connect, 3000);
        }
      };
      ws.onerror = () => ws.close();
      ws.onmessage = (evt) => {
        try {
          const data = JSON.parse(evt.data) as LiveEvent;
          if (data.type === "snapshot") {
            monitorStore.setAll(data.monitors ?? []);
          } else if (data.type === "monitor.update") {
            applyUpdate(data);
          } else if (data.type === "monitor.check_started") {
            monitorStore.markChecking(data.monitorId, true);
          } else if (data.type === "monitors.refresh") {
            refresh();
          }
        } catch (e) {
          console.error("bad ws message", e);
        }
      };
    }

    connect();
    return () => {
      disposed = true;
      if (reconnectTimeout.current) clearTimeout(reconnectTimeout.current);
      wsRef.current?.close();
    };
  }, [refresh, reconnectKey]);

  return (
    <LiveContext.Provider value={{ connected, refresh }}>
      {children}
    </LiveContext.Provider>
  );
}
