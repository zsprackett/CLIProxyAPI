/**
 * Pure model for the Usage page: controls -> query, labels, summary numbers.
 * No React, no DOM, no clock (callers pass `today` or `now`).
 */

import {
  USAGE_FILTER_DIMENSIONS,
  USAGE_OTHER_GROUP,
  USAGE_UNITS,
  type UsageBucket,
  type UsageCell,
  type UsageDimension,
  type UsageFilter,
  type UsageQuery,
  type UsageUnit,
} from '@/services/api/usageHistory';

export const RANGE_PRESETS = ['24h', '7d', '30d', '90d', '365d', 'mtd', 'ytd', 'custom'] as const;
export type RangePreset = (typeof RANGE_PRESETS)[number];
export const USAGE_METRICS = ['cost', 'tokens', 'requests'] as const;
export type UsageMetric = (typeof USAGE_METRICS)[number];
/** Stack bars by the group dimension, or by token type (uncached, cache read, 5m/1h writes...). */
export const STACK_BYS = ['group', 'tokenType'] as const;
export type StackBy = (typeof STACK_BYS)[number];

/** Everything the user can set. Lives in UsagePage state. */
export interface UsageControls {
  preset: RangePreset;
  /** Only for preset 'custom'. YYYY-MM-DD. */
  from: string;
  to: string;
  bucket: UsageUnit;
  groupBy: UsageDimension;
  filter: UsageFilter;
  metric: UsageMetric;
  stackBy: StackBy;
}

export const DEFAULT_USAGE_CONTROLS: UsageControls = {
  preset: '30d',
  from: '',
  to: '',
  bucket: 'day',
  groupBy: 'model',
  filter: {},
  metric: 'cost',
  stackBy: 'group',
};

/** Server span limits, mirrored so the UI never sends a 400. Spans count both ends. */
export const MAX_SPAN_DAYS: Record<UsageUnit, number> = { hour: 31, day: 1100, month: 3660 };

const DAY_MS = 86_400_000;
const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

const civilMs = (date: string): number | null => {
  const match = DATE_RE.exec(date);
  if (!match) return null;
  const ms = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isFinite(ms) ? ms : null;
};

const civilDate = (ms: number): string => new Date(ms).toISOString().slice(0, 10);

const addDays = (date: string, days: number): string => {
  const ms = civilMs(date);
  return ms === null ? date : civilDate(ms + days * DAY_MS);
};

/** Days from `from` to `to`, both included. */
export function spanDays(from: string, to: string): number {
  const a = civilMs(from);
  const b = civilMs(to);
  if (a === null || b === null) return 1;
  return Math.round(Math.abs(b - a) / DAY_MS) + 1;
}

/** The civil range of a preset; `custom` returns the 30 day range (its seed). */
export function presetRange(preset: RangePreset, today: string): { from: string; to: string } {
  switch (preset) {
    case '24h':
      return { from: addDays(today, -1), to: today };
    case '7d':
      return { from: addDays(today, -6), to: today };
    case '90d':
      return { from: addDays(today, -89), to: today };
    case '365d':
      return { from: addDays(today, -364), to: today };
    case 'mtd':
      return { from: `${today.slice(0, 7)}-01`, to: today };
    case 'ytd':
      return { from: `${today.slice(0, 4)}-01-01`, to: today };
    default:
      return { from: addDays(today, -29), to: today };
  }
}

function controlsRange(controls: UsageControls, today: string): { from: string; to: string } {
  if (controls.preset !== 'custom') return presetRange(controls.preset, today);
  const from = civilMs(controls.from) === null ? '' : controls.from;
  const to = civilMs(controls.to) === null ? '' : controls.to;
  if (!from || !to) return presetRange('30d', today);
  return from <= to ? { from, to } : { from: to, to: from };
}

/** Which units the bucket select enables for a span (in days). */
export function allowedUnits(days: number): UsageUnit[] {
  return USAGE_UNITS.filter((unit) => days <= MAX_SPAN_DAYS[unit]);
}

/** Default unit when the preset changes: <=2d hour, <=92d day, else month. */
export function suggestBucket(days: number): UsageUnit {
  if (days <= 2) return 'hour';
  if (days <= 92) return 'day';
  return 'month';
}

/**
 * Controls -> wire query. `today` is YYYY-MM-DD in `tz`. Presets include
 * today: 7d = today and the 6 days before; 24h = yesterday and today. A bucket
 * the span does not allow is replaced by suggestBucket(span).
 */
export function controlsToQuery(controls: UsageControls, tz: string, today: string): UsageQuery {
  const { from, to } = controlsRange(controls, today);
  const days = spanDays(from, to);
  const filter: UsageFilter = {};
  for (const dimension of USAGE_FILTER_DIMENSIONS) {
    const value = controls.filter[dimension];
    if (value) filter[dimension] = value;
  }
  return {
    from,
    to,
    tz,
    bucket: allowedUnits(days).includes(controls.bucket) ? controls.bucket : suggestBucket(days),
    groupBy: controls.groupBy,
    filter,
  };
}

/** A preset change: suggests a unit, and seeds a custom range from the range on screen. */
export function applyPreset(
  controls: UsageControls,
  preset: RangePreset,
  today: string
): UsageControls {
  if (preset === 'custom') {
    const range = controlsRange(controls, today);
    return { ...controls, preset, ...range };
  }
  const range = presetRange(preset, today);
  return { ...controls, preset, bucket: suggestBucket(spanDays(range.from, range.to)) };
}

/** YYYY-MM-DD in `tz` at `nowMs`; an unknown zone reads as UTC. */
export function todayIn(tz: string, nowMs: number): string {
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: tz,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(nowMs);
    const part = (type: string) => parts.find((item) => item.type === type)?.value ?? '';
    return `${part('year')}-${part('month')}-${part('day')}`;
  } catch {
    return civilDate(nowMs);
  }
}

