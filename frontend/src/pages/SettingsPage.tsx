import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import NotificationsSection from "../components/NotificationsSection";
import TemplatesSection from "../components/TemplatesSection";
import StatusPagesSection from "../components/StatusPagesSection";
import UsersSection from "../components/UsersSection";
import ReportChannelsEditor from "../components/ReportChannelsEditor";
import { api, clearToken, getRole, getUsername } from "../lib/api";
import { getPrefs, setPrefs } from "../lib/prefs";
import { useTheme, type Theme } from "../lib/theme";
import { useI18n } from "../lib/i18n";
import type { Preferences } from "../lib/prefs";
import type { SystemSettings } from "../lib/types";
import type {
  AIProvider,
  AISettingsInput,
  AISettingsView,
  ReportSettings,
} from "../lib/types";

type TabId =
  | "profile"
  | "general"
  | "ai"
  | "report"
  | "notifications"
  | "templates"
  | "statusPages"
  | "users"
  | "appearance"
  | "data"
  | "about";

interface TabDef {
  id: TabId;
  labelKey: string;
  descKey: string;
  adminOnly?: boolean;
}

const TABS: TabDef[] = [
  { id: "profile", labelKey: "set.tabProfile", descKey: "set.tabProfileDesc" },
  {
    id: "general",
    labelKey: "set.tabGeneral",
    descKey: "set.tabGeneralDesc",
    adminOnly: true,
  },
  {
    id: "ai",
    labelKey: "set.tabAI",
    descKey: "set.tabAIDesc",
    adminOnly: true,
  },
  {
    id: "report",
    labelKey: "set.tabReport",
    descKey: "set.tabReportDesc",
    adminOnly: true,
  },
  {
    id: "notifications",
    labelKey: "set.tabNotifications",
    descKey: "set.tabNotificationsDesc",
  },
  {
    id: "templates",
    labelKey: "set.tabTemplates",
    descKey: "set.tabTemplatesDesc",
    adminOnly: true,
  },
  {
    id: "statusPages",
    labelKey: "set.tabStatusPages",
    descKey: "set.tabStatusPagesDesc",
  },
  {
    id: "users",
    labelKey: "set.tabUsers",
    descKey: "set.tabUsersDesc",
    adminOnly: true,
  },
  {
    id: "appearance",
    labelKey: "set.tabAppearance",
    descKey: "set.tabAppearanceDesc",
  },
  {
    id: "data",
    labelKey: "set.tabData",
    descKey: "set.tabDataDesc",
    adminOnly: true,
  },
  { id: "about", labelKey: "set.tabAbout", descKey: "set.tabAboutDesc" },
];

