/**
 * Readers of `quota_snapshot` shared by the ledger, the timeline, the reset
 * schedule, and the auth-file card, so they agree on labels, percents, and
 * staleness.
 */

import type { AuthFileItem } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
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

/** Remaining percent, 0..100, from a percent used; null when the percent is unknown. */
export function remainingFromUsed(used: unknown): number | null {
  return typeof used === 'number' && Number.isFinite(used)
    ? Math.min(100, Math.max(0, 100 - used))
    : null;
}

/** A known percent wins; `limit_reached` only fills an unknown percent, as 0 remaining. */
export function snapshotWindowRemaining(window: QuotaWindow): number | null {
  return remainingFromUsed(window.used_percent) ?? (window.limit_reached === true ? 0 : null);
}

export type QuotaReadingSource = 'live' | 'snapshot' | 'none';

/**
 * Which whole reading to show: the live state in the store (from a click or a
 * poll) or the backend snapshot. The UI never merges the two. A loaded live
 * state wins unless the snapshot's `updated_at` is after its fetch time.
 */
export function newerQuotaReading(
  quota: unknown,
  snapshot: QuotaSnapshot | null | undefined
): QuotaReadingSource {
  const live = isRecord(quota) && quota.status === 'success' ? quota : null;
  if (!snapshot) return live ? 'live' : 'none';
  if (!live) return 'snapshot';
  const updatedAtMs = Date.parse(snapshot.updated_at ?? '');
  const fetchedAtMs = live.fetchedAtMs;
  return typeof fetchedAtMs === 'number' && updatedAtMs > fetchedAtMs ? 'snapshot' : 'live';
}

const updatedAtMs = (value: unknown): number =>
  isRecord(value) && typeof value.updated_at === 'string' ? Date.parse(value.updated_at) : NaN;

/** The listing caught up with a poll result once its snapshot is at least as new. */
export function listingCaughtUp(polled: QuotaSnapshot, listed: unknown): boolean {
  const listedMs = updatedAtMs(listed);
  if (!Number.isFinite(listedMs)) return false;
  const polledMs = updatedAtMs(polled);
  return !Number.isFinite(polledMs) || listedMs >= polledMs;
}

/** Whether the listing snapshot was updated after `atMs`; false when it has no snapshot. */
export function listingUpdatedAfter(listed: unknown, atMs: number): boolean {
  return updatedAtMs(listed) > atMs;
}

/** The credential's auth index as the listing and the poll endpoint name it. */
export const fileAuthIndex = (file: AuthFileItem): string | null =>
  normalizeAuthIndex(file.auth_index ?? file.authIndex);

/** An on-demand poll result and the auth file it was polled for. */
export interface PolledQuotaSnapshot {
  name: string;
  snapshot: QuotaSnapshot;
}

/**
 * The listing entry with its on-demand poll result in place of `quota_snapshot`,
 * until the listing carries a snapshot at least as new. The same object
 * otherwise.
 */
export function applyPolledSnapshot(
  file: AuthFileItem,
  polled: Readonly<Record<string, PolledQuotaSnapshot>>
): AuthFileItem {
  const authIndex = fileAuthIndex(file);
  const snapshot = authIndex ? polled[authIndex]?.snapshot : undefined;
  if (!snapshot || listingCaughtUp(snapshot, file.quota_snapshot)) return file;
  return { ...file, quota_snapshot: snapshot };
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
  const observedAtMs = Date.parse(window.observed_at ?? '');
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

const CODEX_CLASS_SUFFIXES: Record<string, string> = {
  'codex:5h': 'primary_window',
  'codex:weekly': 'secondary_window',
  'codex:monthly': 'team_secondary_window',
};

/** Display-only windows (class `""`) name a period by exact lookup, never by threshold. */
const PERIOD_SECONDS_SUFFIXES: Record<number, string> = {
  18000: 'primary_window',
  604800: 'secondary_window',
};

/**
 * The period label of a Codex window: its class, else its period by lookup,
 * else its formatted period; null when the period is unknown.
 */
const codexPeriodLabel = (
  window: QuotaWindow,
  prefix: string,
  params?: Record<string, string | number>
): QuotaWindowLabel | null => {
  const period = window.period_seconds;
  const suffix = window.class
    ? CODEX_CLASS_SUFFIXES[window.class]
    : typeof period === 'number'
      ? PERIOD_SECONDS_SUFFIXES[period]
      : undefined;
  if (suffix) {
    return params
      ? { labelKey: `codex_quota.${prefix}${suffix}`, labelParams: params }
      : { labelKey: `codex_quota.${prefix}${suffix}` };
  }
  if (typeof period === 'number' && Number.isFinite(period) && period > 0) {
    return { labelKey: `codex_quota.${prefix}period_window`, labelParams: { ...params, period } };
  }
  return null;
};

const codexDisplayLabel = (window: QuotaWindow): QuotaWindowLabel | null => {
  const match = /^(code_review|additional\.(.+))\.(primary|secondary)$/.exec(window.id);
  if (!match) return null;
  const [, group, limitName] = match;
  if (group === 'code_review') {
    return (
      codexPeriodLabel(window, 'code_review_') ?? { labelKey: 'codex_quota.code_review_window' }
    );
  }
  const params = { name: window.label || limitName };
  return (
    codexPeriodLabel(window, 'additional_', params) ?? {
      labelKey: 'codex_quota.additional_window',
      labelParams: params,
    }
  );
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
      return (
        codexPeriodLabel(window, '') ??
        (window.label ? { label: window.label } : { labelKey: 'codex_quota.usage_window' })
      );
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
