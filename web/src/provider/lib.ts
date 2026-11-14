// Ollama figures: the account's credit, the summary totals and model details.

import type { CredentialView } from "@core/api/types";
import type { T } from "@core/i18n";
import type { QuotaMap } from "@core/lib/credentials";
import type { ModelView, QuotaView, UsageWindow } from "./types";

/**
 * A model's parameter size: Ollama reports either a raw count
 * ("116829156672" -> "117B") or a label ("8.0B", kept as is).
 */
export function fmtParams(raw: string | undefined | null): string {
  const text = String(raw || "").trim();
  if (!/^\d+$/.test(text)) return text;
  const n = Number(text);
  if (n >= 1e12) return Number((n / 1e12).toPrecision(3)) + "T";
  if (n >= 1e9) return Number((n / 1e9).toPrecision(3)) + "B";
  if (n >= 1e6) return Number((n / 1e6).toPrecision(3)) + "M";
  return text;
}

/** The capabilities shown next to a model besides plain completion. */
export const SHOWN_CAPABILITIES = ["vision", "thinking", "tools", "embedding", "decision"];

/** A model's notable capabilities, e.g. "Vision · Thinking · Tools". */
export function modelTags(t: T, model: Pick<ModelView, "capabilities">): string {
  const caps = Array.isArray(model.capabilities) ? model.capabilities : [];
  return SHOWN_CAPABILITIES.filter((c) => caps.includes(c)).map((c) => t("cap." + c)).join(" · ");
}

/** Credit left on an account: the plan credit of the period plus purchased credit. */
export function availableUSD(d: Pick<QuotaView, "balance">): number | null {
  const b = d.balance;
  if (!b) return null;
  return (b.included ? Number(b.included.balance_usd) || 0 : 0) + (Number(b.purchased_usd) || 0);
}

/** Totals for the summary strip of the credentials tab. */
export interface KeySummary {
  /** Distinct accounts among the keys whose figures loaded. */
  accounts: number;
  /** Accounts that reported a balance, and their credit left. */
  balanceAccounts: number;
  available: number;
  included: number;
  purchased: number;
  /** Accounts that reported usage, and their usage in USD. */
  usageAccounts: number;
  daily: number;
  weekly: number;
  monthly: number;
  loaded: number;
  pending: number;
  failed: number;
  loading: boolean;
}

function windowUSD(w: UsageWindow | undefined): number {
  return w ? Number(w.usd) || 0 : 0;
}

// Ollama's balance and usage both belong to the account, so keys of the same
// account (same figures, same account value) are counted once.
export function summarizeKeys(creds: CredentialView[], quota: QuotaMap): KeySummary {
  const accounts = new Map<string, QuotaView>();
  const sum: KeySummary = {
    accounts: 0, balanceAccounts: 0, available: 0, included: 0, purchased: 0,
    usageAccounts: 0, daily: 0, weekly: 0, monthly: 0, loaded: 0, pending: 0, failed: 0, loading: false,
  };
  for (const cred of creds) {
    if (cred.read_error) continue;
    const q = quota[cred.auth_index];
    if (q && q.state === "loading") sum.loading = true;
    const d = q && q.data;
    if (!d) {
      if (q && q.state === "error") sum.failed++;
      else if (!cred.disabled) sum.pending++;
      continue;
    }
    sum.loaded++;
    const account = d.account || "auth:" + cred.auth_index;
    if (!accounts.has(account)) accounts.set(account, d);
  }
  for (const d of accounts.values()) {
    if (d.balance) {
      sum.balanceAccounts++;
      const included = d.balance.included ? Number(d.balance.included.balance_usd) || 0 : 0;
      const purchased = Number(d.balance.purchased_usd) || 0;
      sum.included += included;
      sum.purchased += purchased;
      sum.available += included + purchased;
    }
    if (d.usage) {
      sum.usageAccounts++;
      sum.daily += windowUSD(d.usage.day);
      sum.weekly += windowUSD(d.usage.week);
      sum.monthly += windowUSD(d.usage.month);
    }
  }
  sum.accounts = accounts.size;
  return sum;
}
