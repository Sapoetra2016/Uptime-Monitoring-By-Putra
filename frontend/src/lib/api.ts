import type {
  CheckResult,
  DomainIntel,
  DomainSSL,
  EscalationPolicy,
  Incident,
  Monitor,
  MonitorAnalysis,
  MonitorStats,
  MonitorUptimeStat,
  MultiPingResult,
  Notification,
  PublicMonitor,
  StatusPage,
  SystemSettings,
  User,
  UserRole,
} from "./types";

const TOKEN_KEY = "uptime_token";
const ROLE_KEY = "uptime_role";
const USER_KEY = "uptime_user";
export const AUTH_CHANGED_EVENT = "uptime-auth-changed";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t);
  window.dispatchEvent(new Event(AUTH_CHANGED_EVENT));
}
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(ROLE_KEY);
  localStorage.removeItem(USER_KEY);
  window.dispatchEvent(new Event(AUTH_CHANGED_EVENT));
}
export function getUsername(): string | null {
  return localStorage.getItem(USER_KEY);
}
export function setUsername(name: string) {
  localStorage.setItem(USER_KEY, name);
}
export function getRole(): UserRole | null {
  const r = localStorage.getItem(ROLE_KEY);
  return r === "admin" || r === "viewer" ? r : null;
}
export function setRole(r: UserRole) {
  localStorage.setItem(ROLE_KEY, r);
}

async function request<T>(path: string, options: RequestInit = {}, skipAuthClear = false): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;

  let res: Response;
  try {
    res = await fetch(`/api${path}`, { ...options, headers });
  } catch (e: any) {
    // Network-level failure (server unreachable, DNS, CORS preflight, etc.)
    throw new Error("Cannot reach server — please check that the backend is running");
  }
  if (res.status === 401) {
    // A 401 while changing the password means "old password incorrect", not
    // "session expired" — don't wipe the token and force a logout.
    if (!skipAuthClear) clearToken();
    throw new Error("Unauthorized — please log in again");
  }
  if (!res.ok) {
    // Read the body defensively. If it isn't JSON (proxy 502 HTML page, empty
    // body, etc.) fall back to the status text so we never throw a raw
    // SyntaxError from res.json().
    const text = await res.text().catch(() => "");
    const short = text && text.length < 200 ? text : "";
    throw new Error(short || `Request failed (${res.status} ${res.statusText})`);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new Error(`Invalid response from server (${res.status})`);
  }
}

// requestList<T> is a defensive wrapper for endpoints that should return a
// JSON array. Older backend builds (or a proxy returning an HTML error page)
// can produce `null` / `undefined`, which crashes downstream `.map()` calls.
// This guarantees callers always get an array.
async function requestList<T>(path: string, options: RequestInit = {}): Promise<T[]> {
  const data = await request<T | T[] | null | undefined>(path, options);
  if (Array.isArray(data)) return data as T[];
  if (data == null) return [];
  // Single-object fallback — some endpoints return `{ items: [] }` shape.
  const obj = data as Record<string, unknown>;
  if (Array.isArray(obj.items)) return obj.items as T[];
  if (Array.isArray(obj.data)) return obj.data as T[];
  return [];
}

