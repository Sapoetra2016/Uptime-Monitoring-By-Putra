import { useEffect, useState, type ReactNode } from "react";
import { useI18n } from "../lib/i18n";
import type {
  EscalationPolicy,
  Monitor,
  MonitorType,
  Notification,
} from "../lib/types";

const emptyMonitor: Partial<Monitor> = {
  name: "",
  type: "http",
  url: "",
  host: "",
  port: 80,
  method: "GET",
  expectedStatus: 200,
  keyword: "",
  dnsServer: "8.8.8.8",
  recordType: "A",
  certWarnDays: 14,
  containerName: "",
  intervalSec: 60,
  timeoutSec: 10,
  pushGraceSec: 30,
  enabled: true,
  notificationIds: [],
  escalationId: null,
  headers: {},
  body: "",
  basicAuthUser: "",
  basicAuthPass: "",
  ignoreTls: false,
  tags: [],
  localIp: "",
  latencyWarnMs: 0,
  sloTargetPct: 0,
  redisPassword: "",
  redisKey: "",
  dbDriver: "postgres",
  dsn: "",
  query: "",
  expectedRows: 0,
};

type HeaderPair = { key: string; value: string };

function headersToPairs(h?: Record<string, string>): HeaderPair[] {
  if (!h) return [];
  return Object.entries(h).map(([key, value]) => ({ key, value }));
}

