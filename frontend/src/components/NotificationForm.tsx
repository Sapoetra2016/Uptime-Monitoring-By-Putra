import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";
import type { Notification } from "../lib/types";

// labelKey/hintKey resolve through the locale dictionaries at render time.
const typeFields: Record<Notification["type"], { key: string; labelKey: string; hintKey?: string }[]> = {
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
  const [type, setType] = useState<Notification["type"]>(initial?.type ?? "telegram");
  const [config, setConfig] = useState<Record<string, any>>(initial?.config ?? {});
  const { t } = useI18n();

  useEffect(() => {
    setName(initial?.name ?? "");
    setType(initial?.type ?? "telegram");
    setConfig(initial?.config ?? {});
  }, [initial]);

  function changeType(t: Notification["type"]) {
    setType(t);
    setConfig({});
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave({ id: initial?.id, name, type, config });
      }}
      className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
    >
      <div className="grid grid-cols-2 gap-4">
        <label className="block text-sm">
          <span className="text-slate-300">{t("form.name")}</span>
          <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("form.type")}</span>
          <select className="input mt-1" value={type} onChange={(e) => changeType(e.target.value as Notification["type"])}>
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
              <label key={f.key} className="flex items-center gap-2 text-sm col-span-2">
                <input
                  type="checkbox"
                  checked={!!config[f.key]}
                  onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.checked }))}
                />
                {t(f.labelKey)}
              </label>
            );
          }
          return (
            <label key={f.key} className="block text-sm">
              <span className="text-slate-300">{t(f.labelKey)}</span>
              {f.hintKey && <span className="block text-xs text-slate-500">{t(f.hintKey)}</span>}
              <input
                className="input mt-1"
                type={f.key === "password" ? "password" : "text"}
                value={config[f.key] ?? ""}
                onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
              />
            </label>
          );
        })}
      </div>

      <div className="flex justify-end gap-2">
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
