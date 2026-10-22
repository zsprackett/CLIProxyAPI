/**
 * Pure geometry for the stacked bar chart (hand-drawn SVG, like
 * forecastModel.buildForecastGeometry). No chart library.
 *
 * Colors: there is no categorical palette, so this adds eight theme tokens
 * --usage-series-1..8 plus --usage-series-other to styles/themes.scss (light,
 * white and dark blocks). The model only assigns indexes; the View maps an index
 * to `var(--usage-series-N)`. Token types get fixed indexes so "1h writes" is the
 * same color on every page view.
 */

import type { UsageBucket, UsageReport, UsageTokenType } from '@/services/api/usageHistory';
import type { StackBy, UsageMetric } from './usageModel';

export const MAX_SERIES = 8;
export const OTHER_COLOR = 8;

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
  /** Group key, token type, or "__other__". */
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

/**
 * Report -> series.
 *  stackBy 'group': series = report.groups in order (cost desc), first MAX_SERIES kept,
 *    the rest folded into one "other" series. values = metricValue(bucket cell, metric).
 *  stackBy 'tokenType': only for metric tokens or cost. series = the seven token types
 *    (cost: six, no unclassified), values summed over all groups of the bucket from
 *    tokens / costByType. Empty series (all zeros) are dropped.
 */
export function buildStacks(
  report: UsageReport,
  opts: {
    metric: UsageMetric;
    stackBy: StackBy;
    label: (seriesId: string) => string;
  }
): Stacks {
  throw new Error('not implemented');
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
  /** Nice ticks from 0 to the max bucket total. */
  yTicks: { y: number; value: number }[];
  /** At most ~12 labels; every k-th bucket. */
  xLabels: { x: number; bucketIndex: number }[];
  /** Hit areas for the hover tooltip, one per bucket (full plot height). */
  columns: { x: number; width: number; bucketIndex: number }[];
}

/**
 * Stacks -> rects. Bars are equal width (bucket index axis, not time axis), so a
 * 25-hour fall-back day is just one more bar and DST never distorts the chart.
 * Gap between bars = 20% of slot, min 1px. Zero values produce no rect.
 */
export function buildUsageChartGeometry(
  stacks: Stacks,
  buckets: readonly UsageBucket[],
  size: ChartSize
): UsageChartGeometry {
  throw new Error('not implemented');
}

/** "Nice" tick step (1, 2, 5 x 10^n) for a max value and ~5 ticks. */
export function niceTicks(max: number, count: number): number[] {
  throw new Error('not implemented');
}