export const api = {
  login: (username: string, password: string) =>
    request<{ token: string; username: string; role: UserRole }>("/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),

  me: () => request<{ username: string; role: UserRole }>("/me"),

  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>(
      "/me/password",
      { method: "PUT", body: JSON.stringify({ currentPassword, newPassword }) },
      true,
    ).catch((e) => {
      // Surface a useful message for wrong old password instead of "log in again".
      if (e.message && e.message.startsWith("Unauthorized")) {
        throw new Error("Current password is incorrect");
      }
      throw e;
    }),

  listUsers: () => requestList<User>("/users"),
  createUser: (u: { username: string; password: string; role: UserRole }) =>
    request<User>("/users", { method: "POST", body: JSON.stringify(u) }),
  deleteUser: (id: string) => request<void>(`/users/${id}`, { method: "DELETE" }),
  updateUserRole: (id: string, role: UserRole) =>
    request<void>(`/users/${id}/role`, { method: "PUT", body: JSON.stringify({ role }) }),
  updateUserPassword: (id: string, password: string) =>
    request<void>(`/users/${id}/password`, { method: "PUT", body: JSON.stringify({ password }) }),

  listMonitors: () => requestList<Monitor>("/monitors"),
  getMonitor: (id: string) => request<Monitor>(`/monitors/${id}`),
  createMonitor: (m: Partial<Monitor>) =>
    request<Monitor>("/monitors", { method: "POST", body: JSON.stringify(m) }),
  updateMonitor: (id: string, m: Partial<Monitor>) =>
    request<Monitor>(`/monitors/${id}`, { method: "PUT", body: JSON.stringify(m) }),
  deleteMonitor: (id: string) => request<void>(`/monitors/${id}`, { method: "DELETE" }),
  setMonitorEnabled: (id: string, enabled: boolean) =>
    request<Monitor>(`/monitors/${id}/enabled`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),
  manualCheck: (id: string) => request<{ status: string }>(`/monitors/${id}/check`, { method: "POST" }),
  recentChecks: (id: string, limit = 100) =>
    requestList<CheckResult>(`/monitors/${id}/checks?limit=${limit}`),
  monitorStats: (id: string, hours = 24) =>
    request<{ stats: MonitorStats; checks: CheckResult[] }>(`/monitors/${id}/stats?hours=${hours}`),
  monitorUptime: (id: string, days = 365) =>
    request<MonitorUptimeStat>(`/monitors/${id}/uptime?days=${days}`),
  domainSsl: (id: string) => request<DomainSSL>(`/monitors/${id}/domain-ssl`),
  // Outside-in domain audit (DNS + email security + headers + SSL detail).
  domainIntel: (id: string) => request<DomainIntel>(`/monitors/${id}/domain-intel`),

  listAnalyses: (id: string, limit = 5) =>
    requestList<MonitorAnalysis>(`/monitors/${id}/analyses?limit=${limit}`),
  // Blocks until the LLM responds (can take tens of seconds), so nginx must
  // proxy_read_timeout long enough for this route.
  analyzeMonitor: (id: string) =>
    request<MonitorAnalysis>(`/monitors/${id}/analyze`, { method: "POST" }),

  listNotifications: () => requestList<Notification>("/notifications"),
  createNotification: (n: Partial<Notification>) =>
    request<Notification>("/notifications", { method: "POST", body: JSON.stringify(n) }),
  updateNotification: (id: string, n: Partial<Notification>) =>
    request<Notification>(`/notifications/${id}`, { method: "PUT", body: JSON.stringify(n) }),
  deleteNotification: (id: string) => request<void>(`/notifications/${id}`, { method: "DELETE" }),
  testNotification: (id: string) => request<{ status: string }>(`/notifications/${id}/test`, { method: "POST" }),

  listEscalations: () => requestList<EscalationPolicy>("/escalations"),
  upsertEscalation: (p: EscalationPolicy) =>
    request<EscalationPolicy>("/escalations", { method: "POST", body: JSON.stringify(p) }),
  deleteEscalation: (id: string) => request<void>(`/escalations/${id}`, { method: "DELETE" }),

  listStatusPages: () => requestList<StatusPage>("/status-pages"),
  upsertStatusPage: (p: StatusPage) =>
    request<StatusPage>("/status-pages", { method: "POST", body: JSON.stringify(p) }),
  deleteStatusPage: (id: string) => request<void>(`/status-pages/${id}`, { method: "DELETE" }),
  publicStatusPage: async (slug: string, days = 90) => {
    const res = await request<{
      page: StatusPage;
      monitors: Monitor[] | null;
      incidents: Incident[] | null;
      stats: MonitorUptimeStat[] | null;
      uptime: number | null;
      days: number | null;
    }>(`/status-page/${slug}?days=${days}`);
    // Normalise nested arrays so consumers can safely call `.map()` on them
    // even if an older backend build returned `null`.
    return {
      page: res.page,
      monitors: res.monitors ?? [],
      incidents: res.incidents ?? [],
      stats: res.stats ?? [],
      uptime: res.uptime ?? 100,
      days: res.days ?? days,
    };
  },

  // Unauthenticated public overview of all monitored services.
  publicMonitors: async (days = 30) => {
    const res = await request<{
      monitors: PublicMonitor[] | null;
      stats: MonitorUptimeStat[] | null;
      uptime: number | null;
      days: number | null;
    }>(`/public/monitors?days=${days}`);
    return {
      monitors: res.monitors ?? [],
      stats: res.stats ?? [],
      uptime: res.uptime ?? 100,
      days: res.days ?? days,
    };
  },

  listIncidents: (monitorId?: string) =>
    requestList<Incident>(monitorId ? `/incidents?monitorId=${encodeURIComponent(monitorId)}` : "/incidents"),

  // Real ping from one Globalping probe per available Asian country.
  multiPing: (monitorId: string) =>
    request<{ target: string; results: MultiPingResult[] }>("/multi-ping", {
      method: "POST",
      body: JSON.stringify({ monitorId }),
    }),

  // Server-wide settings (admin edits, any logged-in role can read).
  getSettings: () => request<SystemSettings>("/settings"),
  saveSettings: (s: SystemSettings) =>
    request<SystemSettings>("/settings", { method: "PUT", body: JSON.stringify(s) }),
  publicSettings: () => request<{ title: string; footer: string }>("/public/settings"),
  countChecks: () => request<{ count: number }>("/maintenance/stats"),
  purgeHistory: (olderThanDays: number) =>
    request<{ deleted: number }>("/maintenance/purge-history", {
      method: "POST",
      body: JSON.stringify({ olderThanDays }),
    }),
  createIncident: (i: Partial<Incident>) =>
    request<Incident>("/incidents", { method: "POST", body: JSON.stringify(i) }),
  updateIncident: (id: string, i: Partial<Incident>) =>
    request<Incident>(`/incidents/${id}`, { method: "PUT", body: JSON.stringify(i) }),
};
