/**
 * Ledger view model: one normalized row per credential, plus per-provider totals.
 *
 * Pure and React-free (`nowMs` is always passed in) so it is directly testable.
 *
 * Two sources feed a row:
 * - live: the provider state fetched on demand through the api-call proxy
 *   (antigravity, kimi, meta, and until P3 xAI and Devin);
 * - observed: the typed `quota_snapshot` the backend keeps per credential for
 *   Claude, Codex, xAI, and Devin, from its quota poll and from response
 *   headers. The backend already merged its windows. A Claude or Codex click
 *   polls the backend and its result stands in for the listing snapshot.
 *
 * A row shows one whole reading, never a merge: `newerQuotaReading` picks the
 * live state or the snapshot.
 */

import type { AuthFileItem } from '@/types';
import { DAY_MS } from '@/utils/time/durations';
import type { QuotaProviderType } from './providers/types';
import type { QuotaProviderKind, QuotaSnapshot, QuotaWindow } from './snapshot/types';
import {
  newerQuotaReading,
  type KnownQuotaSnapshot,
  quotaWindowLabel,
  readQuotaSnapshot,
  remainingFromUsed,
  snapshotWindowRemaining,
  windowStaleSinceMs,
} from './snapshot/windows';

export interface LedgerWindow {
  id: string;
  /** Already-translated label (live provider windows carry one). */
  label?: string;
  labelKey?: string;
  labelParams?: Record<string, string | number>;
  /** Remaining percent, 0..100; null when unknown. */
  remaining: number | null;
  /** Upcoming reset instant; null when no reset is pending. */
  resetAtMs: number | null;
  periodHours: number | null;
  /** Backend window class (e.g. `codex:weekly`); only snapshot windows carry one. */
  class?: string;
  /**
   * Upstream reported the window exhausted and `remaining` is 0. The backend
   * copies `limit_reached` onto every slot of a Codex namespace, so a window
   * with a known percent above 0 is not exhausted.
   */
  limitReached?: boolean;
  /** The reset passed while the percent was unknown; no reading came after it. */
  awaitingData?: boolean;
  /** When the data went stale because the sub-request that owns it failed. */
  staleSinceMs?: number | null;
}

export type LedgerSource = 'live' | 'observed' | 'none';

export interface LedgerRowData {
  source: LedgerSource;
  windows: LedgerWindow[];
  /** Raw plan identifier (e.g. `plan_max`, `pro`), when known. */
  planType: string | null;
  /** When an observed snapshot was captured. */
  observedAtMs: number | null;
}

const WEEK_HOURS = 24 * 7;

const clampPercent = (value: number) => Math.min(100, Math.max(0, value));

const isFiniteNumber = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value);

const toRecord = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

/* ------------------------------------------------------- live provider state */

interface UsedPercentWindowLike {
  id?: string;
  label?: string;
  labelKey?: string;
  labelParams?: Record<string, string | number>;
  usedPercent?: number | null;
  resetAtMs?: number | null;
  periodHours?: number | null;
}

