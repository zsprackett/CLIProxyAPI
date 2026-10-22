import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import { usageHistoryApi as exported } from '@/services/api';
import {
  USAGE_OTHER_GROUP,
  normalizeUsagePrices,
  normalizeUsageReport,
  serializeUsageQuery,
  usageHistoryApi,
} from '@/services/api/usageHistory';
import { rawDisabledReport, rawFoldedReport, rawPrices, rawReport } from './fixtures/usageHistory';

describe('normalizeUsageReport', () => {
  test('maps the snake_case report into typed camelCase', () => {
    const report = normalizeUsageReport(rawReport());
    expect(report).toMatchObject({
      enabled: true,
      reason: null,
      query: {
        from: '2026-10-20',
        to: '2026-10-22',
        tz: 'America/Toronto',
        bucket: 'day',
        groupBy: 'model',
        clamped: false,
      },
      asOfMs: Date.parse('2026-10-22T16:04:05Z'),
      recordingSinceMs: Date.parse('2026-01-01T05:00:00Z'),
      retentionDays: 365,
      foldedGroups: 0,
      unpricedModels: ['devin/devin/swe-1'],
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
        snapshotDate: '2026-10-01',
        fetchedAtMs: Date.parse('2026-10-22T03:00:00Z'),
        nextFetchAtMs: Date.parse('2026-10-23T03:00:00Z'),
        lastError: '',
        remote: true,
      },
    });
    expect(report.buckets).toHaveLength(3);
    expect(report.buckets[1]).toEqual({
      start: '2026-10-21T00:00:00-04:00',
      end: '2026-10-22T00:00:00-04:00',
      startMs: Date.parse('2026-10-21T04:00:00Z'),
      endMs: Date.parse('2026-10-22T04:00:00Z'),
      groups: [],
    });
    expect(report.buckets[0].groups.map((group) => group.key)).toEqual([
      'claude-opus-5-5',
      'claude-sonnet-5',
    ]);
    expect(report.groups.map((group) => group.key)).toEqual([
      'claude-opus-5-5',
      'claude-sonnet-5',
      'devin/swe-1',
    ]);
  });

  test('maps a cell with every token bucket and the cost per type', () => {
    const [opus] = normalizeUsageReport(rawReport()).groups;
    expect(opus).toEqual({
      key: 'claude-opus-5-5',
      tokens: {
        uncached: 1200,
        cacheRead: 80000,
        cacheWrite5m: 3000,
        cacheWrite1h: 1000,
        output: 900,
        reasoning: 100,
        unclassified: 0,
      },
      requests: 8,
      failures: 1,
      latencyMs: 1520.5,
      costUsd: 0.08,
      costByType: {
        uncached: 0.006,
        cacheRead: 0.04,
        cacheWrite5m: 0.01875,
        cacheWrite1h: 0.01,
        output: 0.0225,
        reasoning: 0.0025,
      },
      unpricedTokens: 0,
    });
  });

  test('keeps a null cost as null, never as zero', () => {
    const devin = normalizeUsageReport(rawReport()).groups[2];
    expect(devin.costUsd).toBeNull();
    expect(devin.costByType).toBeNull();
    expect(devin.unpricedTokens).toBe(85200);
  });

  test('a cost per type without a cost is dropped, so the two stay consistent', () => {
    const report = normalizeUsageReport(
      rawReport({ totals: { cost_usd: null, cost_by_type: { uncached: 1 } } })
    );
    expect(report.totals.costUsd).toBeNull();
    expect(report.totals.costByType).toBeNull();
  });

  test('carries the folded group count and the "*other*" group key', () => {
    const report = normalizeUsageReport(rawFoldedReport());
    expect(USAGE_OTHER_GROUP).toBe('*other*');
    expect(report.foldedGroups).toBe(12);
    expect(report.query).toMatchObject({ groupBy: 'key', clamped: true, tz: 'UTC' });
    expect(report.groups[0].key).toBe(USAGE_OTHER_GROUP);
    expect(report.buckets[0].groups.map((group) => group.key)).toEqual([
      'a1b2c3d4e5f6',
      USAGE_OTHER_GROUP,
    ]);
  });

  test('a disabled report with Go nil slices and the zero time reads as empty', () => {
    for (const reason of ['disabled', 'home_mode', 'unavailable']) {
      const report = normalizeUsageReport(rawDisabledReport(reason));
      expect(report.enabled).toBe(false);
      expect(report.reason).toBe(reason);
      expect(report.asOfMs).toBeNull();
      expect(report.recordingSinceMs).toBeNull();
      expect(report.buckets).toEqual([]);
      expect(report.groups).toEqual([]);
      expect(report.unpricedModels).toEqual([]);
      expect(report.keys).toEqual({});
      expect(report.options).toEqual({ provider: [], model: [], key: [], auth: [], agent: [] });
      expect(report.query.bucket).toBe('day');
      expect(report.query.groupBy).toBe('model');
      expect(report.prices.fetchedAtMs).toBeNull();
    }
  });

  test('never throws on garbage and falls back to safe defaults', () => {
    for (const raw of [null, undefined, 42, 'x', [], { enabled: 'yes' }]) {
      const report = normalizeUsageReport(raw);
      expect(report.enabled).toBe(false);
      expect(report.reason).toBe('disabled');
      expect(report.buckets).toEqual([]);
      expect(report.totals.costUsd).toBeNull();
      expect(report.totals.tokens.output).toBe(0);
      expect(report.prices.source).toBe('builtin');
    }
    const report = normalizeUsageReport(
      rawReport({
        query: { bucket: 'week', group_by: 'region' },
        buckets: [
          null,
          { start: 'yesterday', end: 'today', groups: [] },
          { start: '2026-10-22T00:00:00Z', end: '2026-10-23T00:00:00Z', groups: [7, { key: 3 }] },
        ],
        groups: 'many',
        totals: { tokens: { uncached: -5, output: 'NaN' }, requests: Infinity, cost_usd: 'free' },
        folded_groups: -1,
        unpriced_models: ['a', 1, null],
        keys: { ok: 'sk-...1', bad: 1 },
        options: { model: 'x', key: ['k', 2] },
        prices: { source: 'ftp', fetched_at: 'soon' },
      })
    );
    expect(report.query.bucket).toBe('day');
    expect(report.query.groupBy).toBe('model');
    expect(report.buckets).toHaveLength(1);
    expect(report.buckets[0].groups).toEqual([]);
    expect(report.groups).toEqual([]);
    expect(report.totals.tokens.uncached).toBe(0);
    expect(report.totals.tokens.output).toBe(0);
    expect(report.totals.requests).toBe(0);
    expect(report.totals.costUsd).toBeNull();
    expect(report.foldedGroups).toBe(0);
    expect(report.unpricedModels).toEqual(['a']);
    expect(report.keys).toEqual({ ok: 'sk-...1' });
    expect(report.options.model).toEqual([]);
    expect(report.options.key).toEqual(['k']);
    expect(report.prices.source).toBe('builtin');
    expect(report.prices.fetchedAtMs).toBeNull();
  });
});