export default function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const { t } = useI18n();
  const isAdmin = getRole() === "admin";
  const visibleTabs = TABS.filter((tab) => !tab.adminOnly || isAdmin);
  const paramTab = params.get("tab") as TabId | null;
  const active: TabId =
    paramTab && visibleTabs.some((tab) => tab.id === paramTab)
      ? paramTab
      : "profile";

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
              <span className="block text-sm font-medium">
                {t(tab.labelKey)}
              </span>
              <span className="block text-[11px] text-slate-500">
                {t(tab.descKey)}
              </span>
            </button>
          ))}
        </nav>

        <div className="flex-1 min-w-0">
          {active === "profile" && <ProfileTab />}
          {active === "general" && isAdmin && <GeneralTab />}
          {active === "ai" && isAdmin && <AITab />}
          {active === "report" && isAdmin && <ReportTab />}
          {active === "notifications" && <NotificationsSection />}
          {active === "templates" && isAdmin && <TemplatesSection />}
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
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [profBusy, setProfBusy] = useState(false);
  const [profMsg, setProfMsg] = useState<{ ok: boolean; text: string } | null>(
    null,
  );
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Load the stored profile (display name + email); username/role come from
  // the session token and are shown read-only.
  useEffect(() => {
    api
      .me()
      .then((m) => {
        setDisplayName(m.displayName ?? "");
        setEmail(m.email ?? "");
      })
      .catch(() => {
        /* keep fields empty; password form still works */
      });
  }, []);

  async function saveProfile(e: FormEvent) {
    e.preventDefault();
    setProfMsg(null);
    setProfBusy(true);
    try {
      await api.updateProfile(displayName.trim(), email.trim());
      setProfMsg({ ok: true, text: t("set.profileSaved") });
    } catch (err: any) {
      setProfMsg({
        ok: false,
        text: err.message || t("set.profileSaveFailed"),
      });
    } finally {
      setProfBusy(false);
    }
  }

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
    <div className="space-y-6 max-w-4xl">
      <div className="flex items-center gap-4 bg-app-panel border border-slate-800 rounded-lg p-5">
        <div className="w-14 h-14 rounded-full bg-sky-500/20 text-sky-300 flex items-center justify-center text-xl font-bold uppercase">
          {(displayName || username).slice(0, 2)}
        </div>
        <div>
          <div className="font-semibold text-lg">
            {displayName || username}
            {displayName && (
              <span className="ml-2 text-sm text-slate-500">@{username}</span>
            )}
          </div>
          {email && <div className="text-sm text-slate-400">{email}</div>}
          <span
            className={`inline-block mt-1 text-[11px] px-2 py-0.5 rounded uppercase ${
              role === "admin"
                ? "bg-sky-500/15 text-sky-300"
                : "bg-slate-700 text-slate-300"
            }`}
          >
            {role === "admin" ? t("role.admin") : t("role.viewerReadonly")}
          </span>
        </div>
      </div>

      <div className="grid md:grid-cols-2 gap-6 items-start">
        <form
          onSubmit={saveProfile}
          className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
        >
          <h2 className="text-lg font-semibold">{t("set.profileEditTitle")}</h2>
          <div className="grid sm:grid-cols-2 gap-4">
            <label className="block text-sm">
              <span className="text-slate-300">{t("set.profileName")}</span>
              <input
                type="text"
                className="input mt-1"
                placeholder={username}
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            </label>
            <label className="block text-sm">
              <span className="text-slate-300">{t("set.profileEmail")}</span>
              <input
                type="email"
                className="input mt-1"
                placeholder="nama@perusahaan.co.id"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
          </div>
          <p className="text-xs text-slate-500">
            {t("set.profileUsernameNote")}
          </p>
          {profMsg && (
            <div
              className={`text-sm ${profMsg.ok ? "text-green-400" : "text-app-down"}`}
            >
              {profMsg.text}
            </div>
          )}
          <button disabled={profBusy} className="btn-primary">
            {profBusy ? t("common.saving") : t("set.profileSave")}
          </button>
        </form>

        <form
          onSubmit={submit}
          className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
        >
          <h2 className="text-lg font-semibold">{t("set.changePw")}</h2>
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.pwCurrent")}</span>
            <input
              type="password"
              className="input mt-1"
              value={cur}
              onChange={(e) => setCur(e.target.value)}
              required
            />
          </label>
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.pwNew")}</span>
            <input
              type="password"
              className="input mt-1"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              required
            />
          </label>
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.pwConfirm")}</span>
            <input
              type="password"
              className="input mt-1"
              value={confirmPw}
              onChange={(e) => setConfirmPw(e.target.value)}
              required
            />
          </label>
          {msg && (
            <div
              className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}
            >
              {msg.text}
            </div>
          )}
          <button disabled={busy} className="btn-primary">
            {busy ? t("common.saving") : t("set.pwUpdate")}
          </button>
        </form>
      </div>
    </div>
  );
}

// ---------- AI (LLM provider settings, admin) ----------

