/**
 * Pure geometry for the stacked bar chart (hand-drawn SVG, like
 * forecastModel.buildForecastGeometry). No chart library.
 *
 * The model only assigns color indexes. The view maps index N to
 * `var(--usage-series-{N+1})` and OTHER_COLOR to `var(--usage-series-other)`
 * (styles/themes.scss). Token types have fixed indexes, so "1h writes" is the
 * same color on every view.
 */

import {
  USAGE_OTHER_GROUP,
  USAGE_PRICED_TYPES,
  USAGE_TOKEN_TYPES,
  type UsageBucket,
  type UsageReport,
  type UsageTokenType,
} from '@/services/api/usageHistory';
import { metricValue, type StackBy, type UsageMetric } from './usageModel';

export const MAX_SERIES = 8;
export const OTHER_COLOR = 8;
/** Series id of the chart's "other" stack: groups past MAX_SERIES plus the server's folded group. */
export const OTHER_SERIES = '__other__';

export const TOKEN_TYPE_COLOR: Record<UsageTokenType, number> = {
  uncached: 0,
  cacheRead: 1,
  cacheWrite5m: 2,
  cacheWrite1h: 3,
  output: 4,
  reasoning: 5,
  unclassified: 6,
};

export interface StackSeries {
  /** Group key, token type, or OTHER_SERIES. */
  id: string;
  label: string;
  colorIndex: number;
  /** One value per bucket, aligned with report.buckets. */
  values: number[];
}

export interface Stacks {
  series: StackSeries[];
  /** Per-bucket sum of all series. */
  totals: number[];
}

function groupStacks(
  report: UsageReport,
  metric: UsageMetric,
  label: (seriesId: string) => string
): StackSeries[] {
  const named = report.groups.filter((group) => group.key !== USAGE_OTHER_GROUP);
  const kept = named.slice(0, MAX_SERIES).map((group) => group.key);
  const keptSet = new Set(kept);
  const index = new Map(kept.map((key, position) => [key, position]));
  const values = kept.map(() => report.buckets.map(() => 0));
  const other = report.buckets.map(() => 0);
  report.buckets.forEach((bucket, bucketIndex) => {
    for (const group of bucket.groups) {
      const value = metricValue(group, metric);
      if (keptSet.has(group.key)) values[index.get(group.key)!][bucketIndex] += value;
      else other[bucketIndex] += value;
    }
  });
  return [
    ...kept.map((key, position) => ({
      id: key,
      label: label(key),
      colorIndex: position,
      values: values[position],
    })),
    { id: OTHER_SERIES, label: label(OTHER_SERIES), colorIndex: OTHER_COLOR, values: other },
  ];
}

function tokenTypeStacks(
  report: UsageReport,
  metric: 'tokens' | 'cost',
  label: (seriesId: string) => string
): StackSeries[] {
  const types: readonly UsageTokenType[] =
    metric === 'cost' ? USAGE_PRICED_TYPES : USAGE_TOKEN_TYPES;
  return types.map((type) => ({
    id: type,
    label: label(type),
    colorIndex: TOKEN_TYPE_COLOR[type],
    values: report.buckets.map((bucket) =>
      bucket.groups.reduce((sum, group) => {
        if (metric === 'tokens') return sum + group.tokens[type];
        const byType = group.costByType;
        return byType && type !== 'unclassified' ? sum + byType[type] : sum;
      }, 0)
    ),
  }));
}

/**
 * Report -> series.
 *  stackBy 'group': report.groups in order (cost desc), the first MAX_SERIES kept,
 *    the rest and the server's folded group summed into one OTHER_SERIES.
 *  stackBy 'tokenType': for tokens or cost only (requests have no split and stack
 *    by group). Cost has six types: unclassified tokens are never priced.
 * Series that are zero in every bucket are dropped.
 */
