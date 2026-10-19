/**
 * Ledger view model: one normalized row per credential, plus per-provider totals.
 *
 * Pure and React-free (`nowMs` is always passed in) so it is directly testable.
 *
 * Two sources feed a row:
 * - live: the provider state fetched on demand through the api-call proxy;
 * - observed: the passive quota snapshot the backend records from upstream
 *   response headers on ordinary traffic (`quota` / `model_quotas` on each
 *   auth-file entry), or from its optional background quota poll. It costs no
 *   upstream request here, so rows have data before anyone clicks refresh.
 *   Claude and Codex emit these headers; xAI billing signals (`x-xai-billing-*`)
 *   come only from the backend poll.
 *
 * Live data wins while it is the newer reading. When the backend records a
 * snapshot after the live fetch, the snapshot's windows replace the live ones
 * with the same id and the live-only windows (which the snapshot cannot carry)
 * are kept.
 */

import type { AuthFileItem } from '@/types';
import { DAY_MS } from '@/utils/time/durations';
import type { QuotaProviderType } from './providers/types';

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

const parseNumber = (value: unknown): number | null => {
  if (isFiniteNumber(value)) return value;
  if (typeof value !== 'string' || !value.trim()) return null;
  const parsed = Number(value.trim());
  return Number.isFinite(parsed) ? parsed : null;
};

/** Unix seconds (Claude/Codex headers) or an ISO timestamp, in epoch ms. */
const parseInstantMs = (value: unknown): number | null => {
  const numeric = parseNumber(value);
  if (numeric !== null) return numeric > 0 ? numeric * 1000 : null;
  if (typeof value !== 'string') return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
};

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

const fromUsedPercent = (window: UsedPercentWindowLike, index: number): LedgerWindow => ({
  id: window.id || `window-${index}`,
  label: window.label,
  labelKey: window.labelKey,
  labelParams: window.labelParams,
  remaining: isFiniteNumber(window.usedPercent) ? clampPercent(100 - window.usedPercent) : null,
  resetAtMs: isFiniteNumber(window.resetAtMs) ? window.resetAtMs : null,
  periodHours: isFiniteNumber(window.periodHours) ? window.periodHours : null,
});

/** Windows from a loaded provider state, or null when the state is not loaded. */
export function liveLedgerWindows(
  provider: QuotaProviderType,
  quota: unknown
): LedgerWindow[] | null {
  const state = toRecord(quota);
  if (!state || state.status !== 'success') return null;

  if (provider === 'claude' || provider === 'codex') {
    const windows = (state.windows as UsedPercentWindowLike[] | undefined) ?? [];
    return windows.map(fromUsedPercent);
  }

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
      remaining: isFiniteNumber(window.usedPercent) ? clampPercent(100 - window.usedPercent) : null,
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
        remaining: isFiniteNumber(usage) ? clampPercent(100 - usage) : null,
        resetAtMs: isFiniteNumber(billing.resetAtMs) ? billing.resetAtMs : null,
        periodHours: isFiniteNumber(billing.periodHours) ? billing.periodHours : null,
      },
    ];
  }

  return [];
}

/** Plan identifier from a loaded provider state, when it reports one. */
export function livePlanType(provider: QuotaProviderType, quota: unknown): string | null {
  const state = toRecord(quota);
  if (!state || state.status !== 'success') return null;
  if (provider === 'claude' || provider === 'codex') {
    return typeof state.planType === 'string' && state.planType ? state.planType : null;
  }
  return null;
}

/* ---------------------------------------------------------- observed signals */

interface SignalSnapshot {
  observedAtMs: number;
  /** Header names lower-cased so lookups do not depend on canonicalization. */
  signals: Map<string, string>;
}

const readSnapshot = (value: unknown): SignalSnapshot | null => {
  const record = toRecord(value);
  const signals = toRecord(record?.signals);
  if (!record || !signals) return null;
  const observedAtMs = Date.parse(String(record.observed_at ?? ''));
  if (!Number.isFinite(observedAtMs)) return null;
  const map = new Map<string, string>();
  for (const [key, raw] of Object.entries(signals)) {
    if (typeof raw === 'string') map.set(key.toLowerCase(), raw.trim());
  }
  return map.size > 0 ? { observedAtMs, signals: map } : null;
};