/** Windows from a loaded provider state, or null when the state is not loaded. */
export function liveLedgerWindows(
  provider: QuotaProviderType,
  quota: unknown
): LedgerWindow[] | null {
  const state = toRecord(quota);
  if (!state || state.status !== 'success') return null;

  if (provider === 'devin') {
    const windows =
      (state.windows as
        | {
            id: string;
            label?: string;
            remainingPercent: number | null;
            resetAtMs: number | null;
            periodHours: number;
          }[]
        | undefined) ?? [];
    return windows.map((window) => ({
      id: window.id,
      label: window.label,
      labelKey: window.label ? undefined : `quota_management.ledger_window_${window.id}`,
      remaining: isFiniteNumber(window.remainingPercent)
        ? clampPercent(window.remainingPercent)
        : null,
      resetAtMs: isFiniteNumber(window.resetAtMs) ? window.resetAtMs : null,
      periodHours: window.periodHours,
    }));
  }

  if (provider === 'kimi') {
    const rows =
      (state.rows as (UsedPercentWindowLike & { used: number; limit: number })[] | undefined) ?? [];
    return rows.map((row, index) => ({
      id: row.id || `row-${index}`,
      label: row.label,
      labelKey: row.labelKey,
      labelParams: row.labelParams,
      remaining:
        row.limit > 0 ? clampPercent(Math.round(((row.limit - row.used) / row.limit) * 100)) : null,
      resetAtMs: isFiniteNumber(row.resetAtMs) ? row.resetAtMs : null,
      periodHours: isFiniteNumber(row.periodHours) ? row.periodHours : null,
    }));
  }

  if (provider === 'antigravity') {
    const groups =
      (state.groups as
        | {
            buckets?: {
              id: string;
              label?: string;
              remainingFraction?: number;
              resetAtMs?: number | null;
              periodHours?: number | null;
            }[];
          }[]
        | undefined) ?? [];
    return groups
      .flatMap((group) => group.buckets ?? [])
      .map((bucket, index) => ({
        id: bucket.id || `bucket-${index}`,
        label: bucket.label,
        remaining: isFiniteNumber(bucket.remainingFraction)
          ? clampPercent(Math.round(bucket.remainingFraction * 100))
          : null,
        resetAtMs: isFiniteNumber(bucket.resetAtMs) ? bucket.resetAtMs : null,
        periodHours: isFiniteNumber(bucket.periodHours) ? bucket.periodHours : null,
      }));
  }

  if (provider === 'meta') {
    const data = toRecord(state.data);
    const windows =
      (data?.windows as
        { id: 'window' | 'weekly'; usedPercent: number | null; resetAt?: number }[] | undefined) ??
      [];
    return windows.map((window) => ({
      id: window.id,
      labelKey: `meta_quota.${window.id}`,
      remaining: remainingFromUsed(window.usedPercent),
      resetAtMs: isFiniteNumber(window.resetAt) ? window.resetAt * 1000 : null,
      periodHours: window.id === 'weekly' ? WEEK_HOURS : null,
    }));
  }

  if (provider === 'xai') {
    const billing = toRecord(state.billing);
    if (!billing) return [];
    const periodType = billing.periodType === 'weekly' ? 'weekly' : 'monthly';
    const usage = billing.usagePercent ?? billing.usedPercent;
    return [
      {
        id: `xai-${periodType}`,
        labelKey: `quota_management.ledger_window_${periodType}`,
        remaining: remainingFromUsed(usage),
        resetAtMs: isFiniteNumber(billing.resetAtMs) ? billing.resetAtMs : null,
        periodHours: isFiniteNumber(billing.periodHours) ? billing.periodHours : null,
      },
    ];
  }

  return [];
}

/* ----------------------------------------------------------- quota snapshot */

/**
 * A window that already reset after it was observed has fresh capacity and no
 * pending reset — showing the stale utilization would claim a drained credential.
 * Without a known percent there is nothing to settle, so it awaits new data.
 */
const settleWindow = (
  window: LedgerWindow,
  nowMs: number,
  percentKnown = window.remaining !== null
): LedgerWindow => {
  if (window.resetAtMs === null || window.resetAtMs > nowMs) return window;
  return percentKnown
    ? { ...window, remaining: 100, resetAtMs: null, limitReached: false }
    : { ...window, remaining: null, resetAtMs: null, limitReached: false, awaitingData: true };
};

const parseIsoMs = (value: string | null | undefined): number | null => {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
};

/** Ledger windows for some of a snapshot's windows, in the order given. */
export function snapshotLedgerWindows(
  provider: QuotaProviderKind,
  snapshot: QuotaSnapshot,
  windows: readonly QuotaWindow[],
  nowMs: number
): LedgerWindow[] {
  return windows.map((window) => {
    const remaining = snapshotWindowRemaining(window);
    const limitReached = window.limit_reached === true && remaining === 0;
    return settleWindow(
      {
        id: window.id,
        ...quotaWindowLabel(provider, window),
        remaining,
        resetAtMs: parseIsoMs(window.reset_at),
        periodHours: isFiniteNumber(window.period_seconds) ? window.period_seconds / 3600 : null,
        class: window.class,
        limitReached,
        staleSinceMs: windowStaleSinceMs(snapshot, window),
      },
      nowMs,
      remainingFromUsed(window.used_percent) !== null
    );
  });
}

