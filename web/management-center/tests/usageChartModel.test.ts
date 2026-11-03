import { describe, expect, test } from 'bun:test';
import {
  MAX_SERIES,
  OTHER_COLOR,
  OTHER_SERIES,
  TOKEN_TYPE_COLOR,
  TREND_WINDOW,
  buildStacks,
  buildTrend,
  buildUsageChartGeometry,
  niceTicks,
} from '@/features/usage/usageChartModel';
import { USAGE_OTHER_GROUP, normalizeUsageReport } from '@/services/api/usageHistory';
import { rawBucket, rawFoldedReport, rawGroup, rawReport } from './fixtures/usageHistory';

const label = (id: string) => `label:${id}`;

/** Twelve models in two daily buckets, the tail priced lower so the order is by index. */
const manyGroups = () => {
  const keys = Array.from({ length: 12 }, (_, index) => `model-${index}`);
  const cost = (index: number) => 12 - index;
  return normalizeUsageReport(
    rawReport({
      buckets: [
        rawBucket(
          '2026-10-21T00:00:00Z',
          '2026-10-22T00:00:00Z',
          keys.map((key, index) => rawGroup(key, { cost_usd: cost(index), requests: 1 }))
        ),
        rawBucket('2026-10-22T00:00:00Z', '2026-10-23T00:00:00Z', [
          rawGroup('model-0', { cost_usd: 5, requests: 2 }),
          rawGroup('model-11', { cost_usd: 1, requests: 3 }),
        ]),
      ],
      groups: keys.map((key, index) => rawGroup(key, { cost_usd: cost(index) })),
    })
  );
};

describe('buildStacks by group', () => {
  test('keeps the top eight groups in report order and folds the rest into other', () => {
    const stacks = buildStacks(manyGroups(), { metric: 'cost', stackBy: 'group', label });
    expect(stacks.series).toHaveLength(MAX_SERIES + 1);
    expect(stacks.series.slice(0, MAX_SERIES).map((series) => series.id)).toEqual(
      Array.from({ length: MAX_SERIES }, (_, index) => `model-${index}`)
    );
    expect(stacks.series.slice(0, MAX_SERIES).map((series) => series.colorIndex)).toEqual([
      0, 1, 2, 3, 4, 5, 6, 7,
    ]);
    const other = stacks.series[MAX_SERIES];
    expect(other).toMatchObject({ id: OTHER_SERIES, colorIndex: OTHER_COLOR });
    expect(other.label).toBe(`label:${OTHER_SERIES}`);
    // models 8..11 cost 4 + 3 + 2 + 1 on day one, model-11 costs 1 on day two.
    expect(other.values).toEqual([10, 1]);
    expect(stacks.series[0].values).toEqual([12, 5]);
    expect(stacks.totals).toEqual([78, 6]);
  });

  test('the server folded group joins the other series and never takes a palette slot', () => {
    const stacks = buildStacks(normalizeUsageReport(rawFoldedReport()), {
      metric: 'requests',
      stackBy: 'group',
      label,
    });
    expect(stacks.series.map((series) => series.id)).toEqual(['a1b2c3d4e5f6', OTHER_SERIES]);
    expect(stacks.series[0].colorIndex).toBe(0);
    expect(stacks.series[1].values).toEqual([40]);
    expect(stacks.series.some((series) => series.id === USAGE_OTHER_GROUP)).toBe(false);
  });

  test('a null cost stacks as zero and an all-zero series is dropped', () => {
    const report = normalizeUsageReport(rawReport());
    const stacks = buildStacks(report, { metric: 'cost', stackBy: 'group', label });
    expect(stacks.series.map((series) => series.id)).toEqual([
      'claude-opus-5-5',
      'claude-sonnet-5',
    ]);
    expect(stacks.totals).toEqual([0.09, 0, 0.02]);
  });
});

