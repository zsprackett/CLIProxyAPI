/**
 * Long-term usage history and price catalog
 * (GET /observability/usage/history and /observability/usage/prices).
 *
 * The server does every time-zone computation: bucket `start`/`end` are local
 * wall-clock RFC3339 strings with their own offset. The panel never converts.
 * Untrusted input degrades to empty values rather than throwing.
 */

import { apiClient } from './client';

export const USAGE_UNITS = ['hour', 'day', 'month'] as const;
export type UsageUnit = (typeof USAGE_UNITS)[number];

export const USAGE_DIMENSIONS = ['model', 'provider', 'key', 'auth', 'agent', 'none'] as const;
export type UsageDimension = (typeof USAGE_DIMENSIONS)[number];
export type UsageFilterDimension = Exclude<UsageDimension, 'none'>;
export const USAGE_FILTER_DIMENSIONS: readonly UsageFilterDimension[] = [
  'provider',
  'model',
  'key',
  'auth',
  'agent',
];

/** Group key of the groups the server folds past its per-report group limit. */
export const USAGE_OTHER_GROUP = '*other*';

export type UsageFilter = Partial<Record<UsageFilterDimension, string>>;

export interface UsageQuery {
  /** YYYY-MM-DD in `tz`, inclusive. */
  from: string;
  to: string;
  /** IANA zone, normally Intl.DateTimeFormat().resolvedOptions().timeZone. */
  tz: string;
  bucket: UsageUnit;
  groupBy: UsageDimension;
  filter: UsageFilter;
}

/** Seven disjoint buckets; their sum is the total. `output` excludes reasoning. */
export interface UsageTokens {
  uncached: number;
  cacheRead: number;
  cacheWrite5m: number;
  cacheWrite1h: number;
  output: number;
  reasoning: number;
  unclassified: number;
}
export type UsageTokenType = keyof UsageTokens;
export const USAGE_TOKEN_TYPES: readonly UsageTokenType[] = [
  'uncached',
  'cacheRead',
  'cacheWrite5m',
  'cacheWrite1h',
  'output',
  'reasoning',
  'unclassified',
];

/** Dollars per priced bucket (unclassified is never priced). */
export type UsageCostByType = Omit<UsageTokens, 'unclassified'>;
export type UsagePricedType = keyof UsageCostByType;
export const USAGE_PRICED_TYPES: readonly UsagePricedType[] = [
  'uncached',
  'cacheRead',
  'cacheWrite5m',
  'cacheWrite1h',
  'output',
  'reasoning',
];

export interface UsageCell {
  tokens: UsageTokens;
  requests: number;
  failures: number;
  /** Mean over all attempts, failures included. */
  latencyMs: number;
  /** null when the cell has tokens and none could be priced. */
  costUsd: number | null;
  costByType: UsageCostByType | null;
  /** Tokens of unpriced models plus every unclassified token. */
  unpricedTokens: number;
}

export interface UsageGroupCell extends UsageCell {
  /** Model id, provider, key hash, auth id, agent, "all", or USAGE_OTHER_GROUP. */
  key: string;
}

export interface UsageBucket {
  /** Server-local RFC3339, e.g. 2026-11-01T01:00:00-05:00. Used verbatim for labels and CSV. */
  start: string;
  end: string;
  /** Parsed instants, for geometry ordering only. */
  startMs: number;
  endMs: number;
  groups: UsageGroupCell[];
}

export interface UsageCatalogInfo {
  source: 'builtin' | 'remote';
  snapshotDate: string;
  fetchedAtMs: number | null;
  nextFetchAtMs: number | null;
  lastError: string;
  remote: boolean;
}

export interface UsageReport {
  enabled: boolean;
  /** `disabled`, `home_mode` or `unavailable` when not enabled. */
  reason: string | null;
  query: {
    from: string;
    to: string;
    tz: string;
    bucket: UsageUnit;
    groupBy: UsageDimension;
    /** `from` was moved forward to the retention horizon. */
    clamped: boolean;
  };
  asOfMs: number | null;
  recordingSinceMs: number | null;
  retentionDays: number;
  buckets: UsageBucket[];
  /** Per-group totals over the range, cost desc. Drives legend, table and series order. */
  groups: UsageGroupCell[];
  totals: UsageCell;
  /** How many groups the server folded into USAGE_OTHER_GROUP; 0 when none. */
  foldedGroups: number;
  unpricedModels: string[];
  /** Masked configured key by hash. */
  keys: Record<string, string>;
  options: Record<UsageFilterDimension, string[]>;
  prices: UsageCatalogInfo;
}

