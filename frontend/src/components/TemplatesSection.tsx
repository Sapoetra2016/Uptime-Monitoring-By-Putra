import { useEffect, useMemo, useState } from "react";
import { api, getRole } from "../lib/api";
import { t, useI18n } from "../lib/i18n";
import type { AlertTemplate } from "../lib/types";

// Placeholders the backend dispatcher substitutes at send time.
const PLACEHOLDERS = [
  "{monitor}",
  "{serverName}",
  "{status}",
  "{message}",
  "{reason}",
  "{target}",
  "{url}",
  "{ipPublic}",
  "{ipLocal}",
  "{time}",
  "{duration}",
  "{step}",
];

// Built-in default text, mirrored from the backend so "reset" can restore it
// without an extra round-trip.
const DEFAULTS: Record<"chat" | "email", AlertTemplate> = {
  chat: {
    id: "",
    name: "Default (Chat)",
    channel: "chat",
    subject: "",
    bodyDown:
      "🔴 {monitor} DOWN\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
    bodyUp:
      "✅ {monitor} UP again\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nDown for     : {duration}",
    bodyDegraded:
      "⚠️ {monitor} DEGRADED\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
    bodyEscalation:
      "🔴 [ESCALATION step {step} - down {duration}] {monitor} masih DOWN\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
    isDefault: true,
  },
  email: {
    id: "",
    name: "Default (Email)",
    channel: "email",
    subject: "[{status}] {monitor}",
    bodyDown:
      "{monitor} is DOWN at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}\nTarget       : {url}",
    bodyUp:
      "{monitor} is UP again at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nDown for     : {duration}",
    bodyDegraded:
      "{monitor} is DEGRADED at {time}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nPesan        : {message}",
    bodyEscalation:
      "[ESCALATION step {step}] {monitor} has been DOWN for {duration}.\n\nNama Server : {serverName}\nIP Public    : {ipPublic}\nIP Lokal     : {ipLocal}\nWaktu        : {time}\nPesan        : {message}",
    isDefault: true,
  },
};

function blankTemplate(channel: "chat" | "email"): AlertTemplate {
  return {
    id: "new-" + Date.now() + "-" + Math.floor(Math.random() * 1000),
    name: "",
    channel,
    subject: channel === "email" ? "[{status}] {monitor}" : "",
    bodyDown: "{monitor} is DOWN: {message}",
    bodyUp: "{monitor} is UP again",
    bodyDegraded: "{monitor} is DEGRADED: {message}",
    bodyEscalation: "[ESCALATION step {step}] {monitor} down {duration}",
    isDefault: false,
  };
}