describe('buildStacks by token type', () => {
  const report = normalizeUsageReport(rawReport());

  test('tokens stack the non-empty token types with fixed colors', () => {
    const stacks = buildStacks(report, { metric: 'tokens', stackBy: 'tokenType', label });
    expect(stacks.series.map((series) => series.id)).toEqual([
      'uncached',
      'cacheRead',
      'cacheWrite5m',
      'cacheWrite1h',
      'output',
      'reasoning',
    ]);
    for (const series of stacks.series) {
      expect(series.colorIndex).toBe(TOKEN_TYPE_COLOR[series.id as keyof typeof TOKEN_TYPE_COLOR]);
    }
    // Day one: two groups of the default tokens; day three: one default group and devin
    // (no 1h writes).
    expect(stacks.series[1].values).toEqual([160000, 0, 160000]);
    expect(stacks.series[3].values).toEqual([2000, 0, 1000]);
  });

  test('cost stacks the six priced types and skips unpriced cells', () => {
    const stacks = buildStacks(report, { metric: 'cost', stackBy: 'tokenType', label });
    expect(stacks.series.map((series) => series.id)).not.toContain('unclassified');
    const cacheRead = stacks.series.find((series) => series.id === 'cacheRead');
    expect(cacheRead?.values).toEqual([0.08, 0, 0.04]);
  });

  test('requests have no token split and stack by group instead', () => {
    const stacks = buildStacks(report, { metric: 'requests', stackBy: 'tokenType', label });
    expect(stacks.series.map((series) => series.id)).toEqual([
      'claude-opus-5-5',
      'claude-sonnet-5',
      'devin/swe-1',
    ]);
  });
});

describe('niceTicks', () => {
  test('steps by 1, 2 or 5 times a power of ten and covers the max', () => {
    expect(niceTicks(78, 4)).toEqual([0, 20, 40, 60, 80]);
    expect(niceTicks(0.09, 4)).toEqual([0, 0.05, 0.1]);
    expect(niceTicks(1000, 5)).toEqual([0, 200, 400, 600, 800, 1000]);
    expect(niceTicks(0, 4)).toEqual([0, 1]);
  });
});

describe('buildUsageChartGeometry', () => {
  const size = { width: 960, height: 280 };

  test('stacked rects sum to each bucket total at the y scale', () => {
    const report = manyGroups();
    const stacks = buildStacks(report, { metric: 'cost', stackBy: 'group', label });
    const geometry = buildUsageChartGeometry(stacks, report.buckets, size);
    const yMax = geometry.yTicks[geometry.yTicks.length - 1].value;
    expect(yMax).toBe(80);
    stacks.totals.forEach((total, bucketIndex) => {
      const rects = geometry.rects.filter((rect) => rect.bucketIndex === bucketIndex);
      const height = rects.reduce((sum, rect) => sum + rect.height, 0);
      expect(height).toBeCloseTo((total / yMax) * size.height, 6);
      // Stacked bottom up, without overlap.
      const sorted = [...rects].sort((a, b) => b.y - a.y);
      sorted.forEach((rect, index) => {
        if (index === 0) expect(rect.y + rect.height).toBeCloseTo(size.height, 6);
        else expect(rect.y + rect.height).toBeCloseTo(sorted[index - 1].y, 6);
      });
    });
    // Day two has only model-0 and model-11 (other): two rects, no zero-height rects.
    expect(geometry.rects.filter((rect) => rect.bucketIndex === 1)).toHaveLength(2);
    expect(geometry.rects.every((rect) => rect.height > 0)).toBe(true);
  });

  test('bars are equal width with a gap, and columns tile the plot', () => {
    const report = manyGroups();
    const stacks = buildStacks(report, { metric: 'cost', stackBy: 'group', label });
    const geometry = buildUsageChartGeometry(stacks, report.buckets, size);
    const widths = new Set(geometry.rects.map((rect) => rect.width));
    expect(widths.size).toBe(1);
    expect([...widths][0]).toBeCloseTo(480 * 0.8, 6);
    expect(geometry.columns).toEqual([
      { x: 0, width: 480, bucketIndex: 0 },
      { x: 480, width: 480, bucketIndex: 1 },
    ]);
    expect(geometry.yTicks[0]).toEqual({ y: size.height, value: 0 });
  });

  test('a long hourly range shows at most twelve x labels', () => {
    const buckets = Array.from({ length: 744 }, (_, index) => ({
      start: `h${index}`,
      end: `h${index + 1}`,
      startMs: index,
      endMs: index + 1,
      groups: [],
    }));
    const geometry = buildUsageChartGeometry({ series: [], totals: [] }, buckets, size);
    expect(geometry.xLabels.length).toBeLessThanOrEqual(12);
    expect(geometry.xLabels[0].bucketIndex).toBe(0);
    expect(geometry.columns).toHaveLength(744);
    expect(geometry.rects).toEqual([]);
  });

  test('an empty report has no bars and a unit scale', () => {
    const geometry = buildUsageChartGeometry({ series: [], totals: [] }, [], size);
    expect(geometry.rects).toEqual([]);
    expect(geometry.columns).toEqual([]);
    expect(geometry.yTicks.map((tick) => tick.value)).toEqual([0, 1]);
  });
});