function AITab() {
  const { t } = useI18n();
  const [providers, setProviders] = useState<AIProvider[]>([]);
  const [form, setForm] = useState<AISettingsInput>({
    enabled: false,
    provider: "",
    baseUrl: "",
    model: "",
    apiKey: "",
  });
  const [view, setView] = useState<AISettingsView | null>(null);
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.listAIProviders(), api.getAISettings()])
      .then(([ps, v]) => {
        setProviders(ps);
        setView(v);
        setForm({
          enabled: v.enabled,
          provider: v.provider || ps[0]?.id || "",
          baseUrl: v.baseUrl,
          model: v.model,
          apiKey: "",
        });
      })
      .catch((e) => setError(e.message || t("set.aiLoadFailed")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const patch = (p: Partial<AISettingsInput>) =>
    setForm((s) => ({ ...s, ...p }));

  // Choosing a preset fills its base URL + default model; "custom" leaves them
  // for the admin to edit.
  function onProviderChange(id: string) {
    const preset = providers.find((p) => p.id === id);
    if (preset && id !== "custom") {
      patch({
        provider: id,
        baseUrl: preset.baseUrl,
        model: preset.defaultModel,
      });
    } else {
      patch({ provider: id });
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const res = await api.saveAISettings(form);
      setView((v) =>
        v
          ? {
              ...v,
              hasKey: res.hasKey,
              keyPreview: res.keyPreview,
              enabled: form.enabled,
              provider: form.provider,
              baseUrl: form.baseUrl,
              model: form.model,
            }
          : v,
      );
      setForm((f) => ({ ...f, apiKey: "" }));
      setMsg({ ok: true, text: t("set.aiSaved") });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.aiSaveFailed") });
    } finally {
      setBusy(false);
    }
  }

  async function test() {
    setTesting(true);
    setMsg(null);
    try {
      const res = await api.testAISettings(form);
      setMsg({
        ok: true,
        text: t("set.aiTestOk", { model: res.model, reply: res.reply }),
      });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.aiTestFailed") });
    } finally {
      setTesting(false);
    }
  }

  if (error) return <div className="text-app-down text-sm">{error}</div>;
  if (!view)
    return <div className="text-slate-500 text-sm">{t("common.loading")}</div>;

  const isCustom = form.provider === "custom";

  return (
    <form onSubmit={save} className="max-w-4xl space-y-6">
      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold">{t("set.aiTitle")}</h2>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => patch({ enabled: e.target.checked })}
            />
            <span className="text-slate-300">{t("set.aiEnable")}</span>
          </label>
        </div>

        <div className="grid md:grid-cols-2 gap-4">
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.aiProvider")}</span>
            <select
              className="input mt-1"
              value={form.provider}
              onChange={(e) => onProviderChange(e.target.value)}
            >
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>

          <label className="block text-sm">
            <span className="text-slate-300">{t("set.aiModel")}</span>
            <input
              className="input mt-1"
              value={form.model}
              onChange={(e) => patch({ model: e.target.value })}
              placeholder={t("set.aiModelPlaceholder")}
            />
          </label>

          <label className="block text-sm">
            <span className="text-slate-300">{t("set.aiBaseUrl")}</span>
            <input
              className="input mt-1"
              value={form.baseUrl}
              onChange={(e) => patch({ baseUrl: e.target.value })}
              disabled={!isCustom}
              placeholder="https://.../v1"
            />
            {!isCustom && (
              <span className="text-xs text-slate-500">
                {t("set.aiBaseUrlLocked")}
              </span>
            )}
          </label>

          <label className="block text-sm">
            <span className="text-slate-300">{t("set.aiApiKey")}</span>
            <input
              type="password"
              className="input mt-1"
              value={form.apiKey}
              onChange={(e) => patch({ apiKey: e.target.value })}
              placeholder={
                view.hasKey ? view.keyPreview : t("set.aiApiKeyPlaceholder")
              }
              autoComplete="off"
            />
            {view.hasKey && (
              <span className="text-xs text-slate-500">
                {t("set.aiKeyStored")}
              </span>
            )}
          </label>
        </div>
      </div>

      {msg && (
        <div
          className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}
        >
          {msg.text}
        </div>
      )}

      <div className="flex items-center gap-3">
        <button type="submit" disabled={busy} className="btn-primary">
          {busy ? t("common.saving") : t("set.aiSave")}
        </button>
        <button
          type="button"
          disabled={testing}
          onClick={test}
          className="btn-secondary"
        >
          {testing ? t("set.aiTesting") : t("set.aiTest")}
        </button>
      </div>
    </form>
  );
}

// ---------- Laporan berkala (admin) ----------

const WEEKDAYS = [
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
  "Sunday",
];

type Frequency = "daily" | "weekly" | "monthly";

type ReportForm = Omit<ReportSettings, "frequency"> & { frequency: Frequency };

