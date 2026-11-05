import { useCallback, useEffect, useState } from "react";
import { api } from "../lib/api";
import { t, useI18n } from "../lib/i18n";
import type { DomainIntel } from "../lib/types";

// DomainIntelCard runs the outside-in domain audit (DNS + email security +
// security headers + detailed SSL) and renders it as an ops-friendly report.
// It self-loads on mount and can be re-run on demand.
export default function DomainIntelCard({ id }: { id: string }) {
  const [data, setData] = useState<DomainIntel | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const { lang } = useI18n();

  const run = useCallback(() => {
    setLoading(true);
    setError("");
    api
      .domainIntel(id)
      .then((res) => setData(res))
      .catch((e: any) => setError(e?.message || t("di.failed")))
      .finally(() => setLoading(false));
  }, [id]);

  useEffect(() => {
    if (id) run();
  }, [id, run]);

  return (
    <div className="bg-app-panel border border-slate-800 rounded-lg p-4">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
        <h2 className="text-sm font-medium text-slate-300">
          {t("di.title")}
          {data?.domain && (
            <span className="ml-2 text-sky-400">{data.domain}</span>
          )}
        </h2>
        <button className="btn-secondary" onClick={run} disabled={loading}>
          {loading ? t("di.auditing") : t("di.reaudit")}
        </button>
      </div>

      {loading && !data && (
        <div className="text-sm text-slate-500">{t("di.loading")}</div>
      )}
      {error && <div className="text-sm text-app-down mb-2">{error}</div>}
      {data?.error && !data.host && (
        <div className="text-sm text-slate-500">{data.error}</div>
      )}

      {data && data.host && (
        <div className="space-y-4" key={lang}>
          {data.ssl && <SSLSection ssl={data.ssl} />}
          {data.dns && <DNSSection dns={data.dns} />}
          {data.email && <EmailSection email={data.email} />}
          {data.headers && <HeadersSection headers={data.headers} />}
        </div>
      )}
    </div>
  );
}

// ---- badges / chips ------------------------------------------------------
function Badge({
  tone,
  children,
}: {
  tone: "good" | "bad" | "warn" | "neutral";
  children: React.ReactNode;
}) {
  const cls =
    tone === "good"
      ? "bg-app-up/15 text-app-up"
      : tone === "bad"
        ? "bg-app-down/15 text-app-down"
        : tone === "warn"
          ? "bg-amber-500/15 text-amber-400"
          : "bg-slate-800 text-slate-400";
  return (
    <span className={`text-[11px] px-2 py-0.5 rounded-full ${cls}`}>
      {children}
    </span>
  );
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[11px] font-mono px-2 py-0.5 rounded bg-slate-800/70 text-slate-300 break-all">
      {children}
    </span>
  );
}

function SubHead({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-xs uppercase tracking-wide text-slate-500 mb-2">
      {children}
    </div>
  );
}

