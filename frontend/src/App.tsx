import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { clearToken, getRole, getToken, getUsername } from "./lib/api";
import { LiveProvider, useLive } from "./hooks/useLive";
import { useI18n } from "./lib/i18n";
import ErrorBoundary from "./components/ErrorBoundary";
import Toaster from "./components/Toaster";
import Dashboard from "./pages/Dashboard";
import MonitorDetail from "./pages/MonitorDetail";
import MonitorsPage from "./pages/MonitorsPage";
import SettingsPage from "./pages/SettingsPage";
import NotificationsPage from "./pages/NotificationsPage";
import StatusPagesPage from "./pages/StatusPagesPage";
import UsersPage from "./pages/UsersPage";
import PublicStatusPage from "./pages/PublicStatusPage";
import PublicMonitorsPage from "./pages/PublicMonitorsPage";
import LoginPage from "./pages/LoginPage";

function Shell({ children }: { children: ReactNode }) {
  const { connected } = useLive();
  const { t } = useI18n();
  const location = useLocation();
  return (
    <div className="min-h-screen flex flex-col">
      <nav className="flex items-center gap-6 px-6 py-3 bg-app-panel border-b border-slate-800">
        <Link to="/" className="font-bold text-lg">
          {t("app.name")}
        </Link>
        <NavLink to="/" active={location.pathname === "/"}>
          {t("nav.dashboard")}
        </NavLink>
        <NavLink to="/monitors" active={location.pathname.startsWith("/monitors")}>
          {t("nav.monitors")}
        </NavLink>
        <NavLink to="/settings" active={location.pathname.startsWith("/settings")}>
          {t("nav.settings")}
        </NavLink>
        <div className="ml-auto flex items-center gap-3 text-sm">
          <LangToggle />
          <span
            className={`w-2.5 h-2.5 rounded-full ${connected ? "bg-app-up" : "bg-app-down"}`}
            title={connected ? t("live.connected") : t("live.disconnected")}
          />
          <span className="hidden sm:inline text-slate-400">{connected ? t("nav.live") : t("nav.offline")}</span>
          <ProfileMenu />
        </div>
      </nav>
      <main className="flex-1 p-6 max-w-[1600px] w-full mx-auto">
        {/* Per-route boundary: a crash in one page doesn't blank the nav bar. */}
        <ErrorBoundary key={location.pathname}>{children}</ErrorBoundary>
      </main>
      <footer className="text-center text-xs text-slate-600 py-4 border-t border-slate-800">
        © {new Date().getFullYear()} Putra_aja
      </footer>
    </div>
  );
}

// Quick ID/EN switch in the nav bar; persists through Preferences (localStorage).
function LangToggle() {
  const { lang, setLanguage } = useI18n();
  return (
    <button
      onClick={() => setLanguage(lang === "id" ? "en" : "id")}
      className="text-xs px-2 py-1 rounded border border-slate-700 text-slate-400 hover:text-white hover:border-slate-500 uppercase transition-colors"
      title={lang === "id" ? "Ganti ke English" : "Switch to Bahasa Indonesia"}
    >
      {lang === "id" ? "ID" : "EN"}
    </button>
  );
}

// Avatar dropdown in the top-right corner: profile info + quick links + logout.
function ProfileMenu() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const { t } = useI18n();
  const username = getUsername() ?? "account";
  const role = getRole() ?? "viewer";

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  function go(to: string) {
    setOpen(false);
    navigate(to);
  }

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 rounded-full pl-1 pr-2 py-1 hover:bg-slate-800 transition-colors"
        title={t("account")}
      >
        <span className="w-8 h-8 rounded-full bg-sky-500/20 text-sky-300 flex items-center justify-center text-sm font-bold uppercase select-none">
          {username.slice(0, 2)}
        </span>
        <svg width="12" height="12" viewBox="0 0 12 12" className="text-slate-400">
          <path d="M2 4l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>

      {open && (
        <div className="absolute right-0 mt-2 w-60 bg-app-panel border border-slate-700 rounded-lg shadow-xl shadow-black/40 overflow-hidden z-50">
          <div className="px-4 py-3 border-b border-slate-800">
            <div className="font-medium truncate">{username}</div>
            <div
              className={`inline-block mt-1 text-[10px] px-1.5 py-0.5 rounded uppercase ${
                role === "admin" ? "bg-sky-500/15 text-sky-300" : "bg-slate-700 text-slate-300"
              }`}
            >
              {role === "admin" ? t("role.admin") : t("role.viewer")}
            </div>
          </div>
          <MenuItem label={t("profile.menuSecurity")} onClick={() => go("/settings?tab=profile")} />
          <MenuItem label={t("profile.menuNotifications")} onClick={() => go("/settings?tab=notifications")} />
          <MenuItem label={t("profile.menuAll")} onClick={() => go("/settings")} />
          <button
            className="w-full text-left px-4 py-2.5 text-sm text-red-300 hover:bg-slate-800 border-t border-slate-800"
            onClick={() => {
              clearToken();
              window.location.href = "/login";
            }}
          >
            {t("profile.logout")}
          </button>
        </div>
      )}
    </div>
  );
}

function MenuItem({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      className="w-full text-left px-4 py-2.5 text-sm text-slate-300 hover:bg-slate-800 hover:text-white"
      onClick={onClick}
    >
      {label}
    </button>
  );
}

function NavLink({
  to,
  active,
  children,
}: {
  to: string;
  active: boolean;
  children: ReactNode;
}) {
  return (
    <Link
      to={to}
      className={`text-sm ${active ? "text-white font-medium" : "text-slate-400 hover:text-white"}`}
    >
      {children}
    </Link>
  );
}

function RequireAuth({ children }: { children: ReactNode }) {
  const token = getToken();
  if (!token) return <Navigate to="/login" replace />;
  return <Shell>{children}</Shell>;
}

// Root is public when logged out (shows the monitor list + status) and the
// authenticated dashboard when logged in. Management stays behind RequireAuth.
function RootGate({ children }: { children: ReactNode }) {
  const token = getToken();
  if (!token) return <PublicMonitorsPage />;
  return <Shell>{children}</Shell>;
}

export default function App() {
  return (
    <ErrorBoundary>
      <LiveProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/status/:slug" element={<PublicStatusPage />} />
          <Route
            path="/"
            element={
              <RootGate>
                <Dashboard />
              </RootGate>
            }
          />
          <Route
            path="/monitors"
            element={
              <RequireAuth>
                <MonitorsPage />
              </RequireAuth>
            }
          />
          <Route
            path="/monitors/:id"
            element={
              <RequireAuth>
                <MonitorDetail />
              </RequireAuth>
            }
          />
          <Route
            path="/settings"
            element={
              <RequireAuth>
                <SettingsPage />
              </RequireAuth>
            }
          />
          {/* Old URLs redirect into Settings */}
          <Route
            path="/notifications"
            element={
              <RequireAuth>
                <NotificationsPage />
              </RequireAuth>
            }
          />
          <Route
            path="/status-pages"
            element={
              <RequireAuth>
                <StatusPagesPage />
              </RequireAuth>
            }
          />
          <Route
            path="/users"
            element={
              <RequireAuth>
                <UsersPage />
              </RequireAuth>
            }
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        <Toaster />
      </LiveProvider>
    </ErrorBoundary>
  );
}
