import { useMemo, useState, type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { UsageReport } from '@/services/api/usageHistory';
import {
  OTHER_COLOR,
  buildStacks,
  buildUsageChartGeometry,
  type StackSeries,
} from './usageChartModel';
import { bucketLabels, formatTokens, formatUsd, type UsageControls } from './usageModel';
import styles from './UsageChart.module.scss';

/** Drawing units; the SVG stretches to its container. */
const WIDTH = 960;
const HEIGHT = 280;

function seriesColor(colorIndex: number): string {
  return colorIndex === OTHER_COLOR
    ? 'var(--usage-series-other)'
    : `var(--usage-series-${colorIndex + 1})`;
}

function formatMetric(
  value: number,
  metric: UsageControls['metric'],
  locale: string
): string {
  if (metric === 'cost') return formatUsd(value, locale);
  if (metric === 'tokens') return formatTokens(value, locale);
  return new Intl.NumberFormat(locale).format(value);
}

export interface UsageChartProps {
  report: UsageReport;
  metric: UsageControls['metric'];
  stackBy: UsageControls['stackBy'];
  seriesLabel: (seriesId: string) => string;
  locale: string;
}

export function UsageChart({ report, metric, stackBy, seriesLabel, locale }: UsageChartProps) {
  const { t } = useTranslation();
  const [hover, setHover] = useState<number | null>(null);

  const stacks = useMemo(
    () => buildStacks(report, { metric, stackBy, label: seriesLabel }),
    [metric, report, seriesLabel, stackBy]
  );
  const geometry = useMemo(
    () => buildUsageChartGeometry(stacks, report.buckets, { width: WIDTH, height: HEIGHT }),
    [report.buckets, stacks]
  );
  const labels = useMemo(
    () => bucketLabels(report.buckets, report.query.bucket, locale),
    [locale, report.buckets, report.query.bucket]
  );
  const pct = (x: number) => `${(x / WIDTH) * 100}%`;
  const total = stacks.totals.reduce((sum, value) => sum + value, 0);

  const handleMove = (event: MouseEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const count = report.buckets.length;
    if (rect.width <= 0 || count === 0) return;
    const fraction = Math.min(0.9999, Math.max(0, (event.clientX - rect.left) / rect.width));
    setHover(Math.floor(fraction * count));
  };

  const hovered: StackSeries[] =
    hover === null ? [] : stacks.series.filter((series) => series.values[hover] > 0);
  const column = hover === null ? null : geometry.columns[hover];
  const columnCenter = column ? column.x + column.width / 2 : 0;

  return (
    <figure className={styles.chart}>
      <figcaption className={styles.legend}>
        {stacks.series.map((series) => (
          <span key={series.id} className={styles.legendItem}>
            <span
              className={styles.swatch}
              style={{ background: seriesColor(series.colorIndex) }}
              aria-hidden="true"
            />
            {series.label}
          </span>
        ))}
      </figcaption>

      <div className={styles.plot}>
        <div className={styles.yAxis} aria-hidden="true">
          {geometry.yTicks.map((tick) => (
            <span
              key={tick.value}
              className={styles.yTick}
              style={{ top: `${(tick.y / HEIGHT) * 100}%` }}
            >
              {formatMetric(tick.value, metric, locale)}
            </span>
          ))}
        </div>

        <div className={styles.canvas} onMouseMove={handleMove} onMouseLeave={() => setHover(null)}>
          <svg
            className={styles.svg}
            viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={t('usage.chart_summary', {
              metric: t(`usage.metric_${metric}`),
              unit: t(`usage.unit_${report.query.bucket}`),
              count: report.buckets.length,
              total: formatMetric(total, metric, locale),
            })}
          >
            {geometry.yTicks.map((tick) => (
              <line
                key={tick.value}
                className={styles.gridline}
                x1={0}
                x2={WIDTH}
                y1={tick.y}
                y2={tick.y}
              />
            ))}
            {column && (
              <rect
                className={styles.hoverColumn}
                x={column.x}
                y={0}
                width={column.width}
                height={HEIGHT}
              />
            )}
            {geometry.rects.map((rect) => (
              <rect
                key={`${rect.seriesId}:${rect.bucketIndex}`}
                className={styles.bar}
                data-series={rect.seriesId}
                x={rect.x}
                y={rect.y}
                width={rect.width}
                height={rect.height}
                style={{ fill: seriesColor(rect.colorIndex) }}
              />
            ))}
          </svg>

          {hover !== null && (
            <div
              className={styles.tooltip}
              role="status"
              style={{
                left: pct(columnCenter),
                transform:
                  columnCenter < WIDTH * 0.2
                    ? 'translateX(-10%)'
                    : columnCenter > WIDTH * 0.8
                      ? 'translateX(-90%)'
                      : 'translateX(-50%)',
              }}
            >
              <span className={styles.tooltipTime}>{labels[hover]}</span>
              {hovered.map((series) => (
                <span key={series.id} className={styles.tooltipRow}>
                  <span
                    className={styles.swatch}
                    style={{ background: seriesColor(series.colorIndex) }}
                    aria-hidden="true"
                  />
                  {series.label}
                  <b>{formatMetric(series.values[hover], metric, locale)}</b>
                </span>
              ))}
              <span className={`${styles.tooltipRow} ${styles.tooltipTotal}`}>
                {t('usage.chart_total')}
                <b>{formatMetric(stacks.totals[hover] ?? 0, metric, locale)}</b>
              </span>
            </div>
          )}
        </div>
      </div>

      <div className={styles.xAxis} aria-hidden="true">
        {geometry.xLabels.map((label) => (
          <span key={label.bucketIndex} className={styles.xTick} style={{ left: pct(label.x) }}>
            {labels[label.bucketIndex]}
          </span>
        ))}
      </div>
    </figure>
  );
}