// ---- sections ------------------------------------------------------------
function SSLSection({ ssl }: { ssl: NonNullable<DomainIntel["ssl"]> }) {
  return (
    <div className="border-t border-slate-800 pt-3 first:border-0 first:pt-0">
      <div className="flex items-center justify-between mb-2">
        <SubHead>SSL / TLS</SubHead>
        {ssl.ok && (
          <Badge
            tone={ssl.score >= 80 ? "good" : ssl.score >= 50 ? "warn" : "bad"}
          >
            {t("di.score", { n: ssl.score })}
          </Badge>
        )}
      </div>
      {!ssl.ok ? (
        <div className="text-sm text-slate-500">
          — <span className="text-xs break-all">{ssl.error}</span>
        </div>
      ) : (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge
              tone={ssl.expired ? "bad" : ssl.expiresSoon ? "warn" : "good"}
            >
              {ssl.expired
                ? t("det.expiredAgo", { n: -ssl.daysRemaining })
                : t("di.daysLeft", { n: ssl.daysRemaining })}
            </Badge>
            {ssl.protocol && <Chip>{ssl.protocol}</Chip>}
            {ssl.keyAlgorithm && <Chip>{ssl.keyAlgorithm}</Chip>}
            <Badge
              tone={ssl.chainTrusted ? "good" : ssl.selfSigned ? "warn" : "bad"}
            >
              {ssl.chainTrusted
                ? t("di.chainTrusted")
                : ssl.selfSigned
                  ? t("di.selfSigned")
                  : t("di.chainUntrusted")}
            </Badge>
          </div>
          <div className="text-xs text-slate-400 grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1">
            <div>
              <span className="text-slate-500">{t("di.subject")}</span>{" "}
              {ssl.subject || "—"}
            </div>
            <div>
              <span className="text-slate-500">{t("di.issuer")}</span>{" "}
              {ssl.issuer || "—"}
            </div>
            {ssl.notAfter && (
              <div>
                <span className="text-slate-500">{t("di.validUntil")}</span>{" "}
                {new Date(ssl.notAfter).toLocaleDateString()}
              </div>
            )}
          </div>
          {ssl.sans && ssl.sans.length > 0 && (
            <div className="flex flex-wrap gap-1 pt-1">
              {ssl.sans.slice(0, 12).map((s) => (
                <Chip key={s}>{s}</Chip>
              ))}
              {ssl.sans.length > 12 && (
                <span className="text-[11px] text-slate-500">
                  +{ssl.sans.length - 12} {t("di.moreSuffix")}
                </span>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function DNSSection({ dns }: { dns: NonNullable<DomainIntel["dns"]> }) {
  const hasAny =
    dns.a?.length ||
    dns.aaaa?.length ||
    dns.cname ||
    dns.mx?.length ||
    dns.ns?.length ||
    dns.txt?.length;
  return (
    <div className="border-t border-slate-800 pt-3">
      <SubHead>DNS Records</SubHead>
      {!hasAny ? (
        <div className="text-sm text-slate-500">
          — <span className="text-xs break-all">{dns.error}</span>
        </div>
      ) : (
        <div className="space-y-1.5 text-sm">
          {dns.a && dns.a.length > 0 && (
            <Row label="A">
              <div className="flex flex-wrap gap-1 justify-end">
                {dns.a.map((x) => (
                  <Chip key={x}>{x}</Chip>
                ))}
              </div>
            </Row>
          )}
          {dns.aaaa && dns.aaaa.length > 0 && (
            <Row label="AAAA">
              <div className="flex flex-wrap gap-1 justify-end">
                {dns.aaaa.map((x) => (
                  <Chip key={x}>{x}</Chip>
                ))}
              </div>
            </Row>
          )}
          {dns.cname && (
            <Row label="CNAME">
              <Chip>{dns.cname}</Chip>
            </Row>
          )}
          {dns.ns && dns.ns.length > 0 && (
            <Row label="NS">
              <div className="flex flex-wrap gap-1 justify-end">
                {dns.ns.map((x) => (
                  <Chip key={x}>{x}</Chip>
                ))}
              </div>
            </Row>
          )}
          {dns.mx && dns.mx.length > 0 && (
            <Row label="MX">
              <div className="flex flex-wrap gap-1 justify-end">
                {dns.mx.map((m) => (
                  <Chip key={m.host}>
                    {m.pref} · {m.host}
                  </Chip>
                ))}
              </div>
            </Row>
          )}
          {dns.txt && dns.txt.length > 0 && (
            <details className="text-xs">
              <summary className="cursor-pointer text-slate-500 hover:text-slate-300">
                {t("di.txtRecords", { n: dns.txt.length })}
              </summary>
              <div className="mt-1.5 flex flex-col gap-1">
                {dns.txt.map((t, i) => (
                  <div
                    key={i}
                    className="text-slate-400 break-all font-mono text-[11px]"
                  >
                    {t}
                  </div>
                ))}
              </div>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

function EmailSection({ email }: { email: NonNullable<DomainIntel["email"]> }) {
  const spfOk = email.spf?.present;
  const dmarcOk = email.dmarc?.present;
  const dkimOk = (email.dkim?.length ?? 0) > 0;
  return (
    <div className="border-t border-slate-800 pt-3">
      <SubHead>{t("di.emailSection")}</SubHead>
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={spfOk ? (email.spf?.ok ? "good" : "warn") : "bad"}>
          SPF{" "}
          {spfOk ? (email.spf?.ok ? "OK" : t("di.spfWeak")) : t("di.spfNone")}
        </Badge>
        <Badge tone={dmarcOk ? (email.dmarc?.ok ? "good" : "warn") : "bad"}>
          DMARC{" "}
          {dmarcOk
            ? `${email.dmarc?.policy || "?"}${email.dmarc?.pct != null ? ` @${email.dmarc.pct}%` : ""}`
            : t("di.dmarcNone")}
        </Badge>
        <Badge
          tone={
            dkimOk
              ? email.dkim?.some((d) => d.ok)
                ? "good"
                : "warn"
              : "neutral"
          }
        >
          DKIM {dkimOk ? `${email.dkim?.length} selector` : t("di.dkimNone")}
        </Badge>
      </div>
      {email.dkim && email.dkim.length > 0 && (
        <div className="flex flex-wrap gap-1 mt-2">
          {email.dkim.map((d) => (
            <Chip key={d.selector}>
              {d.selector}
              {d.ok ? " ✓" : ""}
            </Chip>
          ))}
        </div>
      )}
    </div>
  );
}

function HeadersSection({
  headers,
}: {
  headers: NonNullable<DomainIntel["headers"]>;
}) {
  const tone =
    headers.score >= 80 ? "good" : headers.score >= 40 ? "warn" : "bad";
  return (
    <div className="border-t border-slate-800 pt-3">
      <div className="flex items-center justify-between mb-2">
        <SubHead>{t("di.headersSection")}</SubHead>
        <Badge tone={tone}>{t("di.score", { n: headers.score })}</Badge>
      </div>
      {!headers.checks?.length ? (
        <div className="text-sm text-slate-500">
          —{" "}
          <span className="text-xs break-all">
            {headers.error || t("di.headersUnreachable")}
          </span>
        </div>
      ) : (
        <ul className="space-y-1.5">
          {headers.checks.map((c) => (
            <li key={c.name} className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="text-sm text-slate-200 flex items-center gap-2">
                  <span
                    className={`w-1.5 h-1.5 rounded-full shrink-0 ${c.good ? "bg-app-up" : c.present ? "bg-amber-400" : "bg-slate-600"}`}
                  />
                  <span className="font-mono text-xs break-all">{c.name}</span>
                </div>
                <div className="text-[11px] text-slate-500 ml-3.5">
                  {c.note}
                </div>
              </div>
              <span
                className="text-[11px] shrink-0 text-slate-500 max-w-[45%] truncate"
                title={c.value}
              >
                {c.good
                  ? "OK"
                  : c.present
                    ? t("di.hdrWeak")
                    : t("di.hdrMissing")}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Row({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-slate-500 text-xs shrink-0">{label}</span>
      <div className="text-right text-slate-200 min-w-0">{children}</div>
    </div>
  );
}
