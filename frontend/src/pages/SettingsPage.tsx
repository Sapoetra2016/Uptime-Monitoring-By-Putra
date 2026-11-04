import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import NotificationsSection from "../components/NotificationsSection";
import StatusPagesSection from "../components/StatusPagesSection";
import UsersSection from "../components/UsersSection";
import { api, clearToken, getRole, getUsername } from "../lib/api";
import { getPrefs, setPrefs } from "../lib/prefs";
import { useI18n } from "../lib/i18n";
import type { Preferences } from "../lib/prefs";
import type { SystemSettings } from "../lib/types";

type TabId = "profile" | "general" | "notifications" | "statusPages" | "users" | "appearance" | "data" | "about";

interface TabDef {
  id: TabId;
  labelKey: string;
  descKey: string;
  adminOnly?: boolean;
}

const TABS: TabDef[] = [
  { id: "profile", labelKey: "set.tabProfile", descKey: "set.tabProfileDesc" },
  { id: "general", labelKey: "set.tabGeneral", descKey: "set.tabGeneralDesc", adminOnly: true },
  { id: "notifications", labelKey: "set.tabNotifications", descKey: "set.tabNotificationsDesc" },
  { id: "statusPages", labelKey: "set.tabStatusPages", descKey: "set.tabStatusPagesDesc" },
  { id: "users", labelKey: "set.tabUsers", descKey: "set.tabUsersDesc", adminOnly: true },
  { id: "appearance", labelKey: "set.tabAppearance", descKey: "set.tabAppearanceDesc" },
  { id: "data", labelKey: "set.tabData", descKey: "set.tabDataDesc", adminOnly: true },
  { id: "about", labelKey: "set.tabAbout", descKey: "set.tabAboutDesc" },
];

export default function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const { t } = useI18n();
  const isAdmin = getRole() === "admin";
  const visibleTabs = TABS.filter((tab) => !tab.adminOnly || isAdmin);
  const paramTab = params.get("tab") as TabId | null;
  const active: TabId =
    paramTab && visibleTabs.some((tab) => tab.id === paramTab) ? paramTab : "profile";

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">{t("set.title")}</h1>
        <p className="text-sm text-slate-400 mt-1">{t("set.subtitle")}</p>
      </div>

      <div className="flex flex-col md:flex-row gap-6">
        {/* Vertical tab rail */}
        <nav className="md:w-56 flex md:flex-col gap-1 overflow-x-auto">
          {visibleTabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setParams({ tab: tab.id })}
              className={`text-left rounded-lg px-3 py-2.5 shrink-0 transition-colors ${
                active === tab.id
                  ? "bg-slate-800 text-white ring-1 ring-sky-500/40"
                  : "text-slate-400 hover:bg-slate-800/50 hover:text-slate-200"
              }`}
            >
              <span className="block text-sm font-medium">{t(tab.labelKey)}</span>
              <span className="block text-[11px] text-slate-500">{t(tab.descKey)}</span>
            </button>
          ))}
        </nav>

        <div className="flex-1 min-w-0">
          {active === "profile" && <ProfileTab />}
          {active === "general" && isAdmin && <GeneralTab />}
          {active === "notifications" && <NotificationsSection />}
          {active === "statusPages" && <StatusPagesSection />}
          {active === "users" && isAdmin && <UsersSection />}
          {active === "appearance" && <AppearanceTab />}
          {active === "data" && isAdmin && <DataTab />}
          {active === "about" && <AboutTab />}
        </div>
      </div>
    </div>
  );
}

// ---------- Profil ----------

function ProfileTab() {
  const { t } = useI18n();
  const username = getUsername() ?? "—";
  const role = getRole() ?? "viewer";
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    if (next.length < 8) {
      setMsg({ ok: false, text: t("set.pwMin") });
      return;
    }
    if (next !== confirmPw) {
      setMsg({ ok: false, text: t("set.pwMismatch") });
      return;
    }
    setBusy(true);
    try {
      await api.changePassword(cur, next);
      setMsg({ ok: true, text: t("set.pwChanged") });
      setCur("");
      setNext("");
      setConfirmPw("");
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.pwFailed") });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6 max-w-lg">
      <div className="flex items-center gap-4 bg-app-panel border border-slate-800 rounded-lg p-5">
        <div className="w-14 h-14 rounded-full bg-sky-500/20 text-sky-300 flex items-center justify-center text-xl font-bold uppercase">
          {username.slice(0, 2)}
        </div>
        <div>
          <div className="font-semibold text-lg">{username}</div>
          <span
            className={`inline-block mt-1 text-[11px] px-2 py-0.5 rounded uppercase ${
              role === "admin" ? "bg-sky-500/15 text-sky-300" : "bg-slate-700 text-slate-300"
            }`}
          >
            {role === "admin" ? t("role.admin") : t("role.viewerReadonly")}
          </span>
        </div>
      </div>

      <form onSubmit={submit} className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <h2 className="text-lg font-semibold">{t("set.changePw")}</h2>
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.pwCurrent")}</span>
          <input type="password" className="input mt-1" value={cur} onChange={(e) => setCur(e.target.value)} required />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.pwNew")}</span>
          <input type="password" className="input mt-1" value={next} onChange={(e) => setNext(e.target.value)} required />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.pwConfirm")}</span>
          <input type="password" className="input mt-1" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} required />
        </label>
        {msg && (
          <div className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}>{msg.text}</div>
        )}
        <button disabled={busy} className="btn-primary">
          {busy ? t("common.saving") : t("set.pwUpdate")}
        </button>
      </form>
    </div>
  );
}

