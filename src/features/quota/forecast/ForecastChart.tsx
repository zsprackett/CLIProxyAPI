import { useMemo, useState, type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { ForecastChart as ForecastChartData } from '@/services/api/quotaForecast';
import { formatInstantShort } from '@/utils/quota';
import {
  buildForecastGeometry,
  chartPointAt,
  formatAccounts,
  type ForecastChartView,
} from './forecastModel';
import styles from './ForecastChart.module.scss';

/** Drawing units; the SVG stretches to the container and strokes do not scale. */
const WIDTH = 720;
const HEIGHT = 220;
const DAY_MS = 86_400_000;

export interface ForecastChartProps {
  chart: ForecastChartData;
  /** Accounts with data: the top of the y axis is never below this. */
  capacity: number;
  remainingAccounts: number;
  initialView?: ForecastChartView;
}

function tickLabel(ms: number, stepMs: number, locale?: string): string {
  const date = new Date(ms);
  if (stepMs >= DAY_MS)
    return date.toLocaleDateString(locale, { month: '2-digit', day: '2-digit' });
  if (stepMs * 6 <= DAY_MS) {
    return date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false });
  }
  return formatInstantShort(ms);
}

export function ForecastChart({
  chart,
  capacity,
  remainingAccounts,
  initialView = 'remaining',
}: ForecastChartProps) {
  const { t, i18n } = useTranslation();
  const [view, setView] = useState<ForecastChartView>(initialView);
  const [hoverMs, setHoverMs] = useState<number | null>(null);

  const geometry = useMemo(
    () => buildForecastGeometry(chart, view, { width: WIDTH, height: HEIGHT }, { capacity }),
    [capacity, chart, view]
  );
  const point = hoverMs === null ? null : chartPointAt(chart, hoverMs);
  const pct = (x: number) => `${(x / WIDTH) * 100}%`;

  const last = chart.projection.times.length - 1;
  const summary =
    last >= 0
      ? t('quota_management.forecast_chart_summary', {
          remaining: formatAccounts(remainingAccounts),
          total: capacity,
          end: formatAccounts(chart.projection.central[last]),
          low: formatAccounts(chart.projection.pessimistic[last]),
          high: formatAccounts(chart.projection.optimistic[last]),
          endAt: formatInstantShort(chart.projection.times[last]),
        })
      : t('quota_management.forecast_chart_summary_history', {
          remaining: formatAccounts(remainingAccounts),
          total: capacity,
        });
  const tickStep =
    geometry.xTicks.length > 1 ? geometry.xTicks[1].ms - geometry.xTicks[0].ms : DAY_MS;

  const handleMove = (event: MouseEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width <= 0) return;
    const fraction = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
    setHoverMs(chart.startMs + fraction * (chart.endMs - chart.startMs));
  };

  const hasDanger = geometry.dangerRects.length > 0;
  const hasPossible = geometry.dangerRects.some((rect) => rect.severity === 'possible');

  return (
    <figure className={styles.chart}>
      <div className={styles.head}>
        <figcaption className={styles.legend}>
          <span className={styles.legendItem}>
            <span className={`${styles.swatch} ${styles.swatchActual}`} aria-hidden="true" />
            {t('quota_management.forecast_legend_actual')}
          </span>
          <span className={styles.legendItem}>
            <span className={`${styles.swatch} ${styles.swatchProjection}`} aria-hidden="true" />
            {t('quota_management.forecast_legend_projection')}
          </span>
          <span className={styles.legendItem}>
            <span className={`${styles.swatch} ${styles.swatchBand}`} aria-hidden="true" />
            {t('quota_management.forecast_legend_band')}
          </span>
          {hasDanger && (
            <span className={styles.legendItem}>
              <span className={`${styles.swatch} ${styles.swatchDanger}`} aria-hidden="true" />
              {t('quota_management.forecast_legend_danger')}
            </span>
          )}
          {hasPossible && (
            <span className={styles.legendItem}>
              <span className={`${styles.swatch} ${styles.swatchPossible}`} aria-hidden="true" />
              {t('quota_management.forecast_legend_possible')}
            </span>
          )}
          {geometry.resetXs.length > 0 && (
            <span className={styles.legendItem}>
              <span className={`${styles.swatch} ${styles.swatchReset}`} aria-hidden="true" />
              {t('quota_management.forecast_legend_reset')}
            </span>
          )}
        </figcaption>
        <div
          className={styles.modes}
          role="group"
          aria-label={t('quota_management.forecast_view_label')}
        >
          {(['remaining', 'stacked'] as const).map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={view === value}
              onClick={() => setView(value)}
            >
              {t(`quota_management.forecast_view_${value}`)}
            </button>
          ))}
        </div>
      </div>

      <div className={styles.plot}>
        <div className={styles.yAxis} aria-hidden="true">
          {geometry.yTicks.map((tick) => (
            <span
              key={tick.value}
              className={styles.yTick}
              style={{ top: `${(tick.y / HEIGHT) * 100}%` }}
            >
              {tick.value}
            </span>
          ))}
        </div>

        <div
          className={styles.canvas}
          onMouseMove={handleMove}
          onMouseLeave={() => setHoverMs(null)}
        >
          <svg
            className={styles.svg}
            viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={summary}
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
            {geometry.dangerRects.map((rect) => (
              <rect
                key={`${rect.severity}:${rect.x}`}
                className={rect.severity === 'projected' ? styles.danger : styles.possible}
                data-severity={rect.severity}
                x={rect.x}
                y={0}
                width={rect.width}
                height={HEIGHT}
              />
            ))}
            {geometry.bandPath && <path className={styles.band} d={geometry.bandPath} />}
            {view === 'stacked' ? (
              <g className={styles.stack}>
                {geometry.stacked.map((area) => (
                  <path key={area.authId} className={styles.stackArea} d={area.path} />
                ))}
              </g>
            ) : (
              geometry.historyPath && <path className={styles.actual} d={geometry.historyPath} />
            )}
            {geometry.projectionPath && (
              <path className={styles.projection} d={geometry.projectionPath} />
            )}
            {geometry.resetXs.map((x) => (
              <line key={x} className={styles.reset} x1={x} x2={x} y1={HEIGHT - 14} y2={HEIGHT} />
            ))}
            <line className={styles.now} x1={geometry.nowX} x2={geometry.nowX} y1={0} y2={HEIGHT} />
            {point && (
              <line
                className={styles.hover}
                x1={geometry.xOf(point.ms)}
                x2={geometry.xOf(point.ms)}
                y1={0}
                y2={HEIGHT}
              />
            )}
          </svg>

          <span className={styles.nowLabel} style={{ left: pct(geometry.nowX) }} aria-hidden="true">
            {t('quota_management.forecast_now')}
          </span>

          {point && (
            <div
              className={styles.tooltip}
              role="status"
              style={{
                left: pct(geometry.xOf(point.ms)),
                transform:
                  geometry.xOf(point.ms) < WIDTH * 0.2
                    ? 'translateX(-10%)'
                    : geometry.xOf(point.ms) > WIDTH * 0.8
                      ? 'translateX(-90%)'
                      : 'translateX(-50%)',
              }}
            >
              <span className={styles.tooltipTime}>{formatInstantShort(point.ms)}</span>
              {point.projected ? (
                <>
                  <span className={styles.tooltipRow}>
                    {t('quota_management.forecast_tooltip_projected')}
                    <b>{formatAccounts(point.projected.central)}</b>
                  </span>
                  <span className={styles.tooltipRange}>
                    {t('quota_management.forecast_tooltip_range', {
                      low: formatAccounts(point.projected.pessimistic),
                      high: formatAccounts(point.projected.optimistic),
                    })}
                  </span>
                </>
              ) : (
                <span className={styles.tooltipRow}>
                  {t('quota_management.forecast_tooltip_actual')}
                  <b>{point.actual === null ? '—' : formatAccounts(point.actual)}</b>
                </span>
              )}
            </div>
          )}
        </div>
      </div>

      <div className={styles.xAxis} aria-hidden="true">
        {geometry.xTicks.map((tick) => (
          <span key={tick.ms} className={styles.xTick} style={{ left: pct(tick.x) }}>
            {tickLabel(tick.ms, tickStep, i18n.resolvedLanguage)}
          </span>
        ))}
      </div>
    </figure>
  );
}
