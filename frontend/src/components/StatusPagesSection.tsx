import { useEffect, useState } from "react";
import { api, getRole } from "../lib/api";
import { useMonitorIds } from "../hooks/useMonitor";
import { monitorStore } from "../lib/monitorStore";
import { t, useI18n } from "../lib/i18n";
import type { StatusPage } from "../lib/types";

// StatusPagesSection holds the public status-page manager UI.
// It lives inside the Settings page ("Status Pages" tab).
export default function StatusPagesSection() {
  const { lang } = useI18n();
  const ids = useMonitorIds();
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [editing, setEditing] = useState<StatusPage | null>(null);
  const [error, setError] = useState("");
  const isAdmin = getRole() === "admin";

  async function load() {
    setPages(await api.listStatusPages().catch(() => []));
  }
  useEffect(() => {
    load();
  }, []);

  async function save(p: StatusPage) {
    setError("");
    if (!/^[a-z0-9-]+$/i.test(p.slug)) {
      setError(t("sp.slugError"));
      return;
    }
    try {
      await api.upsertStatusPage(p);
      setEditing(null);
      await load();
    } catch (e: any) {
      setError(e.message || t("sp.saveFailed"));
    }
  }

  async function remove(id: string) {
    if (!confirm(t("sp.deleteConfirm"))) return;
    await api.deleteStatusPage(id);
    load();
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">{t("sp.title")}</h2>
          <p className="text-sm text-slate-400">
            {t("sp.desc")}
          </p>
        </div>
        {isAdmin && (
          <button
            className="btn-primary"
            onClick={() => setEditing({ slug: "", title: "", description: "", monitors: [] })}
          >
            {t("sp.create")}
          </button>
        )}
      </div>

      {error && <div className="text-app-down text-sm">{error}</div>}

      {editing && isAdmin && (
        <StatusPageForm
          key={lang}
          initial={editing}
          allMonitors={ids.map((id) => monitorStore.getById(id)).filter(Boolean).map((m) => ({ id: m!.id, name: m!.name }))}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      <div className="grid grid-cols-1 gap-2">
        {pages.map((p) => (
          <div
            key={p.id}
            className="bg-app-panel border border-slate-800 rounded-lg p-4 flex items-center gap-4"
          >
            <div className="flex-1">
              <div className="font-medium">{p.title}</div>
              <div className="text-xs text-slate-400">
                /status/{p.slug} · {t("sp.monitorCount", { n: (p.monitors ?? []).length })}
              </div>
            </div>
            <a href={`/status/${p.slug}`} target="_blank" rel="noreferrer" className="btn-secondary">
              {t("sp.view")}
            </a>
            {isAdmin && (
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
        {pages.length === 0 && (
          <div className="text-slate-500 text-sm">{t("sp.none")}</div>
        )}
      </div>
    </div>
  );
}

function StatusPageForm({
  initial,
  allMonitors,
  onSave,
  onCancel,
}: {
  initial: StatusPage;
  allMonitors: { id: string; name: string }[];
  onSave: (p: StatusPage) => void;
  onCancel: () => void;
}) {
  const [page, setPage] = useState<StatusPage>(initial);
  const { t } = useI18n();

  function toggleMonitor(id: string) {
    setPage((p) => {
      const current = p.monitors ?? [];
      const exists = current.some((m) => m.monitorId === id);
      const monitors = exists
        ? current.filter((m) => m.monitorId !== id)
        : [...current, { monitorId: id, position: current.length }];
      return { ...p, monitors };
    });
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave(page);
      }}
      className="bg-app-panel border border-slate-800 rounded-lg p-5 space-y-4"
    >
      <div className="grid grid-cols-2 gap-4">
        <label className="block text-sm">
          <span className="text-slate-300">{t("sp.fieldTitle")}</span>
          <input
            className="input mt-1"
            value={page.title}
            onChange={(e) => setPage((p) => ({ ...p, title: e.target.value }))}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("sp.fieldSlug")}</span>
          <input
            className="input mt-1"
            value={page.slug}
            onChange={(e) => setPage((p) => ({ ...p, slug: e.target.value }))}
            required
          />
        </label>
      </div>
      <label className="block text-sm">
        <span className="text-slate-300">{t("sp.fieldDesc")}</span>
        <input
          className="input mt-1"
          value={page.description ?? ""}
          onChange={(e) => setPage((p) => ({ ...p, description: e.target.value }))}
        />
      </label>

      <div className="text-sm">
        <span className="text-slate-300">{t("sp.monitorsLabel")}</span>
        <div className="mt-2 grid grid-cols-1 md:grid-cols-2 gap-2">
          {allMonitors.map((m) => {
            const checked = (page.monitors ?? []).some((x) => x.monitorId === m.id);
            return (
              <label key={m.id} className="flex items-center gap-2 text-slate-300">
                <input type="checkbox" checked={checked} onChange={() => toggleMonitor(m.id)} />
                {m.name}
              </label>
            );
          })}
          {allMonitors.length === 0 && (
            <div className="text-slate-500 text-xs">{t("sp.noMonitors")}</div>
          )}
        </div>
      </div>

      <div className="flex justify-end gap-2">
        <button type="button" className="btn-secondary" onClick={onCancel}>
          {t("common.cancel")}
        </button>
        <button type="submit" className="btn-primary">
          {t("sp.save")}
        </button>
      </div>
    </form>
  );
}