// ---------- Umum (server-wide settings, admin) ----------

function GeneralTab() {
  const { t } = useI18n();
  const [set, setSet] = useState<SystemSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.getSettings().then(setSet).catch((e) => setError(e.message || t("set.failedLoadSettings")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!set) return;
    setBusy(true);
    setMsg(null);
    try {
      setSet(await api.saveSettings(set));
      setMsg({ ok: true, text: t("set.settingsSaved") });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.saveFailed") });
    } finally {
      setBusy(false);
    }
  }

  if (error) return <div className="text-app-down text-sm">{error}</div>;
  if (!set) return <div className="text-slate-500 text-sm">{t("common.loading")}</div>;

  const patch = (p: Partial<SystemSettings>) => setSet((s) => (s ? { ...s, ...p } : s));

  return (
    <form onSubmit={submit} className="max-w-lg space-y-6">
      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <h2 className="text-lg font-semibold">{t("set.defaultsTitle")}</h2>
        <div className="grid grid-cols-2 gap-4">
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.defaultInterval")}</span>
            <input
              type="number"
              className="input mt-1"
              min={10}
              max={3600}
              step={10}
              value={set.defaultCheckIntervalSec}
              onChange={(e) => patch({ defaultCheckIntervalSec: Number(e.target.value) })}
              required
            />
          </label>
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.defaultTimeout")}</span>
            <input
              type="number"
              className="input mt-1"
              min={1}
              max={120}
              value={set.defaultTimeoutSec}
              onChange={(e) => patch({ defaultTimeoutSec: Number(e.target.value) })}
              required
            />
          </label>
        </div>
        <p className="text-xs text-slate-500">
          {t("set.defaultsHint")}
        </p>
      </div>

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <h2 className="text-lg font-semibold">{t("set.brandingTitle")}</h2>
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.publicTitle")}</span>
          <input
            className="input mt-1"
            maxLength={120}
            value={set.publicTitle}
            onChange={(e) => patch({ publicTitle: e.target.value })}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.publicFooter")}</span>
          <input
            className="input mt-1"
            maxLength={200}
            value={set.publicFooter}
            onChange={(e) => patch({ publicFooter: e.target.value })}
          />
        </label>
      </div>

      {msg && <div className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}>{msg.text}</div>}
      <button disabled={busy} className="btn-primary">
        {busy ? t("common.saving") : t("set.saveSettings")}
      </button>
    </form>
  );
}

// ---------- Data (backup & riwayat, admin) ----------

