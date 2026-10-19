/**
 * The typed quota snapshot the backend stores per credential and serves as
 * `quota_snapshot` on each auth-file entry of `GET /v8/management/auth-files`.
 *
 * Mirrors `sdk/cliproxy/auth/quota_snapshot.go`. The Go golden file
 * `sdk/cliproxy/auth/testdata/quota_snapshot_claude.json` is read by both test
 * suites, so a field rename on either side fails a test.
 */

export type QuotaProviderKind = 'claude' | 'codex' | 'xai' | 'devin';

export type SectionStatus = 'ok' | 'failed' | 'absent' | 'not_applicable';

export interface Section<T> {
  status: SectionStatus;
  /** The last good value. It survives a later failure, so the UI can show "stale since". */
  value: T | null;
  value_at: string | null;
  checked_at: string | null;
  error?: string;
}

export type QuotaWindowSource = 'poll' | 'headers' | 'refresh';

export interface QuotaWindow {
  id: string;
  /** Pool class such as `claude:7d`; empty for display-only windows. */
  class: string;
  /** Upstream-named windows only (Codex additional limits). */
  label?: string;
  /** Absent for credential-wide windows; a model family such as `fable` otherwise. */
  model_scope?: string;
  /** Percent used, 0..100; null when unknown. */
  used_percent: number | null;
  /** Upstream said exhausted, even when `used_percent` is unknown. */
  limit_reached: boolean;
  reset_at: string | null;
  period_seconds: number | null;
  /** The upstream sub-request that owns the window: `claude.usage`, `xai.weekly`, `headers`, ... */
  section: string;
  source: QuotaWindowSource;
  observed_at: string;
}

export interface ClaudeExtraUsage {
  enabled: boolean;
  monthly_limit: number;
  used_credits: number;
  utilization: number | null;
}

export interface CodexAccess {
  allowed: boolean | null;
  limit_reached: boolean | null;
}

export interface CodexCredits {
  has_credits: boolean | null;
  unlimited: boolean | null;
  balance: string | null;
}

export interface CodexResetCredit {
  id: string;
  status: string;
  granted_at: string | null;
  expires_at: string | null;
}

export interface CodexResetCredits {
  available: number | null;
  applicable_available: number | null;
  credits: CodexResetCredit[];
}

export interface XaiProductUsage {
  product: string;
  used: number | null;
}

export interface XaiWeekly {
  period_start: string | null;
  period_end: string | null;
  product_usage: XaiProductUsage[];
}

export interface XaiMonthly {
  limit_cents: number | null;
  used_cents: number | null;
  included_used_cents: number | null;
  on_demand_cap_cents: number | null;
  on_demand_used_cents: number | null;
  prepaid_balance_cents: number | null;
  period_start: string | null;
  period_end: string | null;
}

export interface XaiAccount {
  plan_label: string;
  tier: string;
}

export interface XaiPaidHealth {
  user_id: string;
  team_id: string;
  chat_ok: boolean;
  source: string;
}

export interface DevinPlan {
  name: string;
  start: string | null;
  end: string | null;
}

export type QuotaExtras =
  | {
      kind: 'claude';
      plan: Section<string>;
      extra_usage: Section<ClaudeExtraUsage>;
    }
  | {
      kind: 'codex';
      plan: Section<string>;
      access: Section<CodexAccess>;
      credits: Section<CodexCredits>;
      subscription: Section<{ active_until: string | null }>;
      reset_credits: Section<CodexResetCredits>;
    }
  | {
      kind: 'xai';
      access: 'grok_cli' | 'api_key' | '';
      weekly: Section<XaiWeekly>;
      monthly: Section<XaiMonthly>;
      account: Section<XaiAccount>;
      paid_health: Section<XaiPaidHealth>;
    }
  | {
      kind: 'devin';
      plan: Section<DevinPlan>;
    };

export interface QuotaPollStatus {
  at: string;
  trigger: 'background' | 'on_demand';
  error?: string;
}

export interface QuotaSnapshot {
  provider: QuotaProviderKind | '';
  windows: QuotaWindow[];
  /** Null until the first observation. `extras.kind` equals `provider`. */
  extras: QuotaExtras | null;
  last_poll: QuotaPollStatus | null;
  updated_at: string | null;
}
