import type { ReactNode } from "react";
import { Bar, Meter, StatItem, UsagePending } from "@core/components/keys/parts";
import type { KeyCardProps, ProviderComponents } from "@core/contract";
import type { CredentialView } from "@core/api/types";
import type { T } from "@core/i18n";
import { fmtCount, fmtTokens, fmtUSD, formatShort } from "@core/lib/format";
import type { State } from "@core/state/store";
import { availableUSD, fmtParams, modelTags, summarizeKeys } from "./lib";
import type { ModelUsage, ModelView, QuotaView, UsageWindow } from "./types";

// Totals over the accounts whose figures have been loaded; keys of the same
// account count once.
function KeySummary({ t, s, creds }: { t: T; s: State; creds: CredentialView[] }) {
  const sum = summarizeKeys(creds, s.quota);
  const blank = sum.loading ? "…" : "—";
  const balanceNote: string[] = [];
  if (sum.accounts > 1) balanceNote.push(t("summary.accounts", { n: sum.accounts }));
  if (sum.balanceAccounts) {
    balanceNote.push(t("usage.includedPart", { v: fmtUSD(sum.included) }));
    balanceNote.push(t("usage.purchasedPart", { v: fmtUSD(sum.purchased) }));
  }
  const missingNote: string[] = [];
  if (sum.failed) missingNote.push(t("summary.failed", { n: sum.failed }));
  if (sum.pending) missingNote.push(t("summary.pending", { n: sum.pending }));
  return (
    <div className="summary strip" id="key-summary">
      <StatItem label={t("summary.balance")} value={sum.balanceAccounts ? fmtUSD(sum.available) : blank} note={balanceNote.join(" · ")} />
      <StatItem label={t("usage.spent24h")} value={sum.usageAccounts ? fmtUSD(sum.daily) : blank} />
      <StatItem label={t("usage.spent7d")} value={sum.usageAccounts ? fmtUSD(sum.weekly) : blank} />
      <StatItem label={t("usage.spent30d")} value={sum.usageAccounts ? fmtUSD(sum.monthly) : blank} note={missingNote.join(" · ")} />
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

// The card's figures (or why there are none), then the plan bars and notes.
function KeyUsage({ t, cred, q, locale }: KeyCardProps) {
  if (cred.read_error) return null;
  const d = q && q.data;
  if (!d) return <UsagePending t={t} cred={cred} q={q} />;
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

// Named levels with the default marked, or on / off for toggle-only models.
function ThinkingCell({ t, m }: { t: T; m: ModelView }) {
  const levels = Array.isArray(m.thinking_levels) ? m.thinking_levels : [];
  if (levels.length) {
    return (
      <td>
        <div>{levels.join(" / ")}</div>
        {m.thinking_default ? <div className="sub">{t("catalog.thinkingDefault", { v: m.thinking_default })}</div> : null}
      </td>
    );
  }
  if (m.thinking_toggle) {
    const on = m.thinking_default === "true";
    return (
      <td className="nowrap">
        <div>{t("catalog.thinkingToggle")}</div>
        {m.thinking_default ? <div className="sub">{t("catalog.thinkingDefault", { v: t(on ? "catalog.thinkingOn" : "catalog.thinkingOff") })}</div> : null}
      </td>
    );
  }
  return <td className="nowrap"><span className="sub">—</span></td>;
}

function ModelCells({ t, m }: { t: T; m: ModelView }) {
  return (
    <>
      <td>
        <div className="mono strong">{m.id}</div>
        {m.family ? <div className="sub">{m.family}</div> : null}
      </td>
      <td>
        <div>{modelTags(t, m) || "—"}</div>
        {m.chat === false ? <div className="sub warn-text">{t("catalog.notChat")}</div> : null}
      </td>
      <td className="nowrap">
        <div>{m.context_length ? fmtTokens(m.context_length) : "—"}</div>
        {m.parameter_size || m.quantization ? (
          <div className="sub">{[fmtParams(m.parameter_size), m.quantization || ""].filter(Boolean).join(" · ")}</div>
        ) : null}
      </td>
      <ThinkingCell t={t} m={m} />
    </>
  );
}

export const components: ProviderComponents = {
  KeySummary,
  KeyUsage,
  modelColumns: ["col.model", "col.capabilities", "col.context", "col.thinking"],
  ModelCells,
};