describe('normalizeUsagePrices', () => {
  test('maps the catalog and each model price', () => {
    const prices = normalizeUsagePrices(rawPrices());
    expect(prices.enabled).toBe(true);
    expect(prices.reason).toBeNull();
    expect(prices.catalog).toEqual({
      source: 'remote',
      snapshotDate: '2026-10-01',
      fetchedAtMs: Date.parse('2026-10-22T03:00:00Z'),
      nextFetchAtMs: Date.parse('2026-10-23T03:00:00Z'),
      lastError: '',
      remote: true,
    });
    const [opus, override, devin] = prices.models;
    expect(opus).toEqual({
      provider: 'claude',
      model: 'claude-opus-5-5',
      priced: true,
      matched: 'claude-opus-5-5',
      source: 'remote',
      perMTok: {
        input: 5,
        output: 25,
        reasoning: 25,
        cacheRead: 0.5,
        cacheWrite5m: 6.25,
        cacheWrite1h: 10,
        derived: ['reasoning'],
      },
      longContext: {
        input: 10,
        output: 37.5,
        reasoning: 25,
        cacheRead: 0.5,
        cacheWrite5m: 6.25,
        cacheWrite1h: 10,
        derived: [],
      },
    });
    expect(override.source).toBe('override');
    expect(override.longContext).toBeNull();
    expect(devin).toEqual({
      provider: 'devin',
      model: 'devin/swe-1',
      priced: false,
      matched: '',
      source: '',
      perMTok: null,
      longContext: null,
    });
  });

  test('never throws on garbage', () => {
    for (const raw of [null, 'x', [], { models: 'all' }]) {
      const prices = normalizeUsagePrices(raw);
      expect(prices.enabled).toBe(false);
      expect(prices.reason).toBe('disabled');
      expect(prices.models).toEqual([]);
      expect(prices.catalog.source).toBe('builtin');
    }
    const prices = normalizeUsagePrices(
      rawPrices({ models: [null, { model: 1 }, { provider: 'x', model: 'y', source: 'guess' }] })
    );
    expect(prices.models).toEqual([
      {
        provider: 'x',
        model: 'y',
        priced: false,
        matched: '',
        source: '',
        perMTok: null,
        longContext: null,
      },
    ]);
  });
});

describe('serializeUsageQuery', () => {
  test('sends group for groupBy and flat filters, omitting empty ones', () => {
    expect(
      serializeUsageQuery({
        from: '2026-01-01',
        to: '2026-10-22',
        tz: 'America/Toronto',
        bucket: 'day',
        groupBy: 'key',
        filter: { provider: 'claude', model: '', key: 'a1b2c3d4e5f6', auth: undefined },
      })
    ).toEqual({
      from: '2026-01-01',
      to: '2026-10-22',
      tz: 'America/Toronto',
      bucket: 'day',
      group: 'key',
      provider: 'claude',
      key: 'a1b2c3d4e5f6',
    });
  });
});

describe('usageHistoryApi', () => {
  const restore: Array<() => void> = [];
  afterEach(() => {
    while (restore.length) restore.pop()?.();
  });

  test('is re-exported from the api index', () => {
    expect(exported).toBe(usageHistoryApi);
  });

  test('get reads the history path with the serialized query', async () => {
    const spy = spyOn(apiClient, 'get').mockResolvedValue(rawReport());
    restore.push(() => spy.mockRestore());
    const report = await usageHistoryApi.get({
      from: '2026-10-20',
      to: '2026-10-22',
      tz: 'America/Toronto',
      bucket: 'hour',
      groupBy: 'agent',
      filter: { agent: 'claude-cli' },
    });
    expect(spy).toHaveBeenCalledWith('/observability/usage/history', {
      params: {
        from: '2026-10-20',
        to: '2026-10-22',
        tz: 'America/Toronto',
        bucket: 'hour',
        group: 'agent',
        agent: 'claude-cli',
      },
    });
    expect(report.groups).toHaveLength(3);
  });

  test('prices reads the prices path', async () => {
    const spy = spyOn(apiClient, 'get').mockResolvedValue(rawPrices());
    restore.push(() => spy.mockRestore());
    const prices = await usageHistoryApi.prices();
    expect(spy).toHaveBeenCalledWith('/observability/usage/prices');
    expect(prices.models).toHaveLength(3);
  });
});
