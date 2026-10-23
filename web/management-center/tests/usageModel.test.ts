import { describe, expect, test } from 'bun:test';
import {
  DEFAULT_USAGE_CONTROLS,
  MAX_SPAN_DAYS,
  allowedUnits,
  applyPreset,
  bucketLabel,
  bucketLabels,
  controlsToQuery,
  coversToday,
  formatTokens,
  formatUsd,
  groupLabel,
  metricValue,
  presetRange,
  spanDays,
  suggestBucket,
  summarize,
  todayIn,
  totalTokens,
  type UsageControls,
} from '@/features/usage/usageModel';
import { USAGE_OTHER_GROUP, normalizeUsageReport } from '@/services/api/usageHistory';
import { rawReport } from './fixtures/usageHistory';

const TODAY = '2026-10-22';
const t = (key: string) => `t:${key}`;

describe('presets and spans', () => {
  test('presets include today and count back whole days', () => {
    expect(presetRange('24h', TODAY)).toEqual({ from: '2026-10-21', to: TODAY });
    expect(presetRange('7d', TODAY)).toEqual({ from: '2026-10-16', to: TODAY });
    expect(presetRange('30d', TODAY)).toEqual({ from: '2026-09-23', to: TODAY });
    expect(presetRange('90d', TODAY)).toEqual({ from: '2026-07-25', to: TODAY });
    expect(presetRange('365d', TODAY)).toEqual({ from: '2025-10-23', to: TODAY });
    expect(presetRange('mtd', TODAY)).toEqual({ from: '2026-10-01', to: TODAY });
    expect(presetRange('ytd', TODAY)).toEqual({ from: '2026-01-01', to: TODAY });
  });

  test('a span counts both ends', () => {
    expect(spanDays('2026-10-22', '2026-10-22')).toBe(1);
    expect(spanDays('2026-10-16', '2026-10-22')).toBe(7);
    expect(spanDays('2026-02-27', '2026-03-02')).toBe(4);
  });

  test('allowed units follow the server span limits', () => {
    expect(MAX_SPAN_DAYS).toEqual({ hour: 31, day: 1100, month: 3660 });
    expect(allowedUnits(31)).toEqual(['hour', 'day', 'month']);
    expect(allowedUnits(32)).toEqual(['day', 'month']);
    expect(allowedUnits(1101)).toEqual(['month']);
  });

  test('the suggested unit grows with the span', () => {
    expect(suggestBucket(2)).toBe('hour');
    expect(suggestBucket(3)).toBe('day');
    expect(suggestBucket(92)).toBe('day');
    expect(suggestBucket(93)).toBe('month');
  });
});

describe('controlsToQuery', () => {
  test('builds the default 30 day query by model', () => {
    expect(controlsToQuery(DEFAULT_USAGE_CONTROLS, 'America/Toronto', TODAY)).toEqual({
      from: '2026-09-23',
      to: TODAY,
      tz: 'America/Toronto',
      bucket: 'day',
      groupBy: 'model',
      filter: {},
    });
  });

  test('replaces a bucket the span does not allow', () => {
    const controls: UsageControls = { ...DEFAULT_USAGE_CONTROLS, preset: '90d', bucket: 'hour' };
    expect(controlsToQuery(controls, 'UTC', TODAY).bucket).toBe('day');
  });

  test('custom ranges are ordered, and an empty custom range falls back to 30 days', () => {
    const custom: UsageControls = {
      ...DEFAULT_USAGE_CONTROLS,
      preset: 'custom',
      from: '2026-10-10',
      to: '2026-10-01',
    };
    expect(controlsToQuery(custom, 'UTC', TODAY)).toMatchObject({
      from: '2026-10-01',
      to: '2026-10-10',
    });
    const empty: UsageControls = { ...DEFAULT_USAGE_CONTROLS, preset: 'custom' };
    expect(controlsToQuery(empty, 'UTC', TODAY)).toMatchObject({ from: '2026-09-23', to: TODAY });
  });

  test('drops empty filters', () => {
    const controls: UsageControls = {
      ...DEFAULT_USAGE_CONTROLS,
      filter: { provider: 'claude', model: '' },
    };
    expect(controlsToQuery(controls, 'UTC', TODAY).filter).toEqual({ provider: 'claude' });
  });

  test('applyPreset suggests a unit and seeds a custom range from the current one', () => {
    expect(applyPreset(DEFAULT_USAGE_CONTROLS, '24h', TODAY)).toMatchObject({
      preset: '24h',
      bucket: 'hour',
    });
    expect(applyPreset(DEFAULT_USAGE_CONTROLS, '365d', TODAY).bucket).toBe('month');
    expect(applyPreset(DEFAULT_USAGE_CONTROLS, 'custom', TODAY)).toMatchObject({
      preset: 'custom',
      from: '2026-09-23',
      to: TODAY,
      bucket: 'day',
    });
  });
});