const snapshotPlanType = (snapshot: QuotaSnapshot): string | null => {
  const extras = snapshot.extras;
  if (extras?.kind !== 'claude' && extras?.kind !== 'codex') return null;
  const plan = extras.plan?.value;
  if (typeof plan !== 'string' || !plan) return null;
  return extras.kind === 'codex' ? plan.toLowerCase() : plan;
};

const snapshotRow = (snapshot: KnownQuotaSnapshot, nowMs: number): LedgerRowData => {
  let observedAtMs: number | null = null;
  for (const window of snapshot.windows) {
    const at = parseIsoMs(window.observed_at);
    if (at !== null && (observedAtMs === null || at > observedAtMs)) observedAtMs = at;
  }
  return {
    source: 'observed',
    windows: snapshotLedgerWindows(snapshot.provider, snapshot, snapshot.windows, nowMs),
    planType: snapshotPlanType(snapshot),
    observedAtMs: observedAtMs ?? parseIsoMs(snapshot.updated_at),
  };
};

/** Quota the backend recorded for a credential (`quota_snapshot`), or null when it has none. */
export function observedLedgerData(
  provider: QuotaProviderType,
  file: AuthFileItem,
  nowMs: number
): LedgerRowData | null {
  const snapshot = readQuotaSnapshot(provider, file.quota_snapshot);
  return snapshot ? snapshotRow(snapshot, nowMs) : null;
}

/** The newer whole reading of the live state and the observed snapshot, otherwise nothing. */
export function resolveLedgerRow(
  provider: QuotaProviderType,
  file: AuthFileItem,
  quota: unknown,
  nowMs: number
): LedgerRowData {
  const snapshot = readQuotaSnapshot(provider, file.quota_snapshot);
  const reading = newerQuotaReading(quota, snapshot);
  if (reading === 'snapshot' && snapshot) return snapshotRow(snapshot, nowMs);
  if (reading === 'live') {
    return {
      source: 'live',
      windows: (liveLedgerWindows(provider, quota) ?? []).map((window) =>
        settleWindow(window, nowMs)
      ),
      planType: null,
      observedAtMs: null,
    };
  }
  return { source: 'none', windows: [], planType: null, observedAtMs: null };
}

/* ------------------------------------------------------------ derived figures */

/**
 * What the credential can serve right now: its tightest window. A full weekly
 * window does not help while the 5-hour window is drained.
 */
export function effectiveRemaining(windows: readonly LedgerWindow[]): number | null {
  let tightest: number | null = null;
  for (const window of windows) {
    if (window.remaining === null) continue;
    if (tightest === null || window.remaining < tightest) tightest = window.remaining;
  }
  return tightest;
}

const PREFERRED_WEEKLY_IDS = ['seven-day', 'seven_day', 'weekly'];
const ACCOUNT_WEEKLY_CLASSES = ['claude:7d', 'codex:weekly', 'xai:weekly'];

/**
 * The account-wide weekly window — the one the soonest-reset router orders by.
 * Model-scoped weekly windows are only used when no account-wide one exists.
 */
export function weeklyWindow(windows: readonly LedgerWindow[]): LedgerWindow | null {
  const preferred = windows.find(
    (window) =>
      PREFERRED_WEEKLY_IDS.includes(window.id) ||
      (window.class !== undefined && ACCOUNT_WEEKLY_CLASSES.includes(window.class))
  );
  if (preferred) return preferred;
  // Display-only snapshot windows (class "") never stand in for the account weekly.
  return (
    windows.find(
      (window) =>
        window.class !== '' &&
        window.periodHours !== null &&
        window.periodHours >= WEEK_HOURS - 24 &&
        window.periodHours <= WEEK_HOURS + 24
    ) ?? null
  );
}

export interface LedgerCredential {
  /** Stable identity (quota cache key). */
  key: string;
  /** Filename; the backend auth ID for file-backed credentials. */
  name: string;
  provider: QuotaProviderType;
  disabled: boolean;
  /** True while the backend has the whole credential cooling down. */
  coolingDown: boolean;
  row: LedgerRowData;
}

/** Window after which a weekly reset counts as "about to expire". */
export const EXPIRING_SOON_MS = DAY_MS;