// Default report message templates (mirror backend report.DefaultSubjectTemplate
// and report.DefaultBodyTemplate). Offered via the "fill default" button so
// admins always start from a working layout.
export const REPORT_DEFAULT_SUBJECT = "UPTIME REPORT — {period}";
export const REPORT_DEFAULT_BODY =
  "UPTIME REPORT\nPeriode : {period}\nLayanan : {services} | rata-rata uptime {avg}%\n\nTop layanan bermasalah (uptime terendah):\n{top}\n\nTabel lengkap (nama, IP public, IP lokal, uptime, down, SLA) terlampir sebagai file.\nDikirim otomatis oleh Uptime Monitoring.";

// Fills backend zero values (fresh install stores an empty JSON object) with
// sensible defaults for the editor.
function normalizeReport(s: ReportSettings): ReportForm {
  return {
    ...s,
    frequency: s.frequency || "weekly",
    sendTime: s.sendTime || "09:00",
    weeklyDay: s.weeklyDay || "Monday",
    monthlyDay: s.monthlyDay >= 1 ? s.monthlyDay : 1,
    channels: s.channels ?? [],
    sendFile: s.sendFile ?? true,
    fileFormat: s.fileFormat || "xlsx",
    subjectTemplate: s.subjectTemplate ?? "",
    bodyTemplate: s.bodyTemplate ?? "",
  };
}