/** The credential-wide snapshot followed by every model-scoped one, newest first. */
const readSnapshots = (file: AuthFileItem): SignalSnapshot[] => {
  const snapshots: SignalSnapshot[] = [];
  const credential = readSnapshot(file.quota);
  if (credential) snapshots.push(credential);
  const models = toRecord(file.model_quotas);
  if (models) {
    for (const value of Object.values(models)) {
      const snapshot = readSnapshot(value);
      if (snapshot) snapshots.push(snapshot);
    }
  }
  return snapshots.sort((a, b) => b.observedAtMs - a.observedAtMs);
};

/**
 * A window that already reset after it was observed has fresh capacity and no
 * pending reset — showing the stale utilization would claim a drained credential.
 */
const settleWindow = (window: LedgerWindow, nowMs: number): LedgerWindow =>
  window.resetAtMs !== null && window.resetAtMs <= nowMs
    ? { ...window, remaining: 100, resetAtMs: null }
    : window;

const CLAUDE_SIGNAL_WINDOWS = [
  { prefix: 'five-hour', header: '5h', labelKey: 'claude_quota.five_hour', periodHours: 5 },
  {
    prefix: 'seven-day',
    header: '7d',
    labelKey: 'claude_quota.seven_day',
    periodHours: WEEK_HOURS,
  },
  // The backend treats the 7d_oi window as the Fable-scoped weekly limit.
  {
    prefix: 'seven-day-fable',
    header: '7d_oi',
    labelKey: 'claude_quota.seven_day_fable',
    periodHours: WEEK_HOURS,
  },
] as const;

function observedClaudeWindows(snapshots: SignalSnapshot[], nowMs: number): LedgerWindow[] {
  const windows: LedgerWindow[] = [];
  for (const spec of CLAUDE_SIGNAL_WINDOWS) {
    const utilizationKey = `anthropic-ratelimit-unified-${spec.header}-utilization`;
    const resetKey = `anthropic-ratelimit-unified-${spec.header}-reset`;
    // Each window comes from the newest snapshot that reported it; a model-scoped
    // snapshot is the only place a model-specific window can appear.
    const snapshot = snapshots.find(
      (candidate) => candidate.signals.has(utilizationKey) || candidate.signals.has(resetKey)
    );
    if (!snapshot) continue;
    const utilization = parseNumber(snapshot.signals.get(utilizationKey));
    windows.push(
      settleWindow(
        {
          id: spec.prefix,
          labelKey: spec.labelKey,
          // Utilization is a 0..1 fraction of the window consumed.
          remaining: utilization === null ? null : clampPercent(100 - utilization * 100),
          resetAtMs: parseInstantMs(snapshot.signals.get(resetKey)),
          periodHours: spec.periodHours,
        },
        nowMs
      )
    );
  }
  return windows;
}

const codexWindowIdentity = (minutes: number | null) => {
  if (minutes !== null && minutes > WEEK_HOURS * 60) {
    return { id: 'monthly', labelKey: 'codex_quota.team_secondary_window' };
  }
  if (minutes !== null && minutes >= WEEK_HOURS * 60) {
    return { id: 'weekly', labelKey: 'codex_quota.secondary_window' };
  }
  return { id: 'five-hour', labelKey: 'codex_quota.primary_window' };
};

function observedCodexWindows(snapshot: SignalSnapshot, nowMs: number): LedgerWindow[] {
  const windows: LedgerWindow[] = [];
  for (const slot of ['primary', 'secondary'] as const) {
    const read = (suffix: string) => snapshot.signals.get(`x-codex-${slot}-${suffix}`);
    const used = parseNumber(read('used-percent'));
    const minutes = parseNumber(read('window-minutes'));
    let resetAtMs = parseInstantMs(read('reset-at'));
    if (resetAtMs === null) {
      const afterSeconds = parseNumber(read('reset-after-seconds'));
      if (afterSeconds !== null && afterSeconds >= 0) {
        resetAtMs = snapshot.observedAtMs + afterSeconds * 1000;
      }
    }
    if (used === null && resetAtMs === null) continue;
    windows.push(
      settleWindow(
        {
          ...codexWindowIdentity(minutes),
          remaining: used === null ? null : clampPercent(100 - used),
          resetAtMs,
          periodHours: minutes === null ? null : minutes / 60,
        },
        nowMs
      )
    );
  }
  return windows;
}