export function buildStacks(
  report: UsageReport,
  opts: {
    metric: UsageMetric;
    stackBy: StackBy;
    label: (seriesId: string) => string;
  }
): Stacks {
  const { metric, stackBy, label } = opts;
  const all =
    stackBy === 'tokenType' && metric !== 'requests'
      ? tokenTypeStacks(report, metric, label)
      : groupStacks(report, metric, label);
  const series = all.filter((item) => item.values.some((value) => value > 0));
  const totals = report.buckets.map((_, bucketIndex) =>
    series.reduce((sum, item) => sum + item.values[bucketIndex], 0)
  );
  return { series, totals };
}

export interface ChartSize {
  width: number;
  height: number;
}

export interface BarRect {
  x: number;
  y: number;
  width: number;
  height: number;
  seriesId: string;
  colorIndex: number;
  bucketIndex: number;
  value: number;
}

export interface UsageChartGeometry {
  plot: { left: number; top: number; right: number; bottom: number };
  rects: BarRect[];
  /** Nice ticks from 0 to the top of the scale. */
  yTicks: { y: number; value: number }[];
  /** At most 12 labels; every k-th bucket. */
  xLabels: { x: number; bucketIndex: number }[];
  /** Hit areas for the hover tooltip, one per bucket (full plot height). */
  columns: { x: number; width: number; bucketIndex: number }[];
}

const MAX_X_LABELS = 12;

const tidy = (value: number) => Number(value.toPrecision(12));

/** "Nice" ticks (step 1, 2 or 5 x 10^n) from 0 to the first tick at or above max. */
export function niceTicks(max: number, count: number): number[] {
  if (!(max > 0)) return [0, 1];
  const raw = max / Math.max(1, count);
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = ([1, 2, 5, 10].find((multiple) => multiple * magnitude >= raw) ?? 10) * magnitude;
  const steps = Math.ceil(tidy(max / step));
  return Array.from({ length: steps + 1 }, (_, index) => tidy(index * step));
}

/**
 * Stacks -> rects. Bars are equal width on a bucket-index axis, not a time
 * axis, so a 25-hour fall-back day is one more bar and DST never distorts the
 * chart. The gap is 20% of a slot (at least 1px once a slot is 5px wide).
 * Zero values produce no rect.
 */
export function buildUsageChartGeometry(
  stacks: Stacks,
  buckets: readonly UsageBucket[],
  size: ChartSize
): UsageChartGeometry {
  const { width, height } = size;
  const count = buckets.length;
  const ticks = niceTicks(Math.max(0, ...stacks.totals), 4);
  const yMax = ticks[ticks.length - 1];
  const scale = (value: number) => (value / yMax) * height;

  const slot = count > 0 ? width / count : width;
  const gap = slot >= 5 ? Math.max(1, slot * 0.2) : slot * 0.2;
  const barWidth = slot - gap;

  const rects: BarRect[] = [];
  for (let bucketIndex = 0; bucketIndex < count; bucketIndex += 1) {
    let base = height;
    for (const series of stacks.series) {
      const value = series.values[bucketIndex] ?? 0;
      if (!(value > 0)) continue;
      const barHeight = scale(value);
      base -= barHeight;
      rects.push({
        x: bucketIndex * slot + gap / 2,
        y: base,
        width: barWidth,
        height: barHeight,
        seriesId: series.id,
        colorIndex: series.colorIndex,
        bucketIndex,
        value,
      });
    }
  }

  const every = Math.max(1, Math.ceil(count / MAX_X_LABELS));
  const xLabels: UsageChartGeometry['xLabels'] = [];
  for (let bucketIndex = 0; bucketIndex < count; bucketIndex += every) {
    xLabels.push({ x: bucketIndex * slot + slot / 2, bucketIndex });
  }

  return {
    plot: { left: 0, top: 0, right: width, bottom: height },
    rects,
    yTicks: ticks.map((value) => ({ y: height - scale(value), value })),
    xLabels,
    columns: buckets.map((_, bucketIndex) => ({ x: bucketIndex * slot, width: slot, bucketIndex })),
  };
}
