import type { ReactNode } from "react";
import type { CredentialView, ModelUsage, QuotaView, UsageWindow } from "../../api/types";
import type { T } from "../../i18n";
import { availableUSD, barView, credState, credSubline, displayName, isDirect, type QuotaEntry } from "../../lib/credentials";
import { fmtCount, fmtTokens, fmtUSD, formatShort, formatTime } from "../../lib/format";
import { store } from "../../state/store";

// A remaining-credit bar: what it is and when it resets on the left, what is
// left on the right, coloured like the bar.
function Bar({ t, label, left, total, value }: { t: T; label: string; left: number; total: number; value: string }) {
  const bar = barView(left, total);
  return (
    <div className={"cap" + bar.level}>
      <div className="cap-head">
        <span className="cap-label" title={label}>{label}</span>
        <span className="cap-left">{t("usage.left")} <strong>{value}</strong></span>
      </div>
      <div className="track" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(bar.fraction * 100)}>
        <span className="fill" style={{ width: bar.width }}></span>
      </div>
    </div>
  );
}

// The plan credit of the period, or a legacy plan's session and weekly limits.
function PlanBars({ t, d, locale }: { t: T; d: QuotaView; locale: string }) {
  const b = d.balance;
  if (!b) return null;
  const bars: ReactNode[] = [];
  if (b.included) {
    const inc = b.included;
    const label = t("usage.included") + (inc.period_until ? " · " + t("usage.resets", { time: formatShort(inc.period_until, locale) }) : "");
    bars.push(<Bar key="included" t={t} label={label} left={inc.balance_usd} total={inc.allowance_usd} value={fmtUSD(inc.balance_usd) + " / " + fmtUSD(inc.allowance_usd)} />);
  }
  for (const [key, limit] of [["session", b.session], ["weekly", b.weekly]] as const) {
    if (!limit) continue;
    const label = t("usage.limit." + key) + (limit.resets_at ? " · " + t("usage.resets", { time: formatShort(limit.resets_at, locale) }) : "");
    bars.push(<Bar key={key} t={t} label={label} left={limit.remaining_percent} total={100} value={Math.round(Number(limit.remaining_percent) || 0) + "%"} />);
  }
  return <>{bars}</>;
}

/** A usage figure: USD when Ollama reports cost, else the request count. */
export function usageValue(t: T, w: UsageWindow | undefined): string {
  if (!w) return "—";
  if (w.usd !== undefined && w.usd !== null) return fmtUSD(w.usd);
  // Short, so a legacy plan's request count fits a figure column.
  return t("usage.requestsShort", { n: fmtCount(w.requests) });
}

/** Requests and tokens of a usage window, for its tooltip. */
export function usageTip(t: T, w: UsageWindow | undefined): string {
  if (!w) return "";
  return t("usage.tip", {
    requests: fmtCount(w.requests),
    input: fmtTokens(w.input_tokens),
    cached: fmtTokens(w.cached_input_tokens),
    output: fmtTokens(w.output_tokens),
  });
}

/** The 30-day top models, one per line. */
function modelsTip(t: T, models: ModelUsage[] | undefined): string {
  if (!Array.isArray(models) || !models.length) return "";
  return [t("usage.topModels")].concat(models.map((m) => {
    const name = m.model || t("usage.otherModels");
    const value = m.usd !== undefined && m.usd !== null ? fmtUSD(m.usd) : t("usage.requests", { n: fmtCount(m.requests) });
    return name + "  " + value;
  })).join("\n");
}

// One figure of a card's usage grid: label over value, details in the tooltip.
function Meter({ label, value, tip }: { label: string; value: string; tip?: string }) {
  return (
    <div className="meter">
      <div className="meter-label" title={tip ? label + " · " + tip : label}>{label}</div>
      <div className="meter-value" title={tip ? value + " · " + tip : value}>{value}</div>
    </div>
  );
}

// Four facts laid out like the usage figures; details are in the tooltip.
function Fact({ label, text, tip, extra, flag }: { label: string; text: string; tip?: string; extra?: ReactNode; flag?: string }) {
  return (
    <div className="meter">
      <div className="meter-label" title={label}>{label}{flag ? <span className="warn-text">{" · " + flag}</span> : null}</div>
      <div className="meter-value text" title={tip || text}>{text}{extra || null}</div>
    </div>
  );
}

function Facts({ t, cred }: { t: T; cred: CredentialView }) {
  const models = Array.isArray(cred.models) ? cred.models : [];
  const missing = Array.isArray(cred.missing_models) ? cred.missing_models : [];
  const modelsText = cred.all_models ? t("models.all") : t("models.selected", { count: models.length });
  const prefixText = cred.effective_prefix ? cred.effective_prefix + "/" : t("prefix.none");
  let proxyText = t("proxy.inherit");
  let proxyTip: string | undefined;
  if (cred.proxy_name) {
    proxyText = cred.proxy_name;
    proxyTip = cred.proxy_url;
  } else if (cred.proxy_url) {
    proxyText = isDirect(cred.proxy_url) ? t("proxy.direct") : cred.proxy_url;
  }
  const ok = cred.success || 0;
  const failed = cred.failed || 0;
  return (
    <div className="facts">
      <Fact
        label={t("card.models")}
        text={modelsText}
        tip={[
          modelsText,
          cred.registered_known ? t("models.registered", { count: cred.registered_models }) : "",
          missing.length ? t("models.missing", { list: missing.join(", ") }) : "",
        ].filter(Boolean).join("\n")}
        flag={missing.length ? t("models.missingCount", { count: missing.length }) : ""}
      />
      <Fact
        label={t("card.prefix")}
        text={prefixText}
        tip={prefixText + "\n" + t(cred.prefix === null || cred.prefix === undefined ? "prefix.default" : "prefix.custom")}
      />
      <Fact label={t("card.proxy")} text={proxyText} tip={proxyTip} />
      <Fact
        label={t("card.requestsLabel")}
        text={ok + " / "}
        tip={t("card.requests", { ok, failed })}
        extra={<span className={failed ? "danger-text" : ""}>{String(failed)}</span>}
      />
    </div>
  );
}