describe('buildTrend', () => {
  const HOUR = 3_600_000;
  const DAY = 24 * HOUR;
  const day = (index: number) => ({
    startMs: index * DAY,
    endMs: (index + 1) * DAY,
    groups: [],
  });
  const days = (count: number) => Array.from({ length: count }, (_, index) => day(index));
  const hours = (count: number) =>
    Array.from({ length: count }, (_, index) => ({
      startMs: index * HOUR,
      endMs: (index + 1) * HOUR,
      groups: [],
    }));
  const base = { unit: 'day', metric: 'tokens', asOfMs: null, recordingSinceMs: null } as const;

  test('windows are 24 hours, 7 days and 3 months', () => {
    expect(TREND_WINDOW).toEqual({ hour: 24, day: 7, month: 3 });
  });

  test('off returns no points and no slope', () => {
    const trend = buildTrend([1, 2, 3], days(3), { ...base, mode: 'off' });
    expect(trend).toEqual({ points: [null, null, null], slopePerBucket: null });
  });

  test('average uses a partial window at the start and a full 7-day window after', () => {
    const totals = [10, 20, 30, 40, 50, 60, 70, 80];
    const { points, slopePerBucket } = buildTrend(totals, days(8), { ...base, mode: 'average' });
    expect(points[0]).toBe(10);
    expect(points[1]).toBe(15);
    expect(points[6]).toBe(40);
    expect(points[7]).toBe(50);
    expect(slopePerBucket).toBeNull();
  });

  test('average window follows the bucket unit', () => {
    const totals = Array.from({ length: 25 }, (_, index) => (index === 0 ? 24 : 0));
    const { points } = buildTrend(totals, hours(25), { ...base, unit: 'hour', mode: 'average' });
    expect(points[23]).toBe(1);
    expect(points[24]).toBe(0);
  });

  test('the in-progress bucket is excluded and never enters a window', () => {
    const { points } = buildTrend([10, 20, 1000], days(3), {
      ...base,
      mode: 'average',
      asOfMs: 2.5 * DAY,
    });
    expect(points).toEqual([10, 15, null]);
  });

  test('buckets before recording started are excluded, not counted as zero', () => {
    const { points } = buildTrend([0, 0, 30, 50], days(4), {
      ...base,
      mode: 'average',
      recordingSinceMs: 2 * DAY,
    });
    expect(points).toEqual([null, null, 30, 40]);
  });

  test('a bucket that recording covers only in part is excluded', () => {
    const { points } = buildTrend([5, 50, 50], days(3), {
      ...base,
      mode: 'average',
      recordingSinceMs: 0.6 * DAY,
    });
    expect(points).toEqual([null, 50, 50]);
  });

  test('the average window spans bucket positions, so a skipped bucket does not pull in older ones', () => {
    const cell = (costUsd: number | null) => ({ costUsd }) as never;
    const buckets = days(9).map((bucket, index) => ({
      ...bucket,
      groups: [cell(index === 3 ? null : 1)],
    }));
    const totals = [1000, 1000, 10, 0, 10, 10, 10, 10, 10];
    const { points } = buildTrend(totals, buckets, { ...base, metric: 'cost', mode: 'average' });
    expect(points[3]).toBeNull();
    expect(points[8]).toBe(10);
  });

  test('cost skips buckets where no cell could be priced, other metrics keep them', () => {
    const cell = (costUsd: number | null) => ({ costUsd }) as never;
    const buckets = [
      { ...day(0), groups: [cell(null), cell(null)] },
      { ...day(1), groups: [cell(null), cell(4)] },
      { ...day(2), groups: [cell(8)] },
    ];
    const cost = buildTrend([0, 4, 8], buckets, { ...base, metric: 'cost', mode: 'average' });
    expect(cost.points).toEqual([null, 4, 6]);
    const requests = buildTrend([3, 3, 3], buckets, {
      ...base,
      metric: 'requests',
      mode: 'average',
    });
    expect(requests.points).toEqual([3, 3, 3]);
  });

  test('linear fits totals 1,2,3,4 with slope 1', () => {
    const { points, slopePerBucket } = buildTrend([1, 2, 3, 4], days(4), {
      ...base,
      mode: 'linear',
    });
    expect(slopePerBucket).toBeCloseTo(1, 10);
    points.forEach((point, index) => expect(point).toBeCloseTo(index + 1, 10));
  });

  test('linear extends across the in-progress bucket and starts at the first eligible one', () => {
    const { points } = buildTrend([0, 2, 4, 99], days(4), {
      ...base,
      mode: 'linear',
      recordingSinceMs: DAY,
      asOfMs: 3.5 * DAY,
    });
    expect(points[0]).toBeNull();
    expect(points[1]).toBeCloseTo(2, 10);
    expect(points[2]).toBeCloseTo(4, 10);
    expect(points[3]).toBeCloseTo(6, 10);
  });

  test('linear with fewer than two eligible buckets returns nulls', () => {
    const trend = buildTrend([5, 7], days(2), { ...base, mode: 'linear', asOfMs: 1.5 * DAY });
    expect(trend).toEqual({ points: [null, null], slopePerBucket: null });
  });
});