export default function TemplatesSection() {
  const { lang } = useI18n();
  const isAdmin = getRole() === "admin";
  const [list, setList] = useState<AlertTemplate[]>([]);
  const [selId, setSelId] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [savedMsg, setSavedMsg] = useState("");

  async function load() {
    setLoading(true);
    setError("");
    try {
      const res = await api.listTemplates();
      setList(res);
      setSelId(res[0]?.id ?? "");
    } catch (e: any) {
      setError(e?.message || t("tpl.loadFailed"));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  const selected = useMemo(
    () => list.find((x) => x.id === selId) ?? null,
    [list, selId],
  );

  function patch(p: Partial<AlertTemplate>) {
    if (!selected) return;
    setList((l) => l.map((x) => (x.id === selected.id ? { ...x, ...p } : x)));
    setSavedMsg("");
  }

  function add(channel: "chat" | "email") {
    const draft = blankTemplate(channel);
    setList((l) => [...l, draft]);
    setSelId(draft.id);
    setSavedMsg("");
  }

  function remove(id: string) {
    if (!confirm(t("tpl.deleteConfirm"))) return;
    setList((l) => {
      const next = l.filter((x) => x.id !== id);
      if (selId === id) setSelId(next[0]?.id ?? "");
      return next;
    });
    setSavedMsg("");
  }

  // Mark the selected template as the default for its channel (unsetting the
  // others in the same channel).
  function makeDefault() {
    if (!selected) return;
    setList((l) =>
      l.map((x) =>
        x.channel === selected.channel
          ? { ...x, isDefault: x.id === selected.id }
          : x,
      ),
    );
    setSavedMsg("");
  }

  function resetText() {
    if (!selected) return;
    const d = DEFAULTS[selected.channel];
    patch({
      subject: d.subject,
      bodyDown: d.bodyDown,
      bodyUp: d.bodyUp,
      bodyDegraded: d.bodyDegraded,
      bodyEscalation: d.bodyEscalation,
    });
  }

  async function save() {
    setSaving(true);
    setError("");
    setSavedMsg("");
    try {
      // New templates carry a temporary "new-" id; blank it so the backend
      // assigns a real one.
      const payload = list.map((x) => ({
        ...x,
        id: x.id.startsWith("new-") ? "" : x.id,
      }));
      const saved = await api.saveTemplates(payload);
      setList(saved);
      // Keep the just-saved selection when it already existed; otherwise the
      // backend assigned a fresh id, so select the first template.
      const stillThere =
        selected &&
        saved.find(
          (s) => s.id === selected.id && !selected.id.startsWith("new-"),
        );
      setSelId(stillThere ? stillThere.id : (saved[0]?.id ?? ""));
      setSavedMsg(t("tpl.saved"));
    } catch (e: any) {
      setError(e?.message || t("tpl.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <div className="text-sm text-slate-500">{t("tpl.loading")}</div>;
  }

  const chatList = list.filter((x) => x.channel === "chat");
  const emailList = list.filter((x) => x.channel === "email");

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">{t("tpl.title")}</h2>
        <p className="text-sm text-slate-400">{t("tpl.desc")}</p>
      </div>

      {error && <div className="text-app-down text-sm">{error}</div>}

      <div className="flex flex-col lg:flex-row gap-5">
        {/* Library rail */}
        <div className="lg:w-64 shrink-0 space-y-4">
          <ChannelGroup
            label={t("tpl.channelChat")}
            items={chatList}
            selId={selId}
            onSelect={setSelId}
            onAdd={() => isAdmin && add("chat")}
            canEdit={isAdmin}
          />
          <ChannelGroup
            label={t("tpl.channelEmail")}
            items={emailList}
            selId={selId}
            onSelect={setSelId}
            onAdd={() => isAdmin && add("email")}
            canEdit={isAdmin}
          />
        </div>

        {/* Editor */}
        <div className="flex-1 min-w-0">
          {selected ? (
            <TemplateEditor
              key={selected.id + lang}
              tpl={selected}
              canEdit={isAdmin}
              onChange={patch}
              onMakeDefault={makeDefault}
              onReset={resetText}
              onDelete={() => remove(selected.id)}
            />
          ) : (
            <div className="text-sm text-slate-500">{t("tpl.none")}</div>
          )}
        </div>
      </div>

      {isAdmin && (
        <div className="flex items-center gap-3 pt-2 border-t border-slate-800">
          <button className="btn-primary" onClick={save} disabled={saving}>
            {saving ? t("common.saving") : t("tpl.saveAll")}
          </button>
          {savedMsg && <span className="text-sm text-app-up">{savedMsg}</span>}
        </div>
      )}

      <PlaceholderHelp />
    </div>
  );
}

function ChannelGroup({
  label,
  items,
  selId,
  onSelect,
  onAdd,
  canEdit,
}: {
  label: string;
  items: AlertTemplate[];
  selId: string;
  onSelect: (id: string) => void;
  onAdd: () => void;
  canEdit: boolean;
}) {
  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs uppercase tracking-wide text-slate-500">
          {label}
        </span>
        {canEdit && (
          <button
            className="text-xs text-sky-400 hover:underline"
            onClick={onAdd}
          >
            + {t("tpl.new")}
          </button>
        )}
      </div>
      <div className="space-y-1">
        {items.map((x) => (
          <button
            key={x.id}
            onClick={() => onSelect(x.id)}
            className={`w-full text-left rounded-lg px-3 py-2 text-sm flex items-center justify-between gap-2 ${
              selId === x.id
                ? "bg-slate-800 text-white ring-1 ring-sky-500/40"
                : "text-slate-300 hover:bg-slate-800/50"
            }`}
          >
            <span className="truncate">{x.name || t("tpl.untitled")}</span>
            {x.isDefault && (
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/15 text-emerald-400 shrink-0">
                {t("tpl.defaultBadge")}
              </span>
            )}
          </button>
        ))}
        {items.length === 0 && (
          <div className="text-xs text-slate-600">{t("tpl.emptyChannel")}</div>
        )}
      </div>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block text-sm">
      <span className="text-slate-300">{label}</span>
      <div className="mt-1">{children}</div>
    </label>
  );
}

function TemplateEditor({
  tpl,
  canEdit,
  onChange,
  onMakeDefault,
  onReset,
  onDelete,
}: {
  tpl: AlertTemplate;
  canEdit: boolean;
  onChange: (p: Partial<AlertTemplate>) => void;
  onMakeDefault: () => void;
  onReset: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <Field label={t("tpl.name")}>
          <input
            className="input"
            value={tpl.name}
            disabled={!canEdit}
            onChange={(e) => onChange({ name: e.target.value })}
          />
        </Field>
        <Field label={t("tpl.channel")}>
          <div className="input bg-slate-800/40 text-slate-400">
            {tpl.channel === "email"
              ? t("tpl.channelEmail")
              : t("tpl.channelChat")}
          </div>
        </Field>
      </div>

      {tpl.channel === "email" && (
        <Field label={t("tpl.subject")}>
          <input
            className="input font-mono text-xs"
            value={tpl.subject ?? ""}
            disabled={!canEdit}
            onChange={(e) => onChange({ subject: e.target.value })}
          />
        </Field>
      )}

      <Field label={t("tpl.bodyDown")}>
        <textarea
          className="input font-mono text-xs min-h-[64px]"
          value={tpl.bodyDown}
          disabled={!canEdit}
          onChange={(e) => onChange({ bodyDown: e.target.value })}
        />
      </Field>
      <Field label={t("tpl.bodyUp")}>
        <textarea
          className="input font-mono text-xs min-h-[64px]"
          value={tpl.bodyUp}
          disabled={!canEdit}
          onChange={(e) => onChange({ bodyUp: e.target.value })}
        />
      </Field>
      <Field label={t("tpl.bodyDegraded")}>
        <textarea
          className="input font-mono text-xs min-h-[64px]"
          value={tpl.bodyDegraded}
          disabled={!canEdit}
          onChange={(e) => onChange({ bodyDegraded: e.target.value })}
        />
      </Field>
      <Field label={t("tpl.bodyEscalation")}>
        <textarea
          className="input font-mono text-xs min-h-[64px]"
          value={tpl.bodyEscalation}
          disabled={!canEdit}
          onChange={(e) => onChange({ bodyEscalation: e.target.value })}
        />
      </Field>

      {canEdit && (
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <button
            className="btn-secondary"
            onClick={onMakeDefault}
            disabled={tpl.isDefault}
          >
            {tpl.isDefault ? t("tpl.isDefault") : t("tpl.makeDefault")}
          </button>
          <button className="btn-secondary" onClick={onReset}>
            {t("tpl.resetText")}
          </button>
          <button className="btn-danger ml-auto" onClick={onDelete}>
            {t("common.delete")}
          </button>
        </div>
      )}
    </div>
  );
}

function PlaceholderHelp() {
  return (
    <details className="text-sm bg-app-panel border border-slate-800 rounded-lg p-4">
      <summary className="cursor-pointer text-slate-300">
        {t("tpl.placeholdersTitle")}
      </summary>
      <div className="mt-3 flex flex-wrap gap-2">
        {PLACEHOLDERS.map((p) => (
          <code
            key={p}
            className="text-xs font-mono px-2 py-1 rounded bg-slate-800 text-sky-300"
          >
            {p}
          </code>
        ))}
      </div>
      <p className="text-xs text-slate-500 mt-3">{t("tpl.placeholdersDesc")}</p>
    </details>
  );
}
