/**
 * CSV export, built client-side from the report already on screen (no second
 * request, so the file always matches the chart). Saved with utils/download.ts
 * downloadBlob. Pure string building; tested with fixtures.
 */

import type { UsageDimension, UsageQuery, UsageReport } from '@/services/api/usageHistory';

/**
 * One row per (bucket, group) with traffic, in bucket order then report.groups
 * order. Columns (header row, snake_case, stable for spreadsheets):
 *
 *   bucket_start, bucket_end,           server-local RFC3339 verbatim
 *   <dimension>, <dimension>_label,     e.g. key = a1b2c3d4e5f6, key_label = "laptop" (never a raw key)
 *                                       omitted entirely for groupBy 'none'
 *   requests, failures, avg_latency_ms,
 *   uncached_tokens, cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
 *   output_tokens, reasoning_tokens, unclassified_tokens, total_tokens,
 *   cost_usd,                           empty when null; 6 decimals
 *   unpriced_tokens
 *
 * No totals row (spreadsheets sum; a totals row breaks pivot tables).
 * Escaping: RFC 4180 (quote when , " CR LF present; double inner quotes). Formula
 * injection guard: a cell starting with = + - @ TAB CR gets a leading ' (labels are
 * user-entered key names). Line ending CRLF; UTF-8 with BOM so Excel reads the zh/ru labels.
 */
export function buildUsageCsv(
  report: UsageReport,
  opts: { dimension: UsageDimension; label: (key: string) => string }
): string {
  throw new Error('not implemented');
}

/** usage_<from>_<to>_<bucket>_<groupBy>[_<filter values>].csv, filename-safe. */
export function usageCsvFilename(query: UsageReport['query'], filter: UsageQuery['filter']): string {
  throw new Error('not implemented');
}

export function escapeCsvCell(value: string): string {
  throw new Error('not implemented');
}
