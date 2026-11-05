import { useEffect, useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { t, useI18n } from "../lib/i18n";
import type { User, UserRole } from "../lib/types";

// UsersSection holds the user-management UI. It lives inside the Settings page
// ("Users" tab, admin only). The backend enforces admin on all /api/users
// routes, so this component is purely presentation + client-side hiding.
export default function UsersSection() {
  const { lang } = useI18n();
  const [users, setUsers] = useState<User[]>([]);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<UserRole>("viewer");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  async function load() {
    try {
      setUsers(await api.listUsers());
    } catch (e: any) {
      setError(e.message || t("usr.loadFailed"));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function add(e: FormEvent) {
    e.preventDefault();
    setError("");
    setSaving(true);
    try {
      await api.createUser({ username, password, role });
      setUsername("");
      setPassword("");
      setRole("viewer");
      await load();
    } catch (err: any) {
      setError(err.message || t("usr.createFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function changeRole(u: User, r: UserRole) {
    await api.updateUserRole(u.id, r);
    load();
  }

  async function resetPassword(u: User) {
    const pw = prompt(t("usr.resetPrompt", { name: u.username }));
    if (!pw) return;
    await api.updateUserPassword(u.id, pw);
    alert(t("usr.resetDone"));
  }

  async function remove(u: User) {
    if (!confirm(t("usr.deleteConfirm", { name: u.username }))) return;
    try {
      await api.deleteUser(u.id);
      load();
    } catch (e: any) {
      setError(e.message || t("usr.deleteFailed"));
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">{t("usr.title")}</h2>
        <p className="text-sm text-slate-400">{t("usr.desc")}</p>
      </div>

      <form
        onSubmit={add}
        className="bg-app-panel border border-slate-800 rounded-lg p-5 grid grid-cols-1 md:grid-cols-4 gap-4 items-end"
      >
        <label className="block text-sm">
          <span className="text-slate-300">{t("usr.thUsername")}</span>
          <input
            className="input mt-1"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("login.password")}</span>
          <input
            type="password"
            className="input mt-1"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="text-slate-300">{t("usr.thRole")}</span>
          <select
            className="input mt-1"
            value={role}
            onChange={(e) => setRole(e.target.value as UserRole)}
          >
            <option value="viewer">{t("usr.roleViewer")}</option>
            <option value="admin">{t("usr.roleAdmin")}</option>
          </select>
        </label>
        <button disabled={saving} className="btn-primary">
          {saving ? t("usr.adding") : t("usr.add")}
        </button>
      </form>
      {error && <div className="text-app-down text-sm">{error}</div>}

      <div className="overflow-x-auto bg-app-panel border border-slate-800 rounded-lg">
        <table key={lang} className="w-full text-sm">
          <thead className="text-left text-slate-400">
            <tr>
              <th className="p-3">{t("usr.thUsername")}</th>
              <th className="p-3">{t("usr.thRole")}</th>
              <th className="p-3">{t("usr.thCreated")}</th>
              <th className="p-3 text-right">{t("usr.thActions")}</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id} className="border-t border-slate-800">
                <td className="p-3 font-medium">{u.username}</td>
                <td className="p-3">
                  <select
                    className="input w-auto text-sm"
                    value={u.role}
                    onChange={(e) => changeRole(u, e.target.value as UserRole)}
                  >
                    <option value="admin">admin</option>
                    <option value="viewer">viewer</option>
                  </select>
                </td>
                <td className="p-3 text-slate-400">
                  {u.createdAt
                    ? new Date(u.createdAt).toLocaleDateString()
                    : "—"}
                </td>
                <td className="p-3 text-right space-x-2 whitespace-nowrap">
                  <button
                    className="btn-secondary"
                    onClick={() => resetPassword(u)}
                  >
                    {t("usr.resetPw")}
                  </button>
                  <button className="btn-danger" onClick={() => remove(u)}>
                    {t("common.delete")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
