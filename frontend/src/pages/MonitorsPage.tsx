import { useEffect, useMemo, useState } from "react";
import MonitorForm from "../components/MonitorForm";
import Modal from "../components/Modal";
import MonitorRow from "../components/MonitorRow";
import { useMonitorIds } from "../hooks/useMonitor";
import { useLive } from "../hooks/useLive";
import { api, getRole } from "../lib/api";
import { getPrefs } from "../lib/prefs";
import { monitorStore } from "../lib/monitorStore";
import { useI18n } from "../lib/i18n";
import type { EscalationPolicy, Monitor, Notification } from "../lib/types";

export default function MonitorsPage() {
  const { refresh } = useLive();
  const { t } = useI18n();
  const ids = useMonitorIds();
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [escalations, setEscalations] = useState<EscalationPolicy[]>([]);
  const [editing, setEditing] = useState<Partial<Monitor> | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [tagFilter, setTagFilter] = useState("");
  const [enabledFilter, setEnabledFilter] = useState<"all" | "active" | "paused">(() =>
    getPrefs().hidePaused ? "active" : "all",
  );
  const isAdmin = getRole() === "admin";

  useEffect(() => {
    api.listNotifications().then(setNotifications).catch(() => {});
    api.listEscalations().then(setEscalations).catch(() => {});
  }, []);

  // Collect all tags from the current monitors — structural changes (add/remove/CRUD)
  // trigger this via `ids` dep. Tags don't change on per-node status pushes.
  const allTags = useMemo(() => {
    const set = new Set<string>();
    for (const id of ids) {
      const m = monitorStore.getById(id);
      if (m) for (const t of m.tags ?? []) set.add(t);
    }
    return Array.from(set).sort();
  }, [ids]);

  const visibleIds = useMemo(() => {
    let out = ids;
    if (enabledFilter === "active") {
      out = out.filter((id) => monitorStore.getById(id)?.enabled !== false);
    } else if (enabledFilter === "paused") {
      out = out.filter((id) => monitorStore.getById(id)?.enabled === false);
    }
    if (tagFilter) {
      out = out.filter((id) => {
        const m = monitorStore.getById(id);
        return m?.tags?.includes(tagFilter);
      });
    }
    return out;
  }, [ids, tagFilter, enabledFilter]);

  async function save(m: Partial<Monitor>) {
    setSaving(true);
    setError("");
    try {
      if (m.id) {
        await api.updateMonitor(m.id, m);
      } else {
        await api.createMonitor(m);
      }
      setEditing(null);
      refresh();
    } catch (e: any) {
      setError(e.message || t("mon.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  function handleEdit(m: Monitor) {
    setEditing(m);
  }

  async function handleDelete(m: Monitor) {
    if (!confirm(t("mon.deleteConfirm", { name: m.name }))) return;
    await api.deleteMonitor(m.id);
    monitorStore.remove(m.id);
    refresh();
  }

  // Pause / resume without opening the edit form. Optimistically patch the
  // store for instant feedback, then reconcile with the server.
  const [togglingId, setTogglingId] = useState<string | null>(null);
  async function handleToggle(m: Monitor) {
    const next = !m.enabled;
    setTogglingId(m.id);
    monitorStore.updateOne(m.id, { enabled: next });
    try {
      await api.setMonitorEnabled(m.id, next);
      refresh();
    } catch {
      monitorStore.updateOne(m.id, { enabled: m.enabled }); // revert
    } finally {
      setTogglingId(null);
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-2xl font-bold">{t("mon.title")}</h1>
        <div className="flex items-center gap-3">
          <div className="flex rounded-md border border-slate-700 overflow-hidden text-sm">
            {(["all", "active", "paused"] as const).map((opt) => (
              <button
                key={opt}
                onClick={() => setEnabledFilter(opt)}
                className={`px-3 py-1.5 capitalize transition-colors ${
                  enabledFilter === opt
                    ? "bg-sky-500/20 text-sky-300"
                    : "bg-slate-800 text-slate-400 hover:text-white"
                }`}
              >
                {t(`mon.filter${opt.charAt(0).toUpperCase() + opt.slice(1)}`)}
              </button>
            ))}
          </div>
          {allTags.length > 0 && (
            <select className="input w-auto text-sm" value={tagFilter} onChange={(e) => setTagFilter(e.target.value)}>
              <option value="">{t("mon.allTags")}</option>
              {allTags.map((tag) => (
                <option key={tag} value={tag}>
                  {tag}
                </option>
              ))}
            </select>
          )}
          {isAdmin && (
            <button className="btn-primary" onClick={() => setEditing({})}>
              {t("mon.add")}
            </button>
          )}
        </div>
      </div>

      {!isAdmin && (
        <div className="text-sm text-slate-400">
          {t("mon.viewerNote")}
        </div>
      )}

      {editing && isAdmin && (
        <Modal
          title={editing.id ? t("mon.editTitle") : t("mon.addTitle")}
          onClose={() => setEditing(null)}
          wide
        >
          <MonitorForm
            initial={editing}
            notifications={notifications}
            escalations={escalations}
            saving={saving}
            onSave={save}
            onCancel={() => setEditing(null)}
          />
          {error && <div className="text-app-down text-sm mt-3">{error}</div>}
        </Modal>
      )}

      <div className="overflow-x-auto bg-app-panel border border-slate-800 rounded-lg">
        <table className="w-full text-sm">
          <thead className="text-left text-slate-400">
            <tr>
              <th className="p-3">{t("mon.thStatus")}</th>
              <th className="p-3">{t("mon.thName")}</th>
              <th className="p-3">{t("mon.thType")}</th>
              <th className="p-3">{t("mon.thTarget")}</th>
              <th className="p-3">{t("mon.thTags")}</th>
              <th className="p-3">{t("mon.thInterval")}</th>
              <th className="p-3">{t("mon.thEnabled")}</th>
              <th className="p-3">{t("mon.thAddedBy")}</th>
              {isAdmin && <th className="p-3"></th>}
            </tr>
          </thead>
          <tbody>
            {visibleIds.map((id) => (
              <MonitorRow
                key={id}
                id={id}
                isAdmin={isAdmin}
                compact={getPrefs().compact}
                onEdit={handleEdit}
                onDelete={handleDelete}
                onToggle={handleToggle}
                toggling={togglingId === id}
              />
            ))}
            {visibleIds.length === 0 && (
              <tr>
                <td colSpan={isAdmin ? 9 : 8} className="p-6 text-center text-slate-500">
                  {ids.length === 0 ? t("mon.noneConfigured") : t("mon.noneMatch")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
