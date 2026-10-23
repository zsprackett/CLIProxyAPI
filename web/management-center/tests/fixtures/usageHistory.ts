/**
 * Wire fixtures for GET /observability/usage/history and /observability/usage/prices.
 * Field names follow internal/usagehistory/types.go and report.go exactly.
 */

export const rawTokens = (overrides: Record<string, number> = {}) => ({
  uncached: 1200,
  cache_read: 80000,
  cache_write_5m: 3000,
  cache_write_1h: 1000,
  output: 900,
  reasoning: 100,
  unclassified: 0,
  ...overrides,
});

export const rawCostByType = (overrides: Record<string, number> = {}) => ({
  uncached: 0.006,
  cache_read: 0.04,
  cache_write_5m: 0.01875,
  cache_write_1h: 0.01,
  output: 0.0225,
  reasoning: 0.0025,
  ...overrides,
});

export const rawCell = (overrides: Record<string, unknown> = {}) => ({
  tokens: rawTokens(),
  requests: 10,
  failures: 1,
  latency_ms: 1520.5,
  cost_usd: 0.09975,
  cost_by_type: rawCostByType(),
  unpriced_tokens: 0,
  ...overrides,
});

export const rawGroup = (key: string, overrides: Record<string, unknown> = {}) => ({
  key,
  ...rawCell(overrides),
});

/** An unpriced cell: tokens but no price at all. */
export const rawUnpricedCell = (overrides: Record<string, unknown> = {}) =>
  rawCell({
    tokens: rawTokens({ cache_write_1h: 0, unclassified: 0 }),
    cost_usd: null,
    cost_by_type: null,
    unpriced_tokens: 85200,
    ...overrides,
  });

export const rawBucket = (start: string, end: string, groups: unknown[]) => ({
  start,
  end,
  groups,
});

/** Three days in America/Toronto grouped by model, one model unpriced. */
export const rawReport = (overrides: Record<string, unknown> = {}) => ({
  enabled: true,
  query: {
    from: '2026-10-20',
    to: '2026-10-22',
    tz: 'America/Toronto',
    bucket: 'day',
    group_by: 'model',
    clamped: false,
  },
  as_of: '2026-10-22T16:04:05Z',
  recording_since: '2026-01-01T05:00:00Z',
  retention_days: 365,
  buckets: [
    rawBucket('2026-10-20T00:00:00-04:00', '2026-10-21T00:00:00-04:00', [
      rawGroup('claude-opus-5-5', { requests: 6, cost_usd: 0.06 }),
      rawGroup('claude-sonnet-5', { requests: 4, cost_usd: 0.03 }),
    ]),
    rawBucket('2026-10-21T00:00:00-04:00', '2026-10-22T00:00:00-04:00', []),
    rawBucket('2026-10-22T00:00:00-04:00', '2026-10-23T00:00:00-04:00', [
      rawGroup('claude-opus-5-5', { requests: 2, cost_usd: 0.02 }),
      { key: 'devin/swe-1', ...rawUnpricedCell({ requests: 3 }) },
    ]),
  ],
  groups: [
    rawGroup('claude-opus-5-5', { requests: 8, cost_usd: 0.08 }),
    rawGroup('claude-sonnet-5', { requests: 4, cost_usd: 0.03 }),
    { key: 'devin/swe-1', ...rawUnpricedCell({ requests: 3 }) },
  ],
  totals: rawCell({
    requests: 15,
    failures: 3,
    cost_usd: 0.11,
    unpriced_tokens: 85200,
  }),
  folded_groups: 0,
  unpriced_models: ['devin/devin/swe-1'],
  keys: { a1b2c3d4e5f6: 'sk-...9f2c' },
  options: {
    provider: ['claude', 'devin'],
    model: ['claude-opus-5-5', 'claude-sonnet-5', 'devin/swe-1'],
    key: ['a1b2c3d4e5f6', '-'],
    auth: ['claude-max-1.json'],
    agent: ['claude-cli'],
  },
  prices: {
    source: 'remote',
    snapshot_date: '2026-10-01',
    fetched_at: '2026-10-22T03:00:00Z',
    next_fetch_at: '2026-10-23T03:00:00Z',
    remote: true,
  },
  ...overrides,
});

/** Grouped by key with more groups than the server keeps: the tail folds into "*other*". */
export const rawFoldedReport = () =>
  rawReport({
    query: {
      from: '2026-10-22',
      to: '2026-10-22',
      tz: 'UTC',
      bucket: 'day',
      group_by: 'key',
      clamped: true,
    },
    buckets: [
      rawBucket('2026-10-22T00:00:00Z', '2026-10-23T00:00:00Z', [
        rawGroup('a1b2c3d4e5f6'),
        rawGroup('*other*', { requests: 40, cost_usd: 0.4 }),
      ]),
    ],
    groups: [rawGroup('*other*', { requests: 40, cost_usd: 0.4 }), rawGroup('a1b2c3d4e5f6')],
    totals: rawCell({ requests: 50, cost_usd: 0.5 }),
    folded_groups: 12,
    unpriced_models: [],
  });

export const rawDisabledReport = (reason = 'disabled') => ({
  enabled: false,
  reason,
  query: { from: '', to: '', tz: '', bucket: '', group_by: '', clamped: false },
  as_of: '0001-01-01T00:00:00Z',
  retention_days: 0,
  buckets: null,
  groups: null,
  totals: rawCell({
    tokens: rawTokens({
      uncached: 0,
      cache_read: 0,
      cache_write_5m: 0,
      cache_write_1h: 0,
      output: 0,
      reasoning: 0,
    }),
    requests: 0,
    failures: 0,
    latency_ms: 0,
    cost_usd: 0,
    cost_by_type: rawCostByType({
      uncached: 0,
      cache_read: 0,
      cache_write_5m: 0,
      cache_write_1h: 0,
      output: 0,
      reasoning: 0,
    }),
  }),
  folded_groups: 0,
  unpriced_models: null,
  keys: null,
  options: { provider: null, model: null, key: null, auth: null, agent: null },
  prices: { source: 'builtin', snapshot_date: '2026-10-01', remote: false },
});

export const rawRates = (overrides: Record<string, unknown> = {}) => ({
  input: 5,
  output: 25,
  reasoning: 25,
  cache_read: 0.5,
  cache_write_5m: 6.25,
  cache_write_1h: 10,
  ...overrides,
});

export const rawPrices = (overrides: Record<string, unknown> = {}) => ({
  enabled: true,
  catalog: {
    source: 'remote',
    snapshot_date: '2026-10-01',
    fetched_at: '2026-10-22T03:00:00Z',
    next_fetch_at: '2026-10-23T03:00:00Z',
    last_error: '',
    remote: true,
  },
  models: [
    {
      provider: 'claude',
      model: 'claude-opus-5-5',
      priced: true,
      matched: 'claude-opus-5-5',
      source: 'remote',
      per_mtok: rawRates({ derived: ['reasoning'] }),
      long_context: rawRates({ input: 10, output: 37.5 }),
    },
    {
      provider: 'claude',
      model: 'claude-opus-4-6-thinking',
      priced: true,
      matched: 'override:claude-opus-4-6-thinking same-as claude-opus-4-6',
      source: 'override',
      per_mtok: rawRates(),
    },
    { provider: 'devin', model: 'devin/swe-1', priced: false },
  ],
  ...overrides,
});