function ReportTab() {
  const { t } = useI18n();
  const [form, setForm] = useState<ReportForm | null>(null);
  const [busy, setBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .getReportSettings()
      .then((s) => setForm(normalizeReport(s)))
      .catch((e) => setError(e.message || t("set.reportLoadFailed")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const patch = (p: Partial<ReportSettings>) =>
    setForm((s) =>
      s
        ? {
            ...s,
            ...p,
            frequency: (p.frequency ?? s.frequency) as Frequency,
          }
        : s,
    );

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!form) return;
    setBusy(true);
    setMsg(null);
    try {
      const saved = await api.saveReportSettings(form);
      setForm(normalizeReport(saved));
      setMsg({ ok: true, text: t("set.reportSaved") });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.reportSaveFailed") });
    } finally {
      setBusy(false);
    }
  }

  async function sendNow() {
    if (!form) return;
    setSending(true);
    setMsg(null);
    try {
      // Persist the current choices first so "send now" always reflects what
      // is on screen: the per-channel test uses the in-form config, while the
      // backend send-now reads the saved settings from the database.
      const saved = await api.saveReportSettings(form);
      setForm(normalizeReport(saved));
      const res = await api.sendReportNow();
      setMsg({
        ok: true,
        text: t("set.reportSentAll", { subject: res.subject }),
      });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.reportSendFailed") });
    } finally {
      setSending(false);
    }
  }

  if (error) return <div className="text-app-down text-sm">{error}</div>;
  if (!form)
    return <div className="text-slate-500 text-sm">{t("common.loading")}</div>;

  return (
    <form onSubmit={save} className="max-w-5xl space-y-6">
      <div className="bg-app-panel border border-slate-800 rounded-lg p-5">
        <h2 className="text-lg font-semibold">{t("set.reportTitle")}</h2>
        <p className="text-xs text-slate-500 mt-1 mb-4">
          {t("set.reportFileNote")}
        </p>
        <div className="grid md:grid-cols-2 gap-x-6 gap-y-4 items-start">
          <div className="space-y-4">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) => patch({ enabled: e.target.checked })}
              />
              <span className="text-slate-300">{t("set.reportEnable")}</span>
            </label>

            <div className="grid grid-cols-2 gap-4">
              <label className="block text-sm">
                <span className="text-slate-300">
                  {t("set.reportFrequency")}
                </span>
                <select
                  className="input mt-1"
                  value={form.frequency}
                  onChange={(e) =>
                    patch({
                      frequency: e.target.value as Frequency,
                    })
                  }
                >
                  <option value="daily">{t("set.reportFreqDaily")}</option>
                  <option value="weekly">{t("set.reportFreqWeekly")}</option>
                  <option value="monthly">{t("set.reportFreqMonthly")}</option>
                </select>
              </label>

              <label className="block text-sm">
                <span className="text-slate-300">
                  {t("set.reportSendTime")}
                </span>
                <input
                  type="time"
                  className="input mt-1"
                  value={form.sendTime}
                  onChange={(e) => patch({ sendTime: e.target.value })}
                />
              </label>
            </div>

            {form.frequency === "weekly" && (
              <label className="block text-sm">
                <span className="text-slate-300">{t("set.reportWeekday")}</span>
                <select
                  className="input mt-1"
                  value={form.weeklyDay}
                  onChange={(e) => patch({ weeklyDay: e.target.value })}
                >
                  {WEEKDAYS.map((d) => (
                    <option key={d} value={d}>
                      {t(`set.reportDay${d.slice(0, 3)}`)}
                    </option>
                  ))}
                </select>
              </label>
            )}

            {form.frequency === "monthly" && (
              <label className="block text-sm">
                <span className="text-slate-300">
                  {t("set.reportMonthDay")}
                </span>
                <input
                  type="number"
                  min={1}
                  max={28}
                  className="input mt-1"
                  value={form.monthlyDay}
                  onChange={(e) =>
                    patch({
                      monthlyDay: Math.min(
                        28,
                        Math.max(1, Number(e.target.value) || 1),
                      ),
                    })
                  }
                />
              </label>
            )}

            <div className="space-y-2">
              <span className="text-sm font-semibold text-slate-300">
                {t("set.reportChannels")}
              </span>
              <ReportChannelsEditor
                channels={form.channels ?? []}
                onChange={(next) => patch({ channels: next })}
              />
            </div>

            <div className="space-y-2">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.sendFile}
                  onChange={(e) => patch({ sendFile: e.target.checked })}
                />
                <span className="text-slate-300">
                  {t("set.reportAttachFile")}
                </span>
              </label>
              {form.sendFile && (
                <label className="block text-sm">
                  <span className="text-slate-300">
                    {t("set.reportFileFormat")}
                  </span>
                  <select
                    className="input mt-1"
                    value={form.fileFormat}
                    onChange={(e) =>
                      patch({
                        fileFormat: e.target.value as ReportForm["fileFormat"],
                      })
                    }
                  >
                    <option value="xlsx">{t("set.reportFmtXlsx")}</option>
                    <option value="csv">{t("set.reportFmtCsv")}</option>
                  </select>
                </label>
              )}
            </div>
          </div>

          <div className="space-y-4">
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-sm font-semibold text-slate-300">
                  {t("set.reportMsgTitle")}
                </span>
                <button
                  type="button"
                  className="btn-secondary text-xs px-2 py-1"
                  onClick={() =>
                    patch({
                      subjectTemplate: REPORT_DEFAULT_SUBJECT,
                      bodyTemplate: REPORT_DEFAULT_BODY,
                    })
                  }
                >
                  {t("set.reportMsgFillDefault")}
                </button>
              </div>
              <p className="text-xs text-slate-500">{t("set.reportMsgVars")}</p>
              <label className="block text-sm">
                <span className="text-slate-300">
                  {t("set.reportMsgSubject")}
                </span>
                <input
                  className="input mt-1"
                  type="text"
                  placeholder={REPORT_DEFAULT_SUBJECT}
                  value={form.subjectTemplate}
                  onChange={(e) => patch({ subjectTemplate: e.target.value })}
                />
              </label>
              <label className="block text-sm">
                <span className="text-slate-300">{t("set.reportMsgBody")}</span>
                <textarea
                  className="input mt-1 font-mono text-xs"
                  rows={9}
                  placeholder={REPORT_DEFAULT_BODY}
                  value={form.bodyTemplate}
                  onChange={(e) => patch({ bodyTemplate: e.target.value })}
                />
              </label>
            </div>
          </div>
        </div>

        <div className="border-t border-slate-800 mt-4 pt-4 space-y-4">
          {form.lastSentAt && (
            <p className="text-xs text-slate-500">
              {t("set.reportLastSent")}:{" "}
              {new Date(form.lastSentAt).toLocaleString()}
            </p>
          )}

          {msg && (
            <div
              className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}
            >
              {msg.text}
            </div>
          )}

          <div className="flex items-center gap-3">
            <button type="submit" disabled={busy} className="btn-primary">
              {busy ? t("common.saving") : t("set.reportSave")}
            </button>
            <button
              type="button"
              disabled={sending}
              onClick={sendNow}
              className="btn-secondary"
            >
              {sending ? t("set.reportSending") : t("set.reportSendNow")}
            </button>
          </div>
        </div>
      </div>
    </form>
  );
}