export interface UsageRatesPerMTok {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite5m: number;
  cacheWrite1h: number;
  /** Rates filled by a fallback (e.g. reasoning priced as output). */
  derived: string[];
}

export interface UsageModelPrice {
  provider: string;
  model: string;
  priced: boolean;
  matched: string;
  source: 'builtin' | 'remote' | 'override' | '';
  perMTok: UsageRatesPerMTok | null;
  longContext: UsageRatesPerMTok | null;
}

export interface UsagePrices {
  enabled: boolean;
  reason: string | null;
  catalog: UsageCatalogInfo;
  models: UsageModelPrice[];
}

const asRecord = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};

const asArray = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);

const num = (value: unknown): number =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;

const str = (value: unknown): string => (typeof value === 'string' ? value : '');

const strings = (value: unknown): string[] =>
  asArray(value).filter((item): item is string => typeof item === 'string');

const oneOf = <T extends string>(values: readonly T[], value: unknown, fallback: T): T =>
  (values as readonly unknown[]).includes(value) ? (value as T) : fallback;

/** RFC 3339 to ms; garbage and Go's zero time become null. */
const instant = (value: unknown): number | null => {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(value)) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? ms : null;
};

const keyLabels = (value: unknown): Record<string, string> =>
  Object.fromEntries(
    Object.entries(asRecord(value)).filter(
      (entry): entry is [string, string] => typeof entry[1] === 'string'
    )
  );

const readReason = (raw: Record<string, unknown>): string | null =>
  raw.enabled === true ? null : str(raw.reason) || 'disabled';

function readTokens(raw: unknown): UsageTokens {
  const tokens = asRecord(raw);
  return {
    uncached: num(tokens.uncached),
    cacheRead: num(tokens.cache_read),
    cacheWrite5m: num(tokens.cache_write_5m),
    cacheWrite1h: num(tokens.cache_write_1h),
    output: num(tokens.output),
    reasoning: num(tokens.reasoning),
    unclassified: num(tokens.unclassified),
  };
}

function readCell(raw: unknown): UsageCell {
  const cell = asRecord(raw);
  const cost =
    typeof cell.cost_usd === 'number' && Number.isFinite(cell.cost_usd)
      ? Math.max(0, cell.cost_usd)
      : null;
  const byType = asRecord(cell.cost_by_type);
  return {
    tokens: readTokens(cell.tokens),
    requests: num(cell.requests),
    failures: num(cell.failures),
    latencyMs: num(cell.latency_ms),
    costUsd: cost,
    // The server sends both or neither; a lone cost_by_type is dropped to keep that.
    costByType:
      cost !== null && cell.cost_by_type !== null && typeof cell.cost_by_type === 'object'
        ? {
            uncached: num(byType.uncached),
            cacheRead: num(byType.cache_read),
            cacheWrite5m: num(byType.cache_write_5m),
            cacheWrite1h: num(byType.cache_write_1h),
            output: num(byType.output),
            reasoning: num(byType.reasoning),
          }
        : null,
    unpricedTokens: num(cell.unpriced_tokens),
  };
}

const readGroups = (value: unknown): UsageGroupCell[] =>
  asArray(value).flatMap((item) => {
    const group = asRecord(item);
    return typeof group.key === 'string' ? [{ key: group.key, ...readCell(group) }] : [];
  });

function readBucket(raw: unknown): UsageBucket | null {
  const bucket = asRecord(raw);
  const startMs = instant(bucket.start);
  const endMs = instant(bucket.end);
  if (startMs === null || endMs === null) return null;
  return {
    start: str(bucket.start),
    end: str(bucket.end),
    startMs,
    endMs,
    groups: readGroups(bucket.groups),
  };
}