export interface ProviderSummary {
  provider: QuotaProviderType;
  credentials: number;
  /** Credentials whose tightest window still has capacity. */
  usable: number;
  /** Credentials with no quota data to judge. */
  unknown: number;
  /** One entry per credential, in input order: its tightest remaining percent. */
  segments: (number | null)[];
  soonestWeeklyResetMs: number | null;
  /**
   * Weekly quota that will expire unused at a reset inside EXPIRING_SOON_MS,
   * in credential-equivalents (1 = one credential's full weekly window).
   */
  expiringSoon: number;
}

export function summarizeProvider(
  provider: QuotaProviderType,
  credentials: readonly LedgerCredential[],
  nowMs: number
): ProviderSummary {
  const summary: ProviderSummary = {
    provider,
    credentials: credentials.length,
    usable: 0,
    unknown: 0,
    segments: [],
    soonestWeeklyResetMs: null,
    expiringSoon: 0,
  };
  for (const credential of credentials) {
    const tightest = effectiveRemaining(credential.row.windows);
    summary.segments.push(tightest);
    if (tightest === null) summary.unknown += 1;
    else if (tightest > 0 && !credential.disabled && !credential.coolingDown) summary.usable += 1;

    const weekly = weeklyWindow(credential.row.windows);
    if (!weekly || weekly.resetAtMs === null || weekly.resetAtMs <= nowMs) continue;
    if (summary.soonestWeeklyResetMs === null || weekly.resetAtMs < summary.soonestWeeklyResetMs) {
      summary.soonestWeeklyResetMs = weekly.resetAtMs;
    }
    if (weekly.resetAtMs - nowMs <= EXPIRING_SOON_MS && weekly.remaining !== null) {
      summary.expiringSoon += weekly.remaining / 100;
    }
  }
  return summary;
}

/**
 * The credential the soonest-reset strategy would most likely pick next.
 *
 * Mirrors the backend selector: unavailable credentials are skipped, a
 * credential with no known future weekly reset is tried first (one request
 * teaches the router its window), then the soonest reset wins, ties by name.
 * It is an estimate — the router also weighs priority and per-model state.
 */
export function predictSoonestResetPick(
  credentials: readonly LedgerCredential[],
  nowMs: number
): string | null {
  let best: { key: string; name: string; resetMs: number } | null = null;
  for (const credential of credentials) {
    if (credential.disabled || credential.coolingDown) continue;
    if (credential.provider !== 'claude' && credential.provider !== 'codex') continue;
    if (effectiveRemaining(credential.row.windows) === 0) continue;
    const weekly = weeklyWindow(credential.row.windows);
    const resetMs =
      weekly && weekly.resetAtMs !== null && weekly.resetAtMs > nowMs ? weekly.resetAtMs : 0;
    if (
      best === null ||
      resetMs < best.resetMs ||
      (resetMs === best.resetMs && credential.name < best.name)
    ) {
      best = { key: credential.key, name: credential.name, resetMs };
    }
  }
  return best?.key ?? null;
}

/** Whether the credential has an active credential-scoped cooldown. */
export function isCredentialCoolingDown(file: AuthFileItem, nowMs: number): boolean {
  const snapshot = file.cooldownSnapshot;
  if (!snapshot?.records) return false;
  return snapshot.records.some(
    (record) =>
      record.scope === 'credential' &&
      snapshot.receivedAtMs + record.remainingSeconds * 1000 > nowMs
  );
}

/* ------------------------------------------------------------------ privacy */

const MASK = '•••';

/**
 * Hide the identifying parts of an email embedded in a filename or label:
 * `claude-tom@example.dev.json` → `claude-t•••@e•••.dev.json`. The provider
 * prefix, the first character of each part, the TLD, and the extension stay
 * readable so rows remain distinguishable.
 */
export function maskIdentity(value: string): string {
  return value.replace(/([^\s@/]+)@([^\s@/]+)/g, (_match, local: string, domain: string) => {
    const dash = local.indexOf('-');
    const localHead = dash >= 0 ? local.slice(0, dash + 2) : local.slice(0, 1);
    const extensionMatch = /\.json$/i.exec(domain);
    const extension = extensionMatch ? extensionMatch[0] : '';
    const host = extension ? domain.slice(0, -extension.length) : domain;
    const labels = host.split('.');
    const tld = labels.length > 1 ? `.${labels[labels.length - 1]}` : '';
    return `${localHead}${MASK}@${host.slice(0, 1)}${MASK}${tld}${extension}`;
  });
}