function DataTab() {
  const { t } = useI18n();
  const [counts, setCounts] = useState<{ checks: number; monitors: number; channels: number } | null>(null);
  const [purgeDays, setPurgeDays] = useState(90);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [error, setError] = useState("");

  async function loadCounts() {
    try {
      const [checks, monitors, channels] = await Promise.all([
        api.countChecks(),
        api.listMonitors(),
        api.listNotifications(),
      ]);
      setCounts({ checks: checks.count, monitors: monitors.length, channels: channels.length });
    } catch (e: any) {
      setError(e.message || t("set.failedLoadStats"));
    }
  }

  useEffect(() => {
    loadCounts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function exportAll() {
    setBusy(true);
    setMsg(null);
    try {
      const [monitors, notifications, escalations, statusPages, settings] = await Promise.all([
        api.listMonitors(),
        api.listNotifications(),
        api.listEscalations(),
        api.listStatusPages(),
        api.getSettings(),
      ]);
      const blob = new Blob(
        [JSON.stringify({ exportedAt: new Date().toISOString(), monitors, notifications, escalations, statusPages, settings }, null, 2)],
        { type: "application/json" },
      );
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `uptime-backup-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      setMsg({ ok: true, text: t("set.backupDone") });
    } catch (e: any) {
      setMsg({ ok: false, text: e.message || t("set.saveFailed") });
    } finally {
      setBusy(false);
    }
  }

  async function purge() {
    if (purgeDays <= 0) {
      setMsg({ ok: false, text: t("set.purgeInvalid") });
      return;
    }
    if (!confirm(t("set.purgeConfirm", { days: purgeDays }))) return;
    setBusy(true);
    setMsg(null);
    try {
      const res = await api.purgeHistory(purgeDays);
      setMsg({ ok: true, text: t("set.purgeDone", { n: res.deleted.toLocaleString() }) });
      loadCounts();
    } catch (e: any) {
      setMsg({ ok: false, text: e.message || t("set.saveFailed") });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="max-w-lg space-y-6">
      {error && <div className="text-app-down text-sm">{error}</div>}

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5">
        <h2 className="text-lg font-semibold mb-3">{t("set.statsTitle")}</h2>
        {counts ? (
          <div className="grid grid-cols-3 gap-3 text-center">
            <div>
              <div className="text-2xl font-bold tabular-nums">{counts.monitors}</div>
              <div className="text-xs text-slate-400 uppercase">{t("set.statMonitors")}</div>
            </div>
            <div>
              <div className="text-2xl font-bold tabular-nums">{counts.channels}</div>
              <div className="text-xs text-slate-400 uppercase">{t("set.statChannels")}</div>
            </div>
            <div>
              <div className="text-2xl font-bold tabular-nums">{counts.checks.toLocaleString()}</div>
              <div className="text-xs text-slate-400 uppercase">{t("set.statCheckRows")}</div>
            </div>
          </div>
        ) : (
          <div className="text-slate-500 text-sm">{t("common.loading")}</div>
        )}
      </div>

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3">
        <h2 className="text-lg font-semibold">{t("set.backupTitle")}</h2>
        <p className="text-sm text-slate-400">
          {t("set.backupDesc")}
        </p>
        <button className="btn-secondary" disabled={busy} onClick={exportAll}>
          {t("set.backupExport")}
        </button>
      </div>

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3">
        <h2 className="text-lg font-semibold text-red-300">{t("set.dangerTitle")}</h2>
        <p className="text-sm text-slate-400">
          {t("set.dangerDesc")}
        </p>
        <div className="flex items-center gap-2">
          <input
            type="number"
            className="input w-28"
            min={1}
            value={purgeDays}
            onChange={(e) => setPurgeDays(Number(e.target.value))}
          />
          <span className="text-sm text-slate-400">{t("set.daysUnit")} </span>
          <button className="btn-danger" disabled={busy} onClick={purge}>
            {busy ? t("set.working") : t("set.purge")}
          </button>
        </div>
      </div>

      {msg && <div className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}>{msg.text}</div>}
    </div>
  );
}

// ---------- Tampilan ----------

function AppearanceTab() {
  const { t, lang, setLanguage } = useI18n();
  const [prefs, setPrefsState] = useState<Preferences>(getPrefs());

  function update(patch: Partial<Preferences>) {
    setPrefsState(setPrefs(patch));
  }

  return (
    <div className="max-w-lg space-y-4">
      <h2 className="text-lg font-semibold">{t("set.appearanceTitle")}</h2>

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <label className="block text-sm">
          <span className="text-slate-300">{t("set.language")}</span>
          <span className="block text-xs text-slate-500 mb-1">{t("set.languageDesc")}</span>
          <select
            className="input mt-1"
            value={lang}
            onChange={(e) => setLanguage(e.target.value as "id" | "en")}
          >
            <option value="id">{t("set.langId")}</option>
            <option value="en">{t("set.langEn")}</option>
          </select>
        </label>

        <label className="block text-sm">
          <span className="text-slate-300">{t("set.uptimeWindow")}</span>
          <select
            className="input mt-1"
            value={prefs.uptimeWindowDays}
            onChange={(e) => update({ uptimeWindowDays: Number(e.target.value) })}
          >
            <option value={7}>{t("set.windowDays", { n: 7 })}</option>
            <option value={30}>{t("set.windowDays", { n: 30 })}</option>
            <option value={90}>{t("set.windowDays", { n: 90 })}</option>
          </select>
        </label>

        <label className="flex items-start gap-3 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            checked={prefs.hidePaused}
            onChange={(e) => update({ hidePaused: e.target.checked })}
          />
          <span>
            <span className="text-slate-200 block">{t("set.hidePaused")}</span>
            <span className="text-slate-500 text-xs block">
              {t("set.hidePausedDesc")}
            </span>
          </span>
        </label>

        <label className="flex items-start gap-3 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            checked={prefs.compact}
            onChange={(e) => update({ compact: e.target.checked })}
          />
          <span>
            <span className="text-slate-200 block">{t("set.compact")}</span>
            <span className="text-slate-500 text-xs block">{t("set.compactDesc")}</span>
          </span>
        </label>
      </div>
      <p className="text-xs text-slate-500">{t("set.prefsLocal")}</p>
    </div>
  );
}

// ---------- About ----------

function AboutTab() {
  return (
    <div className="max-w-lg space-y-4">
      <h2 className="text-lg font-semibold">About</h2>
      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3 text-sm">
        <div className="flex justify-between">
          <span className="text-slate-400">Application</span>
          <span>Uptime Monitoring</span>
        </div>
        <div className="flex justify-between">
          <span className="text-slate-400">Version</span>
          <span>1.0.0</span>
        </div>
        <div className="flex justify-between">
          <span className="text-slate-400">Stack</span>
          <span>Go · React · PostgreSQL · WebSocket</span>
        </div>
        <div className="flex justify-between">
          <span className="text-slate-400">Copyright</span>
          <span>© {new Date().getFullYear()} Putra_aja</span>
        </div>
      </div>
      <button
        className="btn-danger"
        onClick={() => {
          clearToken();
          window.location.href = "/login";
        }}
      >
        Log out
      </button>
    </div>
  );
}
