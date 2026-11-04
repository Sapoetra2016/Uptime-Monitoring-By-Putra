import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api, setUsername as storeUsername, setRole, setToken } from "../lib/api";
import { useI18n } from "../lib/i18n";

export default function LoginPage() {
  const { lang, setLanguage, t } = useI18n();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const res = await api.login(username, password);
      setToken(res.token);
      setRole(res.role ?? "viewer");
      storeUsername(res.username ?? username);
      navigate("/");
    } catch (err: any) {
      setError(err.message || t("login.failed"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center relative">
      <button
        onClick={() => setLanguage(lang === "id" ? "en" : "id")}
        className="absolute top-4 right-4 text-xs px-2 py-1 rounded border border-slate-700 text-slate-400 hover:text-white uppercase"
        title={lang === "id" ? "Ganti ke English" : "Switch to Bahasa Indonesia"}
      >
        {lang === "id" ? "ID" : "EN"}
      </button>
      <form
        onSubmit={submit}
        className="bg-app-panel p-8 rounded-xl w-full max-w-sm border border-slate-800"
      >
        <h1 className="text-xl font-bold mb-1">Uptime Monitoring</h1>
        <p className="text-sm text-slate-400 mb-6">{t("login.subtitle")}</p>
        <label className="block text-sm mb-1 text-slate-300">{t("login.username")}</label>
        <input
          className="w-full mb-4 bg-slate-900 border border-slate-700 rounded px-3 py-2"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <label className="block text-sm mb-1 text-slate-300">{t("login.password")}</label>
        <input
          type="password"
          className="w-full mb-4 bg-slate-900 border border-slate-700 rounded px-3 py-2"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error && <div className="text-app-down text-sm mb-3">{error}</div>}
        <button
          disabled={loading}
          className="w-full bg-app-up/80 hover:bg-app-up text-black font-medium rounded py-2 disabled:opacity-50"
        >
          {loading ? t("login.signing") : t("login.signin")}
        </button>
      </form>
    </div>
  );
}
