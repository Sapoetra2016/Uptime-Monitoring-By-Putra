import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";
import { api } from "../lib/api";
import type { AlertTemplate, Notification } from "../lib/types";

// labelKey/hintKey resolve through the locale dictionaries at render time.
const typeFields: Record<
  Notification["type"],
  { key: string; labelKey: string; hintKey?: string }[]
> = {
  telegram: [
    { key: "botToken", labelKey: "nf.botToken" },
    { key: "chatId", labelKey: "nf.chatId" },
  ],
  discord: [{ key: "webhookUrl", labelKey: "nf.webhookUrl" }],
  slack: [{ key: "webhookUrl", labelKey: "nf.slackUrl" }],
  webhook: [
    { key: "url", labelKey: "form.url" },
    { key: "method", labelKey: "nf.method", hintKey: "nf.methodHint" },
  ],
  email: [
    { key: "smtpHost", labelKey: "nf.smtpHost" },
    { key: "smtpPort", labelKey: "nf.smtpPort" },
    { key: "username", labelKey: "login.username" },
    { key: "password", labelKey: "login.password" },
    { key: "from", labelKey: "nf.from" },
    { key: "to", labelKey: "nf.to" },
    { key: "useTls", labelKey: "nf.useTls" },
  ],
};

export default function NotificationForm({
  initial,
  onSave,
  onCancel,
  saving,
}: {
  initial?: Partial<Notification>;
  onSave: (n: Partial<Notification>) => void;
  onCancel: () => void;
  saving: boolean;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [type, setType] = useState<Notification["type"]>(
    initial?.type ?? "telegram",
  );
  const [config, setConfig] = useState<Record<string, any>>(
    initial?.config ?? {},
  );
  const [templateId, setTemplateId] = useState(initial?.templateId ?? "");
  const [templates, setTemplates] = useState<AlertTemplate[]>([]);
  const [showEditor, setShowEditor] = useState(false);
  const [draft, setDraft] = useState<AlertTemplate | null>(null);
  const [tplSaving, setTplSaving] = useState(false);
  const [tplMsg, setTplMsg] = useState("");
  const [testMsg, setTestMsg] = useState("");
  const [testing, setTesting] = useState(false);
  const { t } = useI18n();

  useEffect(() => {
    setName(initial?.name ?? "");
    setType(initial?.type ?? "telegram");
    setConfig(initial?.config ?? {});
    setTemplateId(initial?.templateId ?? "");
  }, [initial]);

  // Load the template library once so the channel-matching options can be
  // offered below.
  useEffect(() => {
    api
      .listTemplates()
      .then(setTemplates)
      .catch(() => setTemplates([]));
  }, []);

  const channel = type === "email" ? "email" : "chat";
  const channelTemplates = templates.filter((x) => x.channel === channel);

  // Open the inline editor for the template this notifier uses (the selected
  // one, else the channel default). Edits are written back to the shared
  // library so they take effect everywhere, exactly like the Settings tab.
  function startEdit() {
    const target =
      channelTemplates.find((x) => x.id === templateId) ??
      channelTemplates.find((x) => x.isDefault) ??
      channelTemplates[0] ??
      null;
    setDraft(
      target
        ? { ...target }
        : {
            id: "new-" + Date.now(),
            name: "",
            channel,
            subject: channel === "email" ? "[{status}] {monitor}" : "",
            bodyDown: "{monitor} is DOWN: {message}",
            bodyUp: "{monitor} is UP again",
            bodyDegraded: "{monitor} is DEGRADED: {message}",
            bodyEscalation:
              "[ESCALATION step {step}] {monitor} down {duration}",
            isDefault: true,
          },
    );
    setTplMsg("");
    setShowEditor(true);
  }

  function patchDraft(p: Partial<AlertTemplate>) {
    setDraft((d) => (d ? { ...d, ...p } : d));
    setTplMsg("");
  }

  async function saveDraft() {
    if (!draft) return;
    setTplSaving(true);
    setTplMsg("");
    const prevIds = new Set(templates.map((x) => x.id));
    const isNew = !draft.id || draft.id.startsWith("new-");
    const clean: AlertTemplate = { ...draft, id: isNew ? "" : draft.id };
    const next = isNew
      ? [...templates, clean]
      : templates.map((x) => (x.id === draft.id ? clean : x));
    try {
      const res = await api.saveTemplates(next);
      setTemplates(res);
      // Newly created template: auto-select it for this notifier. Editing an
      // existing one keeps the notifier's current selection untouched.
      if (isNew) {
        const added = res.find(
          (x) => !prevIds.has(x.id) && x.channel === channel,
        );
        if (added) setTemplateId(added.id);
      }
      setShowEditor(false);
    } catch (e: any) {
      setTplMsg(e?.message || t("tpl.saveFailed"));
    } finally {
      setTplSaving(false);
    }
  }

  function changeType(t: Notification["type"]) {
    setType(t);
    setConfig({});
    setTestMsg("");
  }

  // Send a test alert through the current (unsaved) config so the user can
  // verify credentials before saving the channel.
  async function runTest() {
    setTesting(true);
    setTestMsg(t("notif.sending"));
    try {
      await api.testNotificationPreview({ name, type, config });
      setTestMsg(t("notif.sent"));
    } catch (e: any) {
      setTestMsg(t("notif.sendFailed", { msg: e?.message ?? "" }));
    } finally {
      setTesting(false);
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave({ id: initial?.id, name, type, config, templateId });
      }}
      className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
    >
      <div className="grid grid-cols-2 gap-4">
        <label className="block text-sm">
          <span className="text-slate-300">{t("form.name")}</span>
          <input
            className="input mt-1"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("form.type")}</span>
          <select
            className="input mt-1"
            value={type}
            onChange={(e) => changeType(e.target.value as Notification["type"])}
          >
            <option value="telegram">{t("nf.typeTelegram")}</option>
            <option value="discord">{t("nf.typeDiscord")}</option>
            <option value="slack">{t("nf.typeSlack")}</option>
            <option value="webhook">{t("nf.typeWebhook")}</option>
            <option value="email">{t("nf.typeEmail")}</option>
          </select>
        </label>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {typeFields[type].map((f) => {
          if (f.key === "useTls") {
            return (
              <label
                key={f.key}
                className="flex items-center gap-2 text-sm col-span-2"
              >
                <input
                  type="checkbox"
                  checked={!!config[f.key]}
                  onChange={(e) =>
                    setConfig((c) => ({ ...c, [f.key]: e.target.checked }))
                  }
                />
                {t(f.labelKey)}
              </label>
            );
          }
          return (
            <label key={f.key} className="block text-sm">
              <span className="text-slate-300">{t(f.labelKey)}</span>
              {f.hintKey && (
                <span className="block text-xs text-slate-500">
                  {t(f.hintKey)}
                </span>
              )}
              <input
                className="input mt-1"
                type={f.key === "password" ? "password" : "text"}
                value={config[f.key] ?? ""}
                onChange={(e) =>
                  setConfig((c) => ({ ...c, [f.key]: e.target.value }))
                }
              />
            </label>
          );
        })}
      </div>

      <div>
        <label className="block text-sm">
          <span className="text-slate-300">{t("nf.template")}</span>
          <span className="block text-xs text-slate-500">
            {t("nf.templateHint")}
          </span>
        </label>
        <div className="mt-1 flex items-center gap-2">
          <select
            className="input flex-1"
            value={templateId}
            onChange={(e) => setTemplateId(e.target.value)}
          >
            <option value="">{t("nf.templateDefault")}</option>
            {channelTemplates.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name}
                {x.isDefault ? ` (${t("nf.templateIsDefault")})` : ""}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => (showEditor ? setShowEditor(false) : startEdit())}
            className="btn-secondary whitespace-nowrap"
          >
            {showEditor ? t("common.cancel") : t("nf.editTemplate")}
          </button>
        </div>

        {showEditor && draft && (
          <div className="mt-3 border border-slate-800 rounded-lg p-4 space-y-3 bg-slate-900/40">
            <p className="text-xs text-slate-500">{t("nf.editingTitle")}</p>
            <label className="block text-sm">
              <span className="text-slate-300">{t("tpl.name")}</span>
              <input
                className="input mt-1"
                value={draft.name}
                onChange={(e) => patchDraft({ name: e.target.value })}
              />
            </label>
            {draft.channel === "email" && (
              <label className="block text-sm">
                <span className="text-slate-300">{t("tpl.subject")}</span>
                <input
                  className="input mt-1"
                  value={draft.subject ?? ""}
                  onChange={(e) => patchDraft({ subject: e.target.value })}
                />
              </label>
            )}
            {(
              "bodyDown bodyUp bodyDegraded bodyEscalation".split(
                " ",
              ) as (keyof AlertTemplate)[]
            ).map((k) => (
              <label key={k as string} className="block text-sm">
                <span className="text-slate-300">{t("tpl." + String(k))}</span>
                <textarea
                  className="input mt-1 font-mono text-xs"
                  rows={2}
                  value={String(draft[k] ?? "")}
                  onChange={(e) =>
                    patchDraft({
                      [k as string]: e.target.value,
                    } as Partial<AlertTemplate>)
                  }
                />
              </label>
            ))}
            <p className="text-xs text-slate-500">
              {t("tpl.placeholdersDesc")}
            </p>
            {tplMsg && <p className="text-xs text-rose-400">{tplMsg}</p>}
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setShowEditor(false)}
                className="btn-secondary"
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                onClick={saveDraft}
                disabled={tplSaving}
                className="btn-primary"
              >
                {tplSaving ? t("common.saving") : t("tpl.saveAll")}
              </button>
            </div>
          </div>
        )}
      </div>

      <div className="flex items-center justify-end gap-2">
        <div className="mr-auto text-xs text-slate-400">{testMsg}</div>
        <button
          type="button"
          onClick={runTest}
          disabled={testing || !name.trim()}
          className="btn-secondary"
          title={t("nf.testHint")}
        >
          {t("nf.test")}
        </button>
        <button type="button" onClick={onCancel} className="btn-secondary">
          {t("common.cancel")}
        </button>
        <button type="submit" disabled={saving} className="btn-primary">
          {saving ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </form>
  );
}
