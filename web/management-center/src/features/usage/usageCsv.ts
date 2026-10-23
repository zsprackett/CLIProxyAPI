/**
 * CSV export, built client-side from the report already on screen (no second
 * request, so the file always matches the chart). Saved with utils/download.ts
 * downloadBlob. Pure string building.
 *
 * One row per (bucket, group) with traffic, in bucket order then report.groups
 * order. No totals row: spreadsheets sum, and a totals row breaks pivot tables.
 * Key groups export the key hash and its display label, never a raw key.
 */

import {
  USAGE_FILTER_DIMENSIONS,
  type UsageCell,
  type UsageDimension,
  type UsageGroupCell,
  type UsageQuery,
  type UsageReport,
} from '@/services/api/usageHistory';
import { totalTokens } from './usageModel';

const METRIC_COLUMNS = [
  'requests',
  'failures',
  'avg_latency_ms',
  'uncached_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'output_tokens',
  'reasoning_tokens',
  'unclassified_tokens',
  'total_tokens',
  'cost_usd',
  'unpriced_tokens',
];

const FORMULA_START = /^[=+\-@\t\r]/;

/**
 * RFC 4180 escaping with a formula-injection guard: a cell that starts with
 * = + - @ TAB or CR gets a leading ' so a spreadsheet shows it as text.
 */
export function escapeCsvCell(value: string): string {
  const guarded = FORMULA_START.test(value) ? `'${value}` : value;
  return /[",\r\n]/.test(guarded) ? `"${guarded.replace(/"/g, '""')}"` : guarded;
}

function metricCells(cell: UsageCell): string[] {
  const { tokens } = cell;
  return [
    cell.requests,
    cell.failures,
    Math.round(cell.latencyMs),
    tokens.uncached,
    tokens.cacheRead,
    tokens.cacheWrite5m,
    tokens.cacheWrite1h,
    tokens.output,
    tokens.reasoning,
    tokens.unclassified,
    totalTokens(cell),
  ]
    .map(String)
    .concat(cell.costUsd === null ? '' : cell.costUsd.toFixed(6), String(cell.unpricedTokens));
}

export function buildUsageCsv(
  report: UsageReport,
  opts: { dimension: UsageDimension; label: (key: string) => string }
): string {
  const { dimension, label } = opts;
  const grouped = dimension !== 'none';
  const order = new Map(report.groups.map((group, index) => [group.key, index]));
  const rank = (group: UsageGroupCell) => order.get(group.key) ?? Number.MAX_SAFE_INTEGER;

  const header = [
    'bucket_start',
    'bucket_end',
    ...(grouped ? [dimension, `${dimension}_label`] : []),
    ...METRIC_COLUMNS,
  ];
  const rows = [header.join(',')];
  for (const bucket of report.buckets) {
    const groups = [...bucket.groups].sort(
      (a, b) => rank(a) - rank(b) || a.key.localeCompare(b.key)
    );
    for (const group of groups) {
      const text = [bucket.start, bucket.end, ...(grouped ? [group.key, label(group.key)] : [])];
      rows.push([...text.map(escapeCsvCell), ...metricCells(group)].join(','));
    }
  }
  return `\uFEFF${rows.join('\r\n')}\r\n`;
}

const safePart = (value: string) => value.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '');

/** usage_<from>_<to>_<bucket>_<groupBy>[_<filter values>].csv, filename-safe. */
export function usageCsvFilename(
  query: UsageReport['query'],
  filter: UsageQuery['filter']
): string {
  const parts = [
    'usage',
    query.from,
    query.to,
    query.bucket,
    query.groupBy,
    ...USAGE_FILTER_DIMENSIONS.map((dimension) => filter[dimension] ?? ''),
  ]
    .map(safePart)
    .filter(Boolean);
  return `${parts.join('_')}.csv`;
}
