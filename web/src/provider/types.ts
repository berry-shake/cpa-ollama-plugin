// The Ollama shapes of the management API: the catalog model with its
// details, and the balance and usage of the account behind a key.

/** An Ollama cloud model from /api/tags, with the details of /api/show. */
export interface ModelView {
  id: string;
  /** "completion", "tools", "thinking", "vision", "embedding", ... */
  capabilities?: string[];
  /** False for models without "completion"; "all models" leaves them out. */
  chat: boolean;
  context_length?: number;
  family?: string;
  /** A parameter count ("116829156672") or a label ("8.0B"). */
  parameter_size?: string;
  quantization?: string;
  size?: number;
  modified_at?: string;
  /** Named thinking levels, e.g. low / medium / high. */
  thinking_levels?: string[];
  /** Set for models that only turn thinking on or off. */
  thinking_toggle?: boolean;
  /** The level used when a request sets none ("true" / "false" for toggles). */
  thinking_default?: string;
}

/** The plan credit of the current period. */
export interface IncludedCredits {
  balance_usd: number;
  allowance_usd: number;
  period_from?: string;
  /** When the credit resets (exclusive end of the period). */
  period_until?: string;
}

/** A legacy plan's session or weekly limit. */
export interface PlanLimit {
  /** 0 to 100. */
  remaining_percent: number;
  resets_at?: string;
}

export interface BalanceView {
  /** Absent for legacy plans, which report session and weekly instead. */
  included?: IncludedCredits;
  session?: PlanLimit;
  weekly?: PlanLimit;
  purchased_usd: number;
}

export interface UsageWindow {
  requests: number;
  /** Absent when Ollama reports only request counts (legacy plans). */
  usd?: number;
  /** Includes cached_input_tokens. */
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface ModelUsage {
  /** "" for the models outside the top ten. */
  model: string;
  requests: number;
  usd?: number;
}

export interface UsageView {
  day?: UsageWindow;
  week?: UsageWindow;
  month?: UsageWindow;
  /** The 30-day usage by model, largest first. */
  models?: ModelUsage[];
}

/** Balance and usage of the account behind a key; both are account-wide. */
export interface QuotaView {
  /** Same value for keys whose figures read the same (taken as one account). */
  account?: string;
  balance?: BalanceView;
  balance_error?: string;
  usage?: UsageView;
  usage_error?: string;
  fetched_at: string;
}

export interface CredentialExtra {}

export interface CatalogExtra {
  /** Models whose details (/api/show) failed to load in the last refresh. */
  detail_errors?: number;
}

export interface CredentialSaveExtra {}

export interface EditorExtra {}