// ---------- Umum (server-wide settings, admin) ----------

function GeneralTab() {
  const { t } = useI18n();
  const [set, setSet] = useState<SystemSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .getSettings()
      .then(setSet)
      .catch((e) => setError(e.message || t("set.failedLoadSettings")));
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

  async function applyAll() {
    if (!set) return;
    if (!window.confirm(t("set.applyAllConfirm"))) return;
    setApplying(true);
    setMsg(null);
    try {
      // persist first so the server applies exactly what is on screen
      setSet(await api.saveSettings(set));
      const res = await api.applyDefaults();
      setMsg({ ok: true, text: t("set.applyAllDone", { count: res.updated }) });
    } catch (err: any) {
      setMsg({ ok: false, text: err.message || t("set.applyAllFailed") });
    } finally {
      setApplying(false);
    }
  }

  if (error) return <div className="text-app-down text-sm">{error}</div>;
  if (!set)
    return <div className="text-slate-500 text-sm">{t("common.loading")}</div>;

  const patch = (p: Partial<SystemSettings>) =>
    setSet((s) => (s ? { ...s, ...p } : s));

  return (
    <form onSubmit={submit} className="max-w-4xl space-y-6">
      <div className="grid md:grid-cols-2 gap-6 items-start">
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
                onChange={(e) =>
                  patch({ defaultCheckIntervalSec: Number(e.target.value) })
                }
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
                onChange={(e) =>
                  patch({ defaultTimeoutSec: Number(e.target.value) })
                }
                required
              />
            </label>
            <label className="block text-sm">
              <span className="text-slate-300">{t("set.defaultSlo")}</span>
              <input
                type="number"
                className="input mt-1"
                min={0}
                max={100}
                step={0.01}
                list="slo-preset"
                value={set.defaultSloTargetPct}
                onChange={(e) =>
                  patch({ defaultSloTargetPct: Number(e.target.value) })
                }
              />
              <datalist id="slo-preset">
                <option value="99" />
                <option value="99.5" />
                <option value="99.6" />
                <option value="99.9" />
                <option value="99.95" />
                <option value="99.99" />
                <option value="100" />
              </datalist>
            </label>
          </div>
          <p className="text-xs text-slate-500">{t("set.defaultsHint")}</p>
          <div className="flex items-center gap-3 pt-1">
            <button
              type="button"
              disabled={applying || busy}
              onClick={applyAll}
              className="btn-secondary"
            >
              {applying ? t("set.applyAllBusy") : t("set.applyAll")}
            </button>
            <span className="text-xs text-slate-500">
              {t("set.applyAllHint")}
            </span>
          </div>
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
      </div>

      {msg && (
        <div
          className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}
        >
          {msg.text}
        </div>
      )}
      <button disabled={busy} className="btn-primary">
        {busy ? t("common.saving") : t("set.saveSettings")}
      </button>
    </form>
  );
}

// ---------- Data (backup & riwayat, admin) ----------

