import { useState } from "react";
import { useI18n } from "../lib/i18n";
import { api } from "../lib/api";
import type { Notification, ReportChannel } from "../lib/types";

// Config fields offered per channel type, mirroring the backend notifier
// config shapes so a report channel behaves exactly like an alert channel.
// Slack needs botToken + channel to attach the report file; webhookUrl alone
// only delivers the text summary.
type Field = {
  key: string;
  labelKey: string;
  hintKey?: string;
  type?: "text" | "password" | "number";
  options?: { value: string; labelKey: string }[];
};

const typeFields: Record<Notification["type"], Field[]> = {
  telegram: [
    { key: "botToken", labelKey: "nf.botToken", type: "password" },
    { key: "chatId", labelKey: "nf.chatId" },
  ],
  discord: [{ key: "webhookUrl", labelKey: "nf.webhookUrl" }],
  slack: [
    {
      key: "botToken",
      labelKey: "set.reportChSlackToken",
      hintKey: "set.reportChSlackTokenHint",
      type: "password",
    },
    {
      key: "channel",
      labelKey: "set.reportChSlackChannel",
      hintKey: "set.reportChSlackChannelHint",
    },
    {
      key: "webhookUrl",
      labelKey: "nf.slackUrl",
      hintKey: "set.reportChSlackWebhookHint",
    },
  ],
  webhook: [
    { key: "url", labelKey: "form.url" },
    { key: "method", labelKey: "nf.method", hintKey: "nf.methodHint" },
  ],
  email: [
    { key: "smtpHost", labelKey: "nf.smtpHost", hintKey: "nf.smtpHostHint" },
    {
      key: "smtpPort",
      labelKey: "nf.smtpPort",
      options: [
        { value: "587", labelKey: "nf.smtpPortStartTls" },
        { value: "465", labelKey: "nf.smtpPortImplicitTls" },
        { value: "25", labelKey: "nf.smtpPortPlain" },
      ],
    },
    { key: "username", labelKey: "login.username" },
    { key: "password", labelKey: "login.password", type: "password" },
    { key: "from", labelKey: "nf.from" },
    { key: "to", labelKey: "nf.to" },
  ],
};

const TYPE_OPTIONS: { value: Notification["type"]; labelKey: string }[] = [
  { value: "email", labelKey: "nf.typeEmail" },
  { value: "telegram", labelKey: "nf.typeTelegram" },
  { value: "slack", labelKey: "nf.typeSlack" },
  { value: "discord", labelKey: "nf.typeDiscord" },
  { value: "webhook", labelKey: "nf.typeWebhook" },
];

function newId(): string {
  return (
    "rc-" +
    Date.now().toString(36) +
    Math.floor(Math.random() * 1000).toString(36)
  );
}