/** The single billing window the backend poll derives from the xAI billing endpoints. */
function observedXaiWindows(snapshot: SignalSnapshot, nowMs: number): LedgerWindow[] {
  const read = (suffix: string) => snapshot.signals.get(`x-xai-billing-${suffix}`);
  const periodType = read('period-type') === 'weekly' ? 'weekly' : 'monthly';
  const used = parseNumber(read('used-percent'));
  const resetAtMs = parseInstantMs(read('reset-at'));
  const minutes = parseNumber(read('window-minutes'));
  if (used === null && resetAtMs === null) return [];
  return [
    settleWindow(
      {
        id: `xai-${periodType}`,
        labelKey: `quota_management.ledger_window_${periodType}`,
        remaining: used === null ? null : clampPercent(100 - used),
        resetAtMs,
        periodHours: minutes === null ? null : minutes / 60,
      },
      nowMs
    ),
  ];
}

/** Observed quota for a credential, or null when the backend recorded none. */
export function observedLedgerData(
  provider: QuotaProviderType,
  file: AuthFileItem,
  nowMs: number
): LedgerRowData | null {
  if (provider !== 'claude' && provider !== 'codex' && provider !== 'xai') return null;
  const snapshots = readSnapshots(file);
  if (snapshots.length === 0) return null;

  if (provider === 'xai') {
    const snapshot = snapshots.find((candidate) =>
      [...candidate.signals.keys()].some((key) => key.startsWith('x-xai-billing-'))
    );
    if (!snapshot) return null;
    const windows = observedXaiWindows(snapshot, nowMs);
    if (windows.length === 0) return null;
    return { source: 'observed', windows, planType: null, observedAtMs: snapshot.observedAtMs };
  }

  if (provider === 'claude') {
    const windows = observedClaudeWindows(snapshots, nowMs);
    if (windows.length === 0) return null;
    return { source: 'observed', windows, planType: null, observedAtMs: snapshots[0].observedAtMs };
  }

  // Codex reports the account windows on every response, so the newest snapshot
  // that carries them is complete on its own.
  const snapshot = snapshots.find((candidate) =>
    [...candidate.signals.keys()].some((key) => /^x-codex-(primary|secondary)-/.test(key))
  );
  if (!snapshot) return null;
  const windows = observedCodexWindows(snapshot, nowMs);
  if (windows.length === 0) return null;
  const plan = snapshot.signals.get('x-codex-plan-type');
  return {
    source: 'observed',
    windows,
    planType: plan ? plan.toLowerCase() : null,
    observedAtMs: snapshot.observedAtMs,
  };
}

/**
 * Overlays a newer observed snapshot on an older live reading: observed windows
 * replace live windows with the same id in place, live-only windows are kept,
 * and observed-only windows are appended.
 */
function overlayObserved(live: LedgerRowData, observed: LedgerRowData): LedgerRowData {
  const observedById = new Map(observed.windows.map((window) => [window.id, window]));
  const windows = live.windows.map((window) => {
    const replacement = observedById.get(window.id);
    if (!replacement) return window;
    observedById.delete(window.id);
    return replacement;
  });
  return {
    source: 'observed',
    windows: [...windows, ...observedById.values()],
    planType: live.planType ?? observed.planType,
    observedAtMs: observed.observedAtMs,
  };
}

/**
 * The newer of the live reading and the observed snapshot (see the module
 * comment), whichever exists, otherwise nothing.
 */
export function resolveLedgerRow(
  provider: QuotaProviderType,
  file: AuthFileItem,
  quota: unknown,
  nowMs: number
): LedgerRowData {
  const observed = observedLedgerData(provider, file, nowMs);
  const live = liveLedgerWindows(provider, quota);
  if (live !== null) {
    const liveRow: LedgerRowData = {
      source: 'live',
      windows: live.map((window) => settleWindow(window, nowMs)),
      planType: livePlanType(provider, quota),
      observedAtMs: null,
    };
    const fetchedAtMs = toRecord(quota)?.fetchedAtMs;
    if (
      observed?.observedAtMs != null &&
      isFiniteNumber(fetchedAtMs) &&
      observed.observedAtMs > fetchedAtMs
    ) {
      return overlayObserved(liveRow, observed);
    }
    return liveRow;
  }
  return (
    observed ?? {
      source: 'none',
      windows: [],
      planType: null,
      observedAtMs: null,
    }
  );
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

const PREFERRED_WEEKLY_IDS = ['seven-day', 'weekly'];

/**
 * The account-wide weekly window — the one the soonest-reset router orders by.
 * Model-scoped weekly windows are only used when no account-wide one exists.
 */
export function weeklyWindow(windows: readonly LedgerWindow[]): LedgerWindow | null {
  const preferred = windows.find((window) => PREFERRED_WEEKLY_IDS.includes(window.id));
  if (preferred) return preferred;
  return (
    windows.find(
      (window) =>
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