const RFC3339_RE =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::\d{2}(?:\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/;

/**
 * Bucket label from the server's local RFC3339 start, by string slicing only.
 * Day and month names come from Intl over the digits as a UTC civil date, so
 * no time zone is applied. A repeated hour (fall back) gets its offset.
 */
export function bucketLabel(
  start: string,
  unit: UsageUnit,
  locale: string,
  repeated: boolean
): string {
  const match = RFC3339_RE.exec(start);
  if (!match) return start;
  const [, year, month, day, hour, minute, offset] = match;
  if (unit === 'hour') {
    const label = `${month}-${day} ${hour}:${minute}`;
    return repeated ? `${label} (${offset})` : label;
  }
  const civil = Date.UTC(Number(year), Number(month) - 1, Number(day));
  const options: Intl.DateTimeFormatOptions =
    unit === 'month'
      ? { timeZone: 'UTC', year: 'numeric', month: 'short' }
      : { timeZone: 'UTC', month: 'short', day: 'numeric' };
  try {
    return new Intl.DateTimeFormat(locale, options).format(civil);
  } catch {
    return new Intl.DateTimeFormat('en', options).format(civil);
  }
}

/** Labels for every bucket; hour buckets whose wall time repeats carry their offset. */
export function bucketLabels(
  buckets: readonly UsageBucket[],
  unit: UsageUnit,
  locale: string
): string[] {
  const wall = (start: string) => start.slice(0, 16);
  const seen = new Map<string, number>();
  for (const bucket of buckets)
    seen.set(wall(bucket.start), (seen.get(wall(bucket.start)) ?? 0) + 1);
  return buckets.map((bucket) =>
    bucketLabel(
      bucket.start,
      unit,
      locale,
      unit === 'hour' && (seen.get(wall(bucket.start)) ?? 0) > 1
    )
  );
}

/**
 * Display label for a group key. Key hashes go through the client labels
 * (saved name > masked key > #hash) so a raw key never renders.
 */
export function groupLabel(
  dimension: UsageDimension,
  key: string,
  clientLabels: ReadonlyMap<string, string>,
  t: (key: string) => string
): string {
  if (key === USAGE_OTHER_GROUP) return t('usage.other_groups');
  const missing = !key || key === '-';
  switch (dimension) {
    case 'none':
      return t('usage.all');
    case 'key':
      return missing ? t('usage.no_key') : clientLabels.get(key) || `#${key}`;
    case 'auth':
      return missing ? t('usage.no_auth') : key;
    case 'agent':
      return missing ? t('usage.unknown_agent') : key;
    default:
      return missing ? t('usage.unknown') : key;
  }
}

export interface UsageSummary {
  costUsd: number | null;
  /** True when part of a priced total is unpriced: show "at least $X". */
  costPartial: boolean;
  totalTokens: number;
  requests: number;
  failureRate: number;
  /** cacheRead / (uncached + cacheRead + cacheWrite5m + cacheWrite1h). */
  cacheReadShare: number;
  /** cacheWrite1h / (cacheWrite5m + cacheWrite1h); 0 when no writes. */
  write1hShare: number;
  /** Dollars spent on cache writes (5m + 1h), null when unpriced. */
  cacheWriteCostUsd: number | null;
}

const share = (part: number, whole: number) => (whole > 0 ? part / whole : 0);

export function totalTokens(cell: UsageCell): number {
  const { tokens } = cell;
  return (
    tokens.uncached +
    tokens.cacheRead +
    tokens.cacheWrite5m +
    tokens.cacheWrite1h +
    tokens.output +
    tokens.reasoning +
    tokens.unclassified
  );
}

export function summarize(totals: UsageCell): UsageSummary {
  const { tokens } = totals;
  const prompt = tokens.uncached + tokens.cacheRead + tokens.cacheWrite5m + tokens.cacheWrite1h;
  return {
    costUsd: totals.costUsd,
    costPartial: totals.costUsd !== null && totals.unpricedTokens > 0,
    totalTokens: totalTokens(totals),
    requests: totals.requests,
    failureRate: share(totals.failures, totals.requests),
    cacheReadShare: share(tokens.cacheRead, prompt),
    write1hShare: share(tokens.cacheWrite1h, tokens.cacheWrite5m + tokens.cacheWrite1h),
    cacheWriteCostUsd: totals.costByType
      ? totals.costByType.cacheWrite5m + totals.costByType.cacheWrite1h
      : null,
  };
}

/** Value of `metric` for a cell (cost null -> 0 for stacking; the table shows "-"). */
export function metricValue(cell: UsageCell, metric: UsageMetric): number {
  if (metric === 'cost') return cell.costUsd ?? 0;
  if (metric === 'requests') return cell.requests;
  return totalTokens(cell);
}

/** Whether a range (a query, or a report's echoed query) covers today; drives the 60s refresh. */
export function coversToday(range: { from: string; to: string }, today: string): boolean {
  const { from, to } = range;
  return Boolean(from && to) && from <= today && today <= to;
}

export function formatUsd(value: number | null, locale: string): string {
  if (value === null) return '-';
  const small = value > 0 && value < 0.01;
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: small ? 4 : 2,
    maximumFractionDigits: small ? 4 : 2,
  }).format(value);
}

export function formatTokens(value: number, locale: string): string {
  return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(
    value
  );
}

export function formatPercent(value: number, locale: string): string {
  return new Intl.NumberFormat(locale, { style: 'percent', maximumFractionDigits: 1 }).format(
    value
  );
}