function readCatalog(raw: unknown): UsageCatalogInfo {
  const catalog = asRecord(raw);
  return {
    source: oneOf(['builtin', 'remote'] as const, catalog.source, 'builtin'),
    snapshotDate: str(catalog.snapshot_date),
    fetchedAtMs: instant(catalog.fetched_at),
    nextFetchAtMs: instant(catalog.next_fetch_at),
    lastError: str(catalog.last_error),
    remote: catalog.remote === true,
  };
}

export function normalizeUsageReport(raw: unknown): UsageReport {
  const report = asRecord(raw);
  const query = asRecord(report.query);
  const options = asRecord(report.options);
  return {
    enabled: report.enabled === true,
    reason: readReason(report),
    query: {
      from: str(query.from),
      to: str(query.to),
      tz: str(query.tz),
      bucket: oneOf(USAGE_UNITS, query.bucket, 'day'),
      groupBy: oneOf(USAGE_DIMENSIONS, query.group_by, 'model'),
      clamped: query.clamped === true,
    },
    asOfMs: instant(report.as_of),
    recordingSinceMs: instant(report.recording_since),
    retentionDays: Math.floor(num(report.retention_days)),
    buckets: asArray(report.buckets).flatMap((item) => {
      const bucket = readBucket(item);
      return bucket ? [bucket] : [];
    }),
    groups: readGroups(report.groups),
    totals: readCell(report.totals),
    foldedGroups: Math.floor(num(report.folded_groups)),
    unpricedModels: strings(report.unpriced_models),
    keys: keyLabels(report.keys),
    options: {
      provider: strings(options.provider),
      model: strings(options.model),
      key: strings(options.key),
      auth: strings(options.auth),
      agent: strings(options.agent),
    },
    prices: readCatalog(report.prices),
  };
}

function readRates(raw: unknown): UsageRatesPerMTok | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const rates = asRecord(raw);
  return {
    input: num(rates.input),
    output: num(rates.output),
    reasoning: num(rates.reasoning),
    cacheRead: num(rates.cache_read),
    cacheWrite5m: num(rates.cache_write_5m),
    cacheWrite1h: num(rates.cache_write_1h),
    derived: strings(rates.derived),
  };
}

function readModelPrice(raw: unknown): UsageModelPrice | null {
  const price = asRecord(raw);
  if (typeof price.model !== 'string' || !price.model) return null;
  return {
    provider: str(price.provider),
    model: price.model,
    priced: price.priced === true,
    matched: str(price.matched),
    source: oneOf(['builtin', 'remote', 'override', ''] as const, price.source, ''),
    perMTok: readRates(price.per_mtok),
    longContext: readRates(price.long_context),
  };
}

export function normalizeUsagePrices(raw: unknown): UsagePrices {
  const prices = asRecord(raw);
  return {
    enabled: prices.enabled === true,
    reason: readReason(prices),
    catalog: readCatalog(prices.catalog),
    models: asArray(prices.models).flatMap((item) => {
      const price = readModelPrice(item);
      return price ? [price] : [];
    }),
  };
}

/** Query -> wire params (`group`, flat filters; empty values omitted). Pure. */
export function serializeUsageQuery(query: UsageQuery): Record<string, string> {
  const params: Record<string, string> = {};
  const set = (name: string, value: string | undefined) => {
    if (value) params[name] = value;
  };
  set('from', query.from);
  set('to', query.to);
  set('tz', query.tz);
  set('bucket', query.bucket);
  set('group', query.groupBy);
  for (const dimension of USAGE_FILTER_DIMENSIONS) set(dimension, query.filter[dimension]);
  return params;
}

export const usageHistoryApi = {
  async get(query: UsageQuery): Promise<UsageReport> {
    const raw = await apiClient.get<unknown>('/observability/usage/history', {
      params: serializeUsageQuery(query),
    });
    return normalizeUsageReport(raw);
  },

  async prices(): Promise<UsagePrices> {
    const raw = await apiClient.get<unknown>('/observability/usage/prices');
    return normalizeUsagePrices(raw);
  },
};
