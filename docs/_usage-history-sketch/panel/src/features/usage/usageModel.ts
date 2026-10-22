/**
 * Pure model for the Usage page: controls -> query, labels, summary numbers.
 * No React, no DOM, no clock (callers pass `today`).
 */

import type {
  UsageCell,
  UsageDimension,
  UsageFilter,
  UsageQuery,
  UsageReport,
  UsageUnit,
} from '@/services/api/usageHistory';

export type RangePreset = '24h' | '7d' | '30d' | '90d' | '365d' | 'mtd' | 'ytd' | 'custom';
export type UsageMetric = 'cost' | 'tokens' | 'requests';
/** Stack bars by the group dimension, or by token type (uncached, cache read, 5m/1h writes...). */
export type StackBy = 'group' | 'tokenType';

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

/** Server span limits, mirrored so the UI never sends a 400. */
export const MAX_SPAN_DAYS: Record<UsageUnit, number> = { hour: 31, day: 1100, month: 3660 };

/**
 * Controls -> wire query. `today` is YYYY-MM-DD in `tz` (computed by the caller
 * with Intl). Presets are inclusive of today: 7d = today and the 6 days before;
 * 24h = yesterday and today with hour buckets. If the chosen bucket exceeds
 * MAX_SPAN_DAYS for the span, it is replaced by suggestBucket(span).
 */
export function controlsToQuery(controls: UsageControls, tz: string, today: string): UsageQuery {
  throw new Error('not implemented');
}

/** Which units the bucket select enables for a span (in days). */
export function allowedUnits(spanDays: number): UsageUnit[] {
  throw new Error('not implemented');
}

/** Default unit when the preset changes: <=2d hour, <=92d day, else month. */
export function suggestBucket(spanDays: number): UsageUnit {
  throw new Error('not implemented');
}

/**
 * Bucket label from the server's local RFC3339 start, by string slicing only
 * (no Date parsing, no tz math):
 *   day   "2026-10-22T00:00:00-04:00" -> "Oct 22" (locale month name chosen from the digits)
 *   month "2026-10-01T00:00:00-04:00" -> "Oct 2026"
 *   hour  "2026-11-01T01:00:00-05:00" -> "11-01 01:00"; when the same wall time occurs twice
 *         in the report (fall back), both get their offset appended: " (-04:00)", " (-05:00)".
 */
export function bucketLabel(start: string, unit: UsageUnit, locale: string, repeated: boolean): string {
  throw new Error('not implemented');
}

/**
 * Display label for a group key.
 *   key   -> clientLabels (buildClientLabels: saved name > masked key > #hash); '-' -> t('usage.no_key')
 *   auth  -> the id as-is ("claude-max-1.json"); '-' -> t('usage.no_auth')
 *   agent -> as-is; '-' -> t('usage.unknown_agent')
 *   none  -> t('usage.all')
 *   model, provider -> as-is
 */
export function groupLabel(
  dimension: UsageDimension,
  key: string,
  clientLabels: Map<string, string>,
  t: (key: string) => string
): string {
  throw new Error('not implemented');
}

export interface UsageSummary {
  costUsd: number | null;
  /** True when some tokens in range are unpriced: show "at least $X". */
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

export function summarize(totals: UsageCell): UsageSummary {
  throw new Error('not implemented');
}

export function totalTokens(cell: UsageCell): number {
  throw new Error('not implemented');
}

/** Value of `metric` for a cell (cost null -> 0 for stacking; the table shows "-"). */
export function metricValue(cell: UsageCell, metric: UsageMetric): number {
  throw new Error('not implemented');
}

/** Whether the report covers today (drives the 60s background refresh). */
export function coversToday(report: UsageReport, today: string): boolean {
  throw new Error('not implemented');
}

export function formatUsd(value: number | null, locale: string): string {
  throw new Error('not implemented');
}

export function formatTokens(value: number, locale: string): string {
  throw new Error('not implemented');
}
