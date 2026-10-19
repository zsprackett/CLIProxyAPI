import { useMemo, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import type { ForecastHourlyBurn } from '@/services/api/quotaForecast';
import { buildHeatmap, formatAccounts, type TzOffsetMinutes } from './forecastModel';
import styles from './BurnHeatmap.module.scss';

export interface BurnHeatmapProps {
  hourlyBurn: ForecastHourlyBurn[];
  tzOffsetMinutes?: TzOffsetMinutes;
}

const HOURS = Array.from({ length: 24 }, (_, hour) => hour);

/** Weekday names from a known Sunday (2026-01-04), so any locale labels itself. */
function weekdayNames(locale?: string): string[] {
  let format: Intl.DateTimeFormat;
  try {
    format = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' });
  } catch {
    format = new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' });
  }
  return Array.from({ length: 7 }, (_, day) => format.format(Date.UTC(2026, 0, 4 + day)));
}

/** Average burn per covered hour, binned by local weekday × hour. */
export function BurnHeatmap({ hourlyBurn, tzOffsetMinutes }: BurnHeatmapProps) {
  const { t, i18n } = useTranslation();
  const heatmap = useMemo(
    () => buildHeatmap(hourlyBurn, tzOffsetMinutes),
    [hourlyBurn, tzOffsetMinutes]
  );
  const days = useMemo(() => weekdayNames(i18n.resolvedLanguage), [i18n.resolvedLanguage]);

  return (
    <section className={styles.heatmap} aria-label={t('quota_management.forecast_heatmap_title')}>
      <div className={styles.head}>
        <h3 className={styles.title}>{t('quota_management.forecast_heatmap_title')}</h3>
        <span className={styles.hint}>{t('quota_management.forecast_heatmap_hint')}</span>
      </div>
      <div className={styles.scroller}>
        <div className={styles.grid} role="table">
          <div className={styles.row} role="row">
            <span className={styles.corner} role="columnheader" />
            {HOURS.map((hour) => (
              <span key={hour} className={styles.hour} role="columnheader">
                {hour % 3 === 0 ? hour : ''}
              </span>
            ))}
          </div>
          {heatmap.cells.map((row, day) => (
            <div key={day} className={styles.row} role="row">
              <span className={styles.day} role="rowheader">
                {days[day]}
              </span>
              {row.map((cell, hour) => {
                const label =
                  cell.mean === null
                    ? t('quota_management.forecast_heatmap_cell_empty', { day: days[day], hour })
                    : t('quota_management.forecast_heatmap_cell', {
                        day: days[day],
                        hour,
                        value: formatAccounts(cell.mean),
                        samples: cell.samples,
                      });
                const intensity =
                  cell.mean === null || heatmap.max <= 0 ? 0 : cell.mean / heatmap.max;
                return (
                  <span
                    key={hour}
                    role="cell"
                    className={`${styles.cell} ${cell.mean === null ? styles.cellEmpty : ''}`}
                    style={{ '--intensity': `${Math.round(intensity * 100)}%` } as CSSProperties}
                    title={label}
                    aria-label={label}
                  />
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