function DataTab() {
  const { t } = useI18n();
  const [counts, setCounts] = useState<{
    checks: number;
    monitors: number;
    channels: number;
  } | null>(null);
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
      setCounts({
        checks: checks.count,
        monitors: monitors.length,
        channels: channels.length,
      });
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
      const [monitors, notifications, escalations, statusPages, settings] =
        await Promise.all([
          api.listMonitors(),
          api.listNotifications(),
          api.listEscalations(),
          api.listStatusPages(),
          api.getSettings(),
        ]);
      const blob = new Blob(
        [
          JSON.stringify(
            {
              exportedAt: new Date().toISOString(),
              monitors,
              notifications,
              escalations,
              statusPages,
              settings,
            },
            null,
            2,
          ),
        ],
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
      setMsg({
        ok: true,
        text: t("set.purgeDone", { n: res.deleted.toLocaleString() }),
      });
      loadCounts();
    } catch (e: any) {
      setMsg({ ok: false, text: e.message || t("set.saveFailed") });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="max-w-5xl space-y-6">
      {error && <div className="text-app-down text-sm">{error}</div>}

      <div className="grid md:grid-cols-3 gap-6 items-start">
        <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3">
          <h2 className="text-lg font-semibold mb-3">{t("set.statsTitle")}</h2>
          {counts ? (
            <div className="grid grid-cols-3 gap-3 text-center">
              <div>
                <div className="text-2xl font-bold tabular-nums">
                  {counts.monitors}
                </div>
                <div className="text-xs text-slate-400 uppercase">
                  {t("set.statMonitors")}
                </div>
              </div>
              <div>
                <div className="text-2xl font-bold tabular-nums">
                  {counts.channels}
                </div>
                <div className="text-xs text-slate-400 uppercase">
                  {t("set.statChannels")}
                </div>
              </div>
              <div>
                <div className="text-2xl font-bold tabular-nums">
                  {counts.checks.toLocaleString()}
                </div>
                <div className="text-xs text-slate-400 uppercase">
                  {t("set.statCheckRows")}
                </div>
              </div>
            </div>
          ) : (
            <div className="text-slate-500 text-sm">{t("common.loading")}</div>
          )}
        </div>

        <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3">
          <h2 className="text-lg font-semibold">{t("set.backupTitle")}</h2>
          <p className="text-sm text-slate-400">{t("set.backupDesc")}</p>
          <button className="btn-secondary" disabled={busy} onClick={exportAll}>
            {t("set.backupExport")}
          </button>
        </div>

        <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-3">
          <h2 className="text-lg font-semibold text-red-300">
            {t("set.dangerTitle")}
          </h2>
          <p className="text-sm text-slate-400">{t("set.dangerDesc")}</p>
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
      </div>

      {msg && (
        <div
          className={`text-sm ${msg.ok ? "text-green-400" : "text-app-down"}`}
        >
          {msg.text}
        </div>
      )}
    </div>
  );
}

// ---------- Tampilan ----------

function AppearanceTab() {
  const { t, lang, setLanguage } = useI18n();
  const { theme, setTheme } = useTheme();
  const [prefs, setPrefsState] = useState<Preferences>(getPrefs());

  function update(patch: Partial<Preferences>) {
    setPrefsState(setPrefs(patch));
  }

  return (
    <div className="max-w-4xl space-y-4">
      <h2 className="text-lg font-semibold">{t("set.appearanceTitle")}</h2>

      <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
        <div className="md:grid md:grid-cols-3 md:gap-4 md:space-y-0 space-y-4">
          <label className="block text-sm">
            <span className="text-slate-300">{t("set.theme")}</span>
            <span className="block text-xs text-slate-500 mb-1">
              {t("set.themeDesc")}
            </span>
            <select
              className="input mt-1"
              value={theme}
              onChange={(e) => setTheme(e.target.value as Theme)}
            >
              <option value="default">{t("set.themeDefault")}</option>
              <option value="metro">{t("set.themeMetro")}</option>
              <option value="material">{t("set.themeMaterial")}</option>
              <option value="ios">{t("set.themeIos")}</option>
              <option value="aero">{t("set.themeAero")}</option>
              <option value="macos">{t("set.themeMacos")}</option>
            </select>
          </label>

          <label className="block text-sm">
            <span className="text-slate-300">{t("set.language")}</span>
            <span className="block text-xs text-slate-500 mb-1">
              {t("set.languageDesc")}
            </span>
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
              onChange={(e) =>
                update({ uptimeWindowDays: Number(e.target.value) })
              }
            >
              <option value={7}>{t("set.windowDays", { n: 7 })}</option>
              <option value={30}>{t("set.windowDays", { n: 30 })}</option>
              <option value={90}>{t("set.windowDays", { n: 90 })}</option>
            </select>
          </label>
        </div>

        <div className="md:grid md:grid-cols-2 md:gap-4 md:space-y-0 space-y-3">
          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              className="mt-1"
              checked={prefs.hidePaused}
              onChange={(e) => update({ hidePaused: e.target.checked })}
            />
            <span>
              <span className="text-slate-200 block">
                {t("set.hidePaused")}
              </span>
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
              <span className="text-slate-500 text-xs block">
                {t("set.compactDesc")}
              </span>
            </span>
          </label>
        </div>
      </div>
      <p className="text-xs text-slate-500">{t("set.prefsLocal")}</p>
    </div>
  );
}

// ---------- About ----------

function AboutTab() {
  return (
    <div className="max-w-2xl space-y-4">
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