// ReportChannelsEditor manages the report's own delivery destinations, kept
// separate from the alert notification channels. It is fully controlled: the
// parent owns the array and persists it inside the report settings blob.
export default function ReportChannelsEditor({
  channels,
  onChange,
}: {
  channels: ReportChannel[];
  onChange: (next: ReportChannel[]) => void;
}) {
  const { t } = useI18n();
  const [draftType, setDraftType] = useState<Notification["type"]>("email");
  const [draftName, setDraftName] = useState("");
  const [draftConfig, setDraftConfig] = useState<Record<string, any>>({});
  // Per-channel test feedback, keyed by channel id ("draft" for the add form).
  const [testMsg, setTestMsg] = useState<Record<string, string>>({});
  // Which saved channel cards are expanded for editing. Collapsed by default
  // so the list stays compact; Edit reveals the full config form.
  const [editing, setEditing] = useState<Record<string, boolean>>({});

  // Send a test message through an (unsaved) channel config so the user can
  // verify credentials before adding or saving the report delivery method.
  async function runTest(
    key: string,
    name: string,
    type: Notification["type"],
    config: Record<string, unknown>,
  ) {
    setTestMsg((m) => ({ ...m, [key]: t("notif.sending") }));
    try {
      await api.testNotificationPreview({ name, type, config });
      setTestMsg((m) => ({ ...m, [key]: t("notif.sent") }));
    } catch (e: any) {
      setTestMsg((m) => ({
        ...m,
        [key]: t("notif.sendFailed", { msg: e?.message ?? "" }),
      }));
    }
  }

  function addChannel() {
    if (!draftName.trim()) return;
    const ch: ReportChannel = {
      id: newId(),
      name: draftName.trim(),
      type: draftType,
      config: draftConfig,
    };
    onChange([...channels, ch]);
    // Reset the add form so the next method starts blank.
    setDraftName("");
    setDraftConfig({});
    setDraftType("email");
    setTestMsg((m) => ({ ...m, draft: "" }));
  }

  function removeChannel(id: string) {
    onChange(channels.filter((c) => c.id !== id));
  }

  function updateConfig(id: string, key: string, value: unknown) {
    onChange(
      channels.map((c) =>
        c.id === id ? { ...c, config: { ...c.config, [key]: value } } : c,
      ),
    );
  }

  function changeDraftType(next: Notification["type"]) {
    setDraftType(next);
    setDraftConfig({});
  }

  // Human-readable label for a channel type, used in the summary line.
  const typeLabel = (v: Notification["type"]) => {
    const o = TYPE_OPTIONS.find((x) => x.value === v);
    return o ? t(o.labelKey) : v;
  };

  // A short, single-line description of a saved channel's key target so the
  // collapsed card is meaningful without opening the full form.
  const channelSummary = (c: ReportChannel): string => {
    const cfg = c.config ?? {};
    switch (c.type) {
      case "email": {
        const host = String(cfg.smtpHost ?? "");
        const port = String(cfg.smtpPort ?? "");
        const to = String(cfg.to ?? "");
        return [host ? `${host}:${port}` : "", to].filter(Boolean).join(" · ");
      }
      case "telegram":
        return String(cfg.chatId ?? "");
      case "slack":
        return String(cfg.channel ?? cfg.webhookUrl ?? "");
      case "discord":
        return String(cfg.webhookUrl ?? "");
      case "webhook":
        return String(cfg.url ?? "");
      default:
        return "";
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-xs text-slate-500">{t("set.reportChDesc")}</p>

      {channels.length > 0 && (
        <p className="text-sm text-slate-300">
          {t("set.reportChSummary", {
            methods: channels.map((c) => typeLabel(c.type)).join(", "),
          })}
        </p>
      )}

      {channels.length === 0 ? (
        <p className="text-xs text-slate-500 italic">{t("set.reportChNone")}</p>
      ) : (
        <div className="space-y-3">
          {channels.map((c) => (
            <div
              key={c.id}
              className="border border-slate-800 rounded-lg p-4 bg-slate-900/40 space-y-3"
            >
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-slate-200 truncate">
                    {c.name}{" "}
                    <span className="text-slate-500">
                      ({typeLabel(c.type)})
                    </span>
                  </div>
                  {channelSummary(c) && (
                    <div className="text-xs text-slate-500 truncate">
                      {channelSummary(c)}
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-xs text-slate-400">
                    {testMsg[c.id]}
                  </span>
                  <button
                    type="button"
                    className="btn-secondary text-xs px-2 py-1"
                    onClick={() =>
                      setEditing((e) => ({ ...e, [c.id]: !e[c.id] }))
                    }
                  >
                    {editing[c.id] ? t("common.done") : t("common.edit")}
                  </button>
                  <button
                    type="button"
                    className="btn-secondary text-xs px-2 py-1"
                    onClick={() => runTest(c.id, c.name, c.type, c.config)}
                  >
                    {t("nf.test")}
                  </button>
                  <button
                    type="button"
                    className="btn-danger text-xs px-2 py-1"
                    onClick={() => removeChannel(c.id)}
                  >
                    {t("common.delete")}
                  </button>
                </div>
              </div>
              {editing[c.id] && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  {typeFields[c.type].map((f) => {
                    if (f.key === "useTls") {
                      return (
                        <label
                          key={f.key}
                          className="flex items-center gap-2 text-sm col-span-2"
                        >
                          <input
                            type="checkbox"
                            checked={!!c.config[f.key]}
                            onChange={(e) =>
                              updateConfig(c.id, f.key, e.target.checked)
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
                        {f.options ? (
                          <select
                            className="input mt-1"
                            value={String(
                              c.config[f.key] ?? f.options[0].value,
                            )}
                            onChange={(e) =>
                              updateConfig(c.id, f.key, e.target.value)
                            }
                          >
                            {f.options.map((o) => (
                              <option key={o.value} value={o.value}>
                                {t(o.labelKey)}
                              </option>
                            ))}
                          </select>
                        ) : (
                          <input
                            className="input mt-1"
                            type={
                              f.type === "password"
                                ? "password"
                                : f.type === "number"
                                  ? "number"
                                  : "text"
                            }
                            value={String(c.config[f.key] ?? "")}
                            onChange={(e) =>
                              updateConfig(c.id, f.key, e.target.value)
                            }
                          />
                        )}
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Add-new inline form */}
      <div className="border border-dashed border-slate-700 rounded-lg p-4 space-y-3">
        <div className="grid grid-cols-2 gap-4">
          <label className="block text-sm">
            <span className="text-slate-300">{t("form.name")}</span>
            <input
              className="input mt-1"
              value={draftName}
              onChange={(e) => setDraftName(e.target.value)}
              placeholder={t("set.reportChNamePlaceholder")}
            />
          </label>
          <label className="block text-sm">
            <span className="text-slate-300">{t("form.type")}</span>
            <select
              className="input mt-1"
              value={draftType}
              onChange={(e) =>
                changeDraftType(e.target.value as Notification["type"])
              }
            >
              {TYPE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {t(o.labelKey)}
                </option>
              ))}
            </select>
          </label>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {typeFields[draftType].map((f) => {
            if (f.key === "useTls") {
              return (
                <label
                  key={f.key}
                  className="flex items-center gap-2 text-sm col-span-2"
                >
                  <input
                    type="checkbox"
                    checked={!!draftConfig[f.key]}
                    onChange={(e) =>
                      setDraftConfig((c) => ({
                        ...c,
                        [f.key]: e.target.checked,
                      }))
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
                {f.options ? (
                  <select
                    className="input mt-1"
                    value={String(draftConfig[f.key] ?? f.options[0].value)}
                    onChange={(e) =>
                      setDraftConfig((c) => ({ ...c, [f.key]: e.target.value }))
                    }
                  >
                    {f.options.map((o) => (
                      <option key={o.value} value={o.value}>
                        {t(o.labelKey)}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    className="input mt-1"
                    type={
                      f.type === "password"
                        ? "password"
                        : f.type === "number"
                          ? "number"
                          : "text"
                    }
                    value={draftConfig[f.key] ?? ""}
                    onChange={(e) =>
                      setDraftConfig((c) => ({ ...c, [f.key]: e.target.value }))
                    }
                  />
                )}
              </label>
            );
          })}
        </div>

        <div className="flex items-center justify-end gap-2">
          <span className="mr-auto text-xs text-slate-400">
            {testMsg.draft}
          </span>
          <button
            type="button"
            className="btn-secondary"
            onClick={() => runTest("draft", draftName, draftType, draftConfig)}
            disabled={!draftName.trim()}
          >
            {t("nf.test")}
          </button>
          <button
            type="button"
            className="btn-secondary"
            onClick={addChannel}
            disabled={!draftName.trim()}
          >
            {t("set.reportChAdd")}
          </button>
        </div>
      </div>
    </div>
  );
}
