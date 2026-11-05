import { useEffect, useState } from "react";
import NotificationForm from "./NotificationForm";
import { api, getRole } from "../lib/api";
import { t, useI18n } from "../lib/i18n";
import type { EscalationPolicy, Notification } from "../lib/types";

// NotificationsSection holds the full notification-channel + escalation UI.
// It lives inside the Settings page ("Notifikasi" tab).
export default function NotificationsSection() {
  const { lang } = useI18n();
  const [list, setList] = useState<Notification[]>([]);
  const [escalations, setEscalations] = useState<EscalationPolicy[]>([]);
  const [editing, setEditing] = useState<Partial<Notification> | null>(null);
  const [saving, setSaving] = useState(false);
  const [testMsg, setTestMsg] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const isAdmin = getRole() === "admin";

  async function load() {
    setList(await api.listNotifications().catch(() => []));
    setEscalations(await api.listEscalations().catch(() => []));
  }

  useEffect(() => {
    load();
  }, []);

  async function save(n: Partial<Notification>) {
    setSaving(true);
    setError("");
    try {
      if (n.id) await api.updateNotification(n.id, n);
      else await api.createNotification(n);
      setEditing(null);
      await load();
    } catch (e: any) {
      setError(e.message || t("notif.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function remove(n: Notification) {
    if (!confirm(t("notif.deleteConfirm", { name: n.name }))) return;
    await api.deleteNotification(n.id);
    load();
  }

  async function test(n: Notification) {
    setTestMsg((m) => ({ ...m, [n.id]: t("notif.sending") }));
    try {
      await api.testNotification(n.id);
      setTestMsg((m) => ({ ...m, [n.id]: t("notif.sent") }));
    } catch (e: any) {
      setTestMsg((m) => ({
        ...m,
        [n.id]: t("notif.sendFailed", { msg: e.message }),
      }));
    }
  }

  return (
    <div className="space-y-8">
      <div>
        <div className="flex items-center justify-between mb-4">
          <div>
            <h2 className="text-lg font-semibold">
              {t("notif.channelsTitle")}
            </h2>
            <p className="text-sm text-slate-400">{t("notif.channelsDesc")}</p>
          </div>
          {isAdmin && (
            <button className="btn-primary" onClick={() => setEditing({})}>
              {t("notif.addChannel")}
            </button>
          )}
        </div>

        {editing && isAdmin && (
          <div className="mb-4">
            <NotificationForm
              initial={editing}
              saving={saving}
              onSave={save}
              onCancel={() => setEditing(null)}
            />
          </div>
        )}
        {error && <div className="text-app-down text-sm mb-2">{error}</div>}

        <div className="grid grid-cols-1 gap-2">
          {list.map((n) => (
            <div
              key={n.id}
              className="bg-app-panel border border-slate-800 rounded-lg p-4 flex items-center gap-4"
            >
              <div className="flex-1">
                <div className="font-medium">{n.name}</div>
                <div className="text-xs text-slate-400 uppercase">{n.type}</div>
              </div>
              {testMsg[n.id] && (
                <span className="text-xs text-slate-400">{testMsg[n.id]}</span>
              )}
              {isAdmin && (
                <>
                  <button className="btn-secondary" onClick={() => test(n)}>
                    {t("common.test")}
                  </button>
                  <button
                    className="btn-secondary"
                    onClick={() => setEditing(n)}
                  >
                    {t("common.edit")}
                  </button>
                  <button className="btn-danger" onClick={() => remove(n)}>
                    {t("common.delete")}
                  </button>
                </>
              )}
            </div>
          ))}
          {list.length === 0 && (
            <div className="text-slate-500 text-sm">{t("notif.none")}</div>
          )}
        </div>
      </div>

      <EscalationSection
        key={lang}
        escalations={escalations}
        notifications={list}
        onChange={load}
        canEdit={isAdmin}
      />
    </div>
  );
}

function EscalationSection({
  escalations,
  notifications,
  onChange,
  canEdit,
}: {
  escalations: EscalationPolicy[];
  notifications: Notification[];
  onChange: () => void;
  canEdit: boolean;
}) {
  const [editing, setEditing] = useState<EscalationPolicy | null>(null);
  const [error, setError] = useState("");

  async function save(p: EscalationPolicy) {
    setError("");
    try {
      await api.upsertEscalation(p);
      setEditing(null);
      onChange();
    } catch (e: any) {
      setError(e.message || t("notif.escSaveFailed"));
    }
  }

  async function remove(id: string) {
    if (!confirm(t("notif.escDeleteConfirm"))) return;
    await api.deleteEscalation(id);
    onChange();
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold">{t("notif.escTitle")}</h2>
          <p className="text-sm text-slate-400">{t("notif.escDesc")}</p>
        </div>
        {canEdit && (
          <button
            className="btn-primary"
            onClick={() => setEditing({ name: "", steps: [] })}
          >
            {t("notif.addPolicy")}
          </button>
        )}
      </div>

      {error && <div className="text-app-down text-sm mb-2">{error}</div>}

      {editing && canEdit && (
        <EscalationForm
          initial={editing}
          notifications={notifications}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      <div className="grid grid-cols-1 gap-2 mt-3">
        {escalations.map((p) => (
          <div
            key={p.id}
            className="bg-app-panel border border-slate-800 rounded-lg p-4 flex items-center gap-4"
          >
            <div className="flex-1">
              <div className="font-medium">{p.name}</div>
              <div className="text-xs text-slate-400">
                {(p.steps ?? []).length === 0
                  ? t("notif.noSteps")
                  : (p.steps ?? [])
                      .map((s) => {
                        const n = notifications.find(
                          (x) => x.id === s.notifierId,
                        );
                        return t("notif.stepAfter", {
                          m: Math.round(s.afterDownForSec / 60),
                          name: n?.name ?? "?",
                        });
                      })
                      .join(" · ")}
              </div>
            </div>
            {canEdit && (
              <>
                <button className="btn-secondary" onClick={() => setEditing(p)}>
                  {t("common.edit")}
                </button>
                <button className="btn-danger" onClick={() => remove(p.id!)}>
                  {t("common.delete")}
                </button>
              </>
            )}
          </div>
        ))}
        {escalations.length === 0 && (
          <div className="text-slate-500 text-sm">{t("notif.noPolicies")}</div>
        )}
      </div>
    </div>
  );
}

function EscalationForm({
  initial,
  notifications,
  onSave,
  onCancel,
}: {
  initial: EscalationPolicy;
  notifications: Notification[];
  onSave: (p: EscalationPolicy) => void;
  onCancel: () => void;
}) {
  const [policy, setPolicy] = useState<EscalationPolicy>(initial);

  function updateStep(
    idx: number,
    patch: Partial<EscalationPolicy["steps"][number]>,
  ) {
    setPolicy((p) => {
      const steps = (p.steps ?? []).slice();
      steps[idx] = { ...steps[idx], ...patch };
      return { ...p, steps };
    });
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave(policy);
      }}
      className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
    >
      <label className="block text-sm">
        <span className="text-slate-300">{t("notif.policyName")}</span>
        <input
          className="input mt-1"
          value={policy.name}
          onChange={(e) => setPolicy((p) => ({ ...p, name: e.target.value }))}
          required
        />
      </label>

      <div className="space-y-2">
        {(policy.steps ?? []).map((s, i) => (
          <div key={i} className="flex items-center gap-2 text-sm">
            <span className="text-slate-400">{t("notif.afterDownFor")}</span>
            <input
              type="number"
              className="input w-24"
              value={s.afterDownForSec}
              onChange={(e) =>
                updateStep(i, { afterDownForSec: Number(e.target.value) })
              }
            />
            <span className="text-slate-400">{t("notif.secondsNotify")}</span>
            <select
              className="input w-48"
              value={s.notifierId}
              onChange={(e) => updateStep(i, { notifierId: e.target.value })}
            >
              <option value="">{t("notif.pickChannel")}</option>
              {notifications.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name}
                </option>
              ))}
            </select>
            <button
              type="button"
              className="btn-danger"
              onClick={() =>
                setPolicy((p) => ({
                  ...p,
                  steps: (p.steps ?? []).filter((_, idx) => idx !== i),
                }))
              }
            >
              {t("notif.remove")}
            </button>
          </div>
        ))}
      </div>

      <div className="flex justify-between">
        <button
          type="button"
          className="btn-secondary"
          onClick={() =>
            setPolicy((p) => ({
              ...p,
              steps: [
                ...(p.steps ?? []),
                { afterDownForSec: 300, notifierId: "" },
              ],
            }))
          }
        >
          {t("notif.addStep")}
        </button>
        <div className="space-x-2">
          <button type="button" className="btn-secondary" onClick={onCancel}>
            {t("common.cancel")}
          </button>
          <button type="submit" className="btn-primary">
            {t("notif.savePolicy")}
          </button>
        </div>
      </div>
    </form>
  );
}