export default function MonitorForm({
  initial,
  notifications,
  escalations,
  onSave,
  onCancel,
  saving,
}: {
  initial?: Partial<Monitor>;
  notifications: Notification[];
  escalations: EscalationPolicy[];
  onSave: (m: Partial<Monitor>) => void;
  onCancel: () => void;
  saving: boolean;
}) {
  const [form, setForm] = useState<Partial<Monitor>>({
    ...emptyMonitor,
    ...initial,
  });
  const [pairs, setPairs] = useState<HeaderPair[]>(
    headersToPairs(initial?.headers),
  );
  const { t } = useI18n();

  useEffect(() => {
    const base = { ...emptyMonitor, ...initial };
    setForm(base);
    setPairs(headersToPairs(base.headers));
  }, [initial]);

  function set<K extends keyof Monitor>(key: K, value: Monitor[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  function submit() {
    const headers: Record<string, string> = {};
    for (const p of pairs) {
      if (p.key.trim()) headers[p.key.trim()] = p.value;
    }
    onSave({ ...form, headers });
  }

  const type = form.type as MonitorType;
  const showLatency = ["http", "tcp", "ping", "redis", "db"].includes(type);

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="space-y-4"
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label={t("form.name")}>
          <input
            required
            className="input"
            value={form.name ?? ""}
            onChange={(e) => set("name", e.target.value)}
          />
        </Field>
        <Field label={t("form.type")}>
          <select
            className="input"
            value={type}
            onChange={(e) => set("type", e.target.value as MonitorType)}
          >
            <option value="http">{t("form.typeHttp")}</option>
            <option value="tcp">{t("form.typeTcp")}</option>
            <option value="ping">{t("form.typePing")}</option>
            <option value="dns">{t("form.typeDns")}</option>
            <option value="cert">{t("form.typeCert")}</option>
            <option value="push">{t("form.typePush")}</option>
            <option value="docker">{t("form.typeDocker")}</option>
            <option value="redis">{t("form.typeRedis")}</option>
            <option value="db">{t("form.typeDb")}</option>
          </select>
        </Field>
      </div>

      {type === "http" && (
        <Field label={t("form.url")} hint={t("form.urlHint")}>
          <input
            className="input"
            value={form.url ?? ""}
            onChange={(e) => set("url", e.target.value)}
          />
        </Field>
      )}

      {type === "http" && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Field label={t("form.method")}>
              <select
                className="input"
                value={form.method ?? "GET"}
                onChange={(e) => set("method", e.target.value)}
              >
                {["GET", "POST", "PUT", "PATCH", "HEAD", "DELETE"].map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("form.expectedStatus")}>
              <input
                type="number"
                className="input"
                value={form.expectedStatus ?? 200}
                onChange={(e) => set("expectedStatus", Number(e.target.value))}
              />
            </Field>
            <Field label={t("form.keyword")} hint={t("form.keywordHint")}>
              <input
                className="input"
                value={form.keyword ?? ""}
                onChange={(e) => set("keyword", e.target.value)}
              />
            </Field>
          </div>

          <Field label={t("form.body")} hint={t("form.bodyHint")}>
            <textarea
              rows={3}
              className="input font-mono text-xs"
              value={form.body ?? ""}
              onChange={(e) => set("body", e.target.value)}
            />
          </Field>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <Field label={t("form.authUser")}>
              <input
                className="input"
                value={form.basicAuthUser ?? ""}
                onChange={(e) => set("basicAuthUser", e.target.value)}
              />
            </Field>
            <Field label={t("form.authPass")}>
              <input
                type="password"
                className="input"
                value={form.basicAuthPass ?? ""}
                onChange={(e) => set("basicAuthPass", e.target.value)}
              />
            </Field>
          </div>

          <div>
            <div className="flex items-center justify-between mb-1">
              <span className="text-slate-300 text-sm">
                {t("form.headers")}
              </span>
              <button
                type="button"
                className="text-xs text-app-up hover:underline"
                onClick={() => setPairs((p) => [...p, { key: "", value: "" }])}
              >
                {t("form.addHeader")}
              </button>
            </div>
            {pairs.length === 0 && (
              <div className="text-xs text-slate-500">
                {t("form.noHeaders")}
              </div>
            )}
            <div className="space-y-2">
              {pairs.map((p, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    className="input w-1/3"
                    placeholder={t("form.headerName")}
                    value={p.key}
                    onChange={(e) =>
                      setPairs((prev) =>
                        prev.map((x, j) =>
                          j === i ? { ...x, key: e.target.value } : x,
                        ),
                      )
                    }
                  />
                  <input
                    className="input flex-1"
                    placeholder={t("form.headerValue")}
                    value={p.value}
                    onChange={(e) =>
                      setPairs((prev) =>
                        prev.map((x, j) =>
                          j === i ? { ...x, value: e.target.value } : x,
                        ),
                      )
                    }
                  />
                  <button
                    type="button"
                    className="btn-danger px-3"
                    onClick={() =>
                      setPairs((prev) => prev.filter((_, j) => j !== i))
                    }
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          </div>

          <label className="flex items-start gap-2 text-sm text-slate-300">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={form.ignoreTls ?? false}
              onChange={(e) => set("ignoreTls", e.target.checked)}
            />
            <span>
              {t("form.ignoreSsl")}
              <span className="block text-xs text-slate-500">
                {t("form.ignoreSslHint")}
              </span>
            </span>
          </label>
        </>
      )}

      {(type === "tcp" || type === "redis") && (
        <div className="grid grid-cols-2 gap-4">
          <Field label={t("form.host")}>
            <input
              className="input"
              value={form.host ?? ""}
              onChange={(e) => set("host", e.target.value)}
            />
          </Field>
          <Field label={t("form.port")}>
            <input
              type="number"
              className="input"
              value={form.port ?? (type === "redis" ? 6379 : 80)}
              onChange={(e) => set("port", Number(e.target.value))}
            />
          </Field>
        </div>
      )}

      {type === "redis" && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field
            label={t("form.redisPassword")}
            hint={t("form.redisPasswordHint")}
          >
            <input
              type="password"
              className="input"
              value={form.redisPassword ?? ""}
              onChange={(e) => set("redisPassword", e.target.value)}
            />
          </Field>
          <Field label={t("form.redisKey")} hint={t("form.redisKeyHint")}>
            <input
              className="input"
              value={form.redisKey ?? ""}
              onChange={(e) => set("redisKey", e.target.value)}
            />
          </Field>
        </div>
      )}

      {type === "ping" && (
        <Field label={t("form.pingHost")} hint={t("form.pingHostHint")}>
          <input
            className="input"
            value={form.host ?? ""}
            onChange={(e) => set("host", e.target.value)}
          />
        </Field>
      )}

      {type === "db" && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <Field label={t("form.driver")}>
              <select
                className="input"
                value={form.dbDriver ?? "postgres"}
                onChange={(e) => set("dbDriver", e.target.value)}
              >
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL</option>
              </select>
            </Field>
            <Field label={t("form.minRows")} hint={t("form.minRowsHint")}>
              <input
                type="number"
                className="input"
                value={form.expectedRows ?? 0}
                onChange={(e) => set("expectedRows", Number(e.target.value))}
              />
            </Field>
          </div>
          <Field label={t("form.dsn")} hint={t("form.dsnHint")}>
            <input
              className="input font-mono text-xs"
              value={form.dsn ?? ""}
              onChange={(e) => set("dsn", e.target.value)}
            />
          </Field>
          <Field label={t("form.query")} hint={t("form.queryHint")}>
            <textarea
              rows={2}
              className="input font-mono text-xs"
              value={form.query ?? ""}
              onChange={(e) => set("query", e.target.value)}
            />
          </Field>
        </>
      )}

      {type === "dns" && (
        <div className="grid grid-cols-3 gap-4">
          <Field label={t("form.domain")}>
            <input
              className="input"
              value={form.host ?? ""}
              onChange={(e) => set("host", e.target.value)}
            />
          </Field>
          <Field label={t("form.dnsServer")}>
            <input
              className="input"
              value={form.dnsServer ?? ""}
              onChange={(e) => set("dnsServer", e.target.value)}
            />
          </Field>
          <Field label={t("form.recordType")}>
            <select
              className="input"
              value={form.recordType ?? "A"}
              onChange={(e) => set("recordType", e.target.value)}
            >
              {["A", "AAAA", "CNAME", "MX", "TXT", "NS"].map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}

      {type === "cert" && (
        <Field label={t("form.certHost")} hint={t("form.certHostHint")}>
          <input
            className="input"
            value={form.host ?? ""}
            onChange={(e) => set("host", e.target.value)}
          />
        </Field>
      )}

      {type === "cert" && (
        <Field label={t("form.certWarnDays")}>
          <input
            type="number"
            className="input"
            value={form.certWarnDays ?? 14}
            onChange={(e) => set("certWarnDays", Number(e.target.value))}
          />
        </Field>
      )}

      {type === "docker" && (
        <Field label={t("form.container")}>
          <input
            className="input"
            value={form.containerName ?? ""}
            onChange={(e) => set("containerName", e.target.value)}
          />
        </Field>
      )}

      {type === "push" && (
        <div className="grid grid-cols-2 gap-4">
          <Field label={t("form.gracePeriod")} hint={t("form.graceHint")}>
            <input
              type="number"
              className="input"
              value={form.pushGraceSec ?? 30}
              onChange={(e) => set("pushGraceSec", Number(e.target.value))}
            />
          </Field>
          {form.pushToken && (
            <Field
              label={t("form.heartbeatUrl")}
              hint={t("form.heartbeatHint")}
            >
              <input
                readOnly
                className="input text-xs"
                value={`${window.location.origin}/api/push/${form.pushToken}`}
                onFocus={(e) => e.target.select()}
              />
            </Field>
          )}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <Field label={t("form.interval")}>
          <input
            type="number"
            className="input"
            value={form.intervalSec ?? 60}
            onChange={(e) => set("intervalSec", Number(e.target.value))}
          />
        </Field>
        <Field label={t("form.timeout")}>
          <input
            type="number"
            className="input"
            value={form.timeoutSec ?? 10}
            onChange={(e) => set("timeoutSec", Number(e.target.value))}
          />
        </Field>
        {showLatency && (
          <Field label={t("form.latencySlo")} hint={t("form.latencySloHint")}>
            <input
              type="number"
              className="input"
              value={form.latencyWarnMs ?? 0}
              onChange={(e) => set("latencyWarnMs", Number(e.target.value))}
            />
          </Field>
        )}
        <Field label={t("form.uptimeSlo")} hint={t("form.uptimeSloHint")}>
          <input
            type="number"
            step="0.01"
            min="0"
            max="100"
            className="input"
            value={form.sloTargetPct ?? 0}
            onChange={(e) => set("sloTargetPct", Number(e.target.value))}
          />
        </Field>
      </div>

      <Field label={t("form.tags")} hint={t("form.tagsHint")}>
        <input
          className="input"
          value={(form.tags ?? []).join(", ")}
          onChange={(e) =>
            set(
              "tags",
              e.target.value
                .split(",")
                .map((t) => t.trim())
                .filter(Boolean),
            )
          }
        />
      </Field>

      <Field label={t("form.localIp")} hint={t("form.localIpHint")}>
        <input
          className="input font-mono text-xs"
          placeholder={t("form.localIpPh")}
          value={form.localIp ?? ""}
          onChange={(e) => set("localIp", e.target.value)}
        />
      </Field>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label={t("form.notifications")}>
          <select
            multiple
            className="input h-24"
            value={form.notificationIds ?? []}
            onChange={(e) =>
              set(
                "notificationIds",
                Array.from(e.target.selectedOptions).map((o) => o.value),
              )
            }
          >
            {notifications.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name} ({n.type})
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("form.escalation")}>
          <select
            className="input"
            value={form.escalationId ?? ""}
            onChange={(e) =>
              set("escalationId", (e.target.value || null) as any)
            }
          >
            <option value="">{t("form.none")}</option>
            {escalations.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={form.enabled ?? true}
          onChange={(e) => set("enabled", e.target.checked)}
        />
        {t("form.enabled")}
      </label>

      <div className="flex justify-end gap-2 pt-2">
        <button type="button" onClick={onCancel} className="btn-secondary">
          {t("common.cancel")}
        </button>
        <button type="submit" disabled={saving} className="btn-primary">
          {saving ? t("common.saving") : t("form.save")}
        </button>
      </div>
    </form>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="block text-sm">
      <span className="text-slate-300">{label}</span>
      {hint && (
        <span className="block text-xs text-slate-500 mb-1">{hint}</span>
      )}
      <div className="mt-1">{children}</div>
    </label>
  );
}
