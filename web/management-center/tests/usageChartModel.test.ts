import { describe, expect, test } from 'bun:test';
import {
  MAX_SERIES,
  OTHER_COLOR,
  OTHER_SERIES,
  TOKEN_TYPE_COLOR,
  buildStacks,
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