describe('buildUsageChartGeometry trend', () => {
  const buckets = Array.from({ length: 4 }, (_, index) => ({
    startMs: index * 1000,
    endMs: (index + 1) * 1000,
    groups: [],
  }));
  const stacks = { series: [], totals: [1, 1, 1, 1] };
  const size = { width: 400, height: 100 };

  test('the y scale grows to include the highest trend point', () => {
    const plain = buildUsageChartGeometry(stacks, buckets, size);
    const trend = { points: [1, 1, 1, 50], slopePerBucket: null };
    const withTrend = buildUsageChartGeometry(stacks, buckets, size, trend);
    expect(withTrend.yTicks[withTrend.yTicks.length - 1].value).toBeGreaterThanOrEqual(50);
    expect(plain.yTicks[plain.yTicks.length - 1].value).toBeLessThan(50);
    expect(withTrend.trendPath[0][3].y).toBeGreaterThanOrEqual(0);
  });

  test('segments break on null and sit at bucket centers', () => {
    const trend = { points: [1, null, 1, 1], slopePerBucket: null };
    const { trendPath } = buildUsageChartGeometry(stacks, buckets, size, trend);
    expect(trendPath.map((segment) => segment.map((point) => point.x))).toEqual([[50], [250, 350]]);
  });

  test('a line that falls below zero stops where it crosses the baseline', () => {
    const trend = { points: [10, -10, -30, null], slopePerBucket: null };
    const { trendPath, plot } = buildUsageChartGeometry(stacks, buckets, size, trend);
    expect(trendPath).toHaveLength(1);
    expect(trendPath[0]).toHaveLength(2);
    expect(trendPath[0][1]).toEqual({ x: 100, y: plot.bottom });
  });

  test('a line that rises from below zero starts where it crosses the baseline', () => {
    const trend = { points: [-10, 10, null, null], slopePerBucket: null };
    const { trendPath, plot } = buildUsageChartGeometry(stacks, buckets, size, trend);
    expect(trendPath).toHaveLength(1);
    expect(trendPath[0][0]).toEqual({ x: 100, y: plot.bottom });
    expect(trendPath[0][1].x).toBe(150);
  });

  test('no trend means no path', () => {
    expect(buildUsageChartGeometry(stacks, buckets, size).trendPath).toEqual([]);
  });
});