// The card's figures (or why there are none), then the plan bars and notes.
function UsageParts({ t, cred, q, locale }: { t: T; cred: CredentialView; q: QuotaEntry | undefined; locale: string }) {
  if (cred.read_error) return null;
  const d = q && q.data;
  if (!d) {
    const load = () => void store.loadQuota(cred.auth_index);
    return (
      <div className="usage-state">
        {q && q.state === "loading" ? (
          <span>{t("usage.loading")}</span>
        ) : q && q.state === "error" ? (
          <>
            <span className="danger-text">{t("usage.failed", { msg: q.error })}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={load}>{t("usage.retry")}</button>
          </>
        ) : (
          <>
            <span>{t(cred.disabled ? "usage.disabled" : "usage.notLoaded")}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={load}>{t("usage.check")}</button>
          </>
        )}
      </div>
    );
  }
  const u = d.usage;
  const available = availableUSD(d);
  // Kept to the tooltip so the figures stay as tall as the facts beside them.
  const balanceTip = d.balance
    ? [
      d.balance.included ? t("usage.includedPart", { v: fmtUSD(d.balance.included.balance_usd) }) : "",
      t("usage.purchasedPart", { v: fmtUSD(d.balance.purchased_usd) }),
      t("usage.accountNote"),
    ].filter(Boolean).join(" · ")
    : "";
  const monthTip = [usageTip(t, u && u.month), modelsTip(t, u && u.models)].filter(Boolean).join("\n\n");
  return (
    <>
      <div className="meters">
        {available !== null
          ? <Meter label={t("usage.balance")} value={fmtUSD(available)} tip={balanceTip} />
          : <Meter label={t("usage.balance")} value="—" tip={d.balance_error || ""} />}
        <Meter label={t("usage.spent24h")} value={usageValue(t, u && u.day)} tip={usageTip(t, u && u.day)} />
        <Meter label={t("usage.spent7d")} value={usageValue(t, u && u.week)} tip={usageTip(t, u && u.week)} />
        <Meter label={t("usage.spent30d")} value={usageValue(t, u && u.month)} tip={monthTip} />
      </div>
      <PlanBars t={t} d={d} locale={locale} />
      {d.balance_error ? <div className="usage-note danger-text">{t("usage.balanceError", { msg: d.balance_error })}</div> : null}
      {d.usage_error ? <div className="usage-note danger-text">{t("usage.usageError", { msg: d.usage_error })}</div> : null}
      {q && q.state === "error" ? <div className="usage-note danger-text">{t("usage.failed", { msg: q.error })}</div> : null}
    </>
  );
}

export function CredCard({ t, cred, q, armed, busy, locale }: {
  t: T;
  cred: CredentialView;
  q: QuotaEntry | undefined;
  armed: boolean;
  busy: Record<string, true>;
  locale: string;
}) {
  const [badgeClass, badgeKey] = credState(cred);
  // Why the key is off or cooling down, under the figures on its own line.
  const stateNote = cred.read_error || cred.status_message || "";
  const name = displayName(cred);
  return (
    <article className={"cred" + (cred.disabled ? " off" : "")}>
      <div className="cred-head">
        <div className="cred-row">
          <div className="cred-title">
            <span className="cred-name" title={name}>{name}</span>
            <span className={"badge " + badgeClass}>{t(badgeKey)}</span>
          </div>
        </div>
        <div className="cred-row">
          <div className="cred-file" title={cred.name}>{credSubline(cred, t("card.priority"))}</div>
        </div>
      </div>
      <div className="cred-body">
        {cred.read_error ? null : <Facts t={t} cred={cred} />}
        <UsageParts t={t} cred={cred} q={q} locale={locale} />
        {cred.note ? <div className="usage-note sub" title={cred.note}>{t("card.noteLine", { msg: cred.note })}</div> : null}
        {stateNote ? <div className={"usage-note " + (cred.read_error ? "danger-text" : "warn-text")}>{t("card.statusNote", { msg: stateNote })}</div> : null}
      </div>
      <div className="cred-foot">
        <div className="cred-meta">
          {q && q.data && q.data.fetched_at ? <span>{t("usage.fetched", { time: formatTime(q.data.fetched_at, locale) })}</span> : null}
          {!cred.read_error && q && q.data ? (
            <button type="button" className="btn-link" disabled={q.state === "loading"} onClick={() => void store.loadQuota(cred.auth_index)}>
              {t(q.state === "loading" ? "usage.loading" : "usage.refresh")}
            </button>
          ) : null}
        </div>
        <div className="row-actions">
          <button type="button" className="btn btn-secondary btn-sm" disabled={!!cred.read_error} onClick={() => store.openEditor("edit", cred)}>
            {t("row.edit")}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy["toggle:" + cred.name]} onClick={() => void store.toggleDisabled(cred)}>
            {t(cred.disabled ? "row.enable" : "row.disable")}
          </button>
          <button
            type="button"
            className={"btn btn-danger btn-sm" + (armed ? " armed" : "")}
            disabled={!!busy["delete:" + cred.name]}
            onClick={() => void store.deleteCredential(cred)}
          >
            {t(armed ? "row.confirmDelete" : "row.delete")}
          </button>
        </div>
      </div>
    </article>
  );
}
