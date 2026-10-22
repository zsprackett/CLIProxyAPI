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
  'uncached', 'cacheRead', 'cacheWrite5m', 'cacheWrite1h', 'output', 'reasoning', 'unclassified',
];

/** Dollars per priced bucket (unclassified is never priced). */
export type UsageCostByType = Omit<UsageTokens, 'unclassified'>;

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
  /** Model id, provider, key hash, auth id, agent, or "all". */
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

/** Query -> wire params (`group`, flat filters; empty filters omitted). Pure. */
export function serializeUsageQuery(query: UsageQuery): Record<string, string> {
  throw new Error('not implemented');
}

/** Pure, never throws. Unknown units/dimensions fall back to day/model. */
export function normalizeUsageReport(raw: unknown): UsageReport {
  throw new Error('not implemented');
}

/** Pure, never throws. */
export function normalizeUsagePrices(raw: unknown): UsagePrices {
  throw new Error('not implemented');
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

// services/api/index.ts: export { usageHistoryApi } from './usageHistory';
// services/api/quotaForecast.ts: normalize the new optional attribution `reason` string.