describe('labels', () => {
  test('day and month labels come from the digits, not a time zone', () => {
    expect(bucketLabel('2026-10-22T00:00:00-04:00', 'day', 'en', false)).toBe('Oct 22');
    expect(bucketLabel('2026-10-01T00:00:00-04:00', 'month', 'en', false)).toBe('Oct 2026');
    expect(bucketLabel('2026-01-01T00:00:00+14:00', 'day', 'en', false)).toBe('Jan 1');
  });

  test('hour labels add the offset only when a wall time repeats', () => {
    expect(bucketLabel('2026-11-01T01:00:00-05:00', 'hour', 'en', false)).toBe('11-01 01:00');
    expect(bucketLabel('2026-11-01T01:00:00-05:00', 'hour', 'en', true)).toBe(
      '11-01 01:00 (-05:00)'
    );
    expect(bucketLabel('2026-11-01T01:00:00Z', 'hour', 'en', true)).toBe('11-01 01:00 (Z)');
  });

  test('bucketLabels marks the fall-back hour twice', () => {
    const at = (start: string) => ({ start, end: start, startMs: 0, endMs: 0, groups: [] });
    expect(
      bucketLabels(
        [
          at('2026-11-01T00:00:00-04:00'),
          at('2026-11-01T01:00:00-04:00'),
          at('2026-11-01T01:00:00-05:00'),
          at('2026-11-01T02:00:00-05:00'),
        ],
        'hour',
        'en'
      )
    ).toEqual(['11-01 00:00', '11-01 01:00 (-04:00)', '11-01 01:00 (-05:00)', '11-01 02:00']);
  });

  test('group labels never show a raw key', () => {
    const labels = new Map([['a1b2c3d4e5f6', 'Laptop']]);
    expect(groupLabel('key', 'a1b2c3d4e5f6', labels, t)).toBe('Laptop');
    expect(groupLabel('key', 'ffffffffffff', labels, t)).toBe('#ffffffffffff');
    expect(groupLabel('key', '-', labels, t)).toBe('t:usage.no_key');
    expect(groupLabel('auth', '-', labels, t)).toBe('t:usage.no_auth');
    expect(groupLabel('auth', 'claude-max-1.json', labels, t)).toBe('claude-max-1.json');
    expect(groupLabel('agent', '-', labels, t)).toBe('t:usage.unknown_agent');
    expect(groupLabel('none', 'all', labels, t)).toBe('t:usage.all');
    expect(groupLabel('model', 'claude-opus-5-5', labels, t)).toBe('claude-opus-5-5');
    expect(groupLabel('model', USAGE_OTHER_GROUP, labels, t)).toBe('t:usage.other_groups');
  });
});

describe('summary', () => {
  const report = normalizeUsageReport(rawReport());

  test('sums the seven token buckets', () => {
    expect(totalTokens(report.totals)).toBe(1200 + 80000 + 3000 + 1000 + 900 + 100);
  });

  test('flags a partial cost so the view says "at least"', () => {
    expect(summarize(report.totals)).toEqual({
      costUsd: 0.11,
      costPartial: true,
      totalTokens: 86200,
      requests: 15,
      failureRate: 3 / 15,
      cacheReadShare: 80000 / 85200,
      write1hShare: 0.25,
      cacheWriteCostUsd: 0.01875 + 0.01,
    });
  });

  test('a fully priced or fully unpriced total is not partial', () => {
    expect(summarize({ ...report.totals, unpricedTokens: 0 }).costPartial).toBe(false);
    const unpriced = summarize({ ...report.totals, costUsd: null, costByType: null });
    expect(unpriced.costPartial).toBe(false);
    expect(unpriced.costUsd).toBeNull();
    expect(unpriced.cacheWriteCostUsd).toBeNull();
  });

  test('an empty total has zero shares, not NaN', () => {
    const empty = normalizeUsageReport({}).totals;
    expect(summarize(empty)).toMatchObject({
      failureRate: 0,
      cacheReadShare: 0,
      write1hShare: 0,
    });
  });

  test('metric values stack a null cost as zero', () => {
    const devin = report.groups[2];
    expect(metricValue(devin, 'cost')).toBe(0);
    expect(metricValue(devin, 'requests')).toBe(3);
    expect(metricValue(devin, 'tokens')).toBe(totalTokens(devin));
  });

  test('coversToday compares civil dates', () => {
    expect(coversToday(report.query, TODAY)).toBe(true);
    expect(coversToday(report.query, '2026-10-23')).toBe(false);
    expect(coversToday(normalizeUsageReport({}).query, TODAY)).toBe(false);
  });

  test('todayIn reads the civil date in the zone', () => {
    const now = Date.parse('2026-10-23T02:30:00Z');
    expect(todayIn('America/Toronto', now)).toBe('2026-10-22');
    expect(todayIn('UTC', now)).toBe('2026-10-23');
    expect(todayIn('Not/AZone', now)).toBe('2026-10-23');
  });

  test('formats dollars and tokens', () => {
    expect(formatUsd(null, 'en')).toBe('-');
    expect(formatUsd(0, 'en')).toBe('$0.00');
    expect(formatUsd(12.345, 'en')).toBe('$12.35');
    expect(formatUsd(0.0023, 'en')).toBe('$0.0023');
    expect(formatUsd(1234.5, 'en')).toBe('$1,234.50');
    expect(formatTokens(86200, 'en')).toBe('86.2K');
    expect(formatTokens(950, 'en')).toBe('950');
  });
});
