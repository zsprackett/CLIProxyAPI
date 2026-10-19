/**
 * Readers of `quota_snapshot` shared by the ledger, the timeline, the reset
 * schedule, and the auth-file card, so they agree on labels and staleness.
 */

import type {
  QuotaExtras,
  QuotaProviderKind,
  QuotaSnapshot,
  QuotaWindow,
  SectionStatus,
} from './types';

export type KnownQuotaSnapshot = QuotaSnapshot & { provider: QuotaProviderKind };

const SNAPSHOT_PROVIDERS: readonly string[] = ['claude', 'codex', 'xai', 'devin'];

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/**
 * The snapshot of a listing entry when it belongs to `provider` and holds at
 * least one window; null otherwise (other providers get an empty snapshot).
 */
export function readQuotaSnapshot(provider: string, value: unknown): KnownQuotaSnapshot | null {
  if (!isRecord(value) || !Array.isArray(value.windows)) return null;
  if (value.provider !== provider || !SNAPSHOT_PROVIDERS.includes(provider)) return null;
  const windows = value.windows.filter(
    (window): window is QuotaWindow => isRecord(window) && typeof window.id === 'string'
  );
  if (windows.length === 0) return null;
  return {
    ...(value as unknown as KnownQuotaSnapshot),
    extras: isRecord(value.extras) ? (value.extras as unknown as QuotaExtras) : null,
    windows,
  };
}

const SECTION_STATUS: Record<string, (extras: QuotaExtras) => SectionStatus | undefined> = {
  'claude.usage': (extras) => (extras.kind === 'claude' ? extras.extra_usage?.status : undefined),
  'codex.usage': (extras) => (extras.kind === 'codex' ? extras.access?.status : undefined),
  'xai.weekly': (extras) => (extras.kind === 'xai' ? extras.weekly?.status : undefined),
  'xai.monthly': (extras) => (extras.kind === 'xai' ? extras.monthly?.status : undefined),
  'devin.status': (extras) => (extras.kind === 'devin' ? extras.plan?.status : undefined),
};

/**
 * When the window's data went stale: its `observed_at` once the sub-request
 * that owns it (the extras field sharing its section) last failed. Header
 * windows have no sub-request, so they are never stale by status.
 */
export function windowStaleSinceMs(snapshot: QuotaSnapshot, window: QuotaWindow): number | null {
  const read = SECTION_STATUS[window.section];
  if (!read || !snapshot.extras || read(snapshot.extras) !== 'failed') return null;
  const observedAtMs = Date.parse(window.observed_at);
  return Number.isFinite(observedAtMs) ? observedAtMs : null;
}

export interface QuotaWindowLabel {
  labelKey?: string;
  labelParams?: Record<string, string | number>;
  label?: string;
}

const CLAUDE_LABELED_IDS = new Set([
  'five_hour',
  'seven_day',
  'seven_day_fable',
  'seven_day_oauth_apps',
  'seven_day_opus',
  'seven_day_sonnet',
  'seven_day_cowork',
]);

const CODEX_CLASS_LABELS: Record<string, string> = {
  'codex:5h': 'codex_quota.primary_window',
  'codex:weekly': 'codex_quota.secondary_window',
  'codex:monthly': 'codex_quota.team_secondary_window',
};

const WEEK_SECONDS = 7 * 24 * 60 * 60;

const codexDisplayLabel = (window: QuotaWindow): QuotaWindowLabel | null => {
  const match = /^(code_review|additional\.(.+))\.(primary|secondary)$/.exec(window.id);
  if (!match) return null;
  const [, group, limitName, slot] = match;
  const monthly = (window.period_seconds ?? 0) > WEEK_SECONDS;
  const suffix =
    slot === 'primary' ? 'primary_window' : monthly ? 'team_secondary_window' : 'secondary_window';
  if (group === 'code_review') return { labelKey: `codex_quota.code_review_${suffix}` };
  return {
    labelKey: `codex_quota.additional_${suffix}`,
    labelParams: { name: window.label || limitName },
  };
};

/** The i18n label of a snapshot window; unknown ids fall back to the upstream label or the id. */
export function quotaWindowLabel(
  provider: QuotaProviderKind,
  window: QuotaWindow
): QuotaWindowLabel {
  if (provider === 'claude' && CLAUDE_LABELED_IDS.has(window.id)) {
    return { labelKey: `claude_quota.${window.id}` };
  }
  if (provider === 'codex') {
    if (window.id === 'primary' || window.id === 'secondary') {
      const labelKey = CODEX_CLASS_LABELS[window.class];
      if (labelKey) return { labelKey };
    }
    const display = codexDisplayLabel(window);
    if (display) return display;
  }
  if (window.id === 'weekly' || window.id === 'monthly' || window.id === 'daily') {
    return { labelKey: `quota_management.ledger_window_${window.id}` };
  }
  return { label: window.label || window.id };
}

/**
 * Whether the window's reset returns rate-limited capacity. The xAI monthly
 * window is a spend cap rolling over, so the timeline and the recovery sort
 * leave it out, as they do for the live xAI state.
 */
export function isCapacityWindow(window: QuotaWindow): boolean {
  return window.class !== 'xai:monthly';
}
