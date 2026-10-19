import { useTranslation } from 'react-i18next';
import type { ForecastPool } from '@/services/api/quotaForecast';
import {
  burnDisplay,
  describeRunway,
  formatAccounts,
  formatForecastDuration,
  formatRunway,
  runwayTone,
} from './forecastModel';
import styles from './ForecastHeadline.module.scss';

export interface ForecastHeadlineProps {
  pool: ForecastPool;
  nowMs: number;
}

/** Three tiles: how much is left, how fast it goes, and whether it lasts to the reset. */
export function ForecastHeadline({ pool, nowMs }: ForecastHeadlineProps) {
  const { t } = useTranslation();
  const burn = burnDisplay(pool);
  const runway = describeRunway(pool, nowMs);
  const perUnit = (rate: number) =>
    t(
      burn.unit === 'hour'
        ? 'quota_management.forecast_burn_per_hour'
        : 'quota_management.forecast_burn_per_day',
      { rate: formatAccounts(rate) }
    );

  return (
    <div className={styles.tiles}>
      <article className={styles.tile}>
        <span className={styles.label}>{t('quota_management.forecast_accounts_left_label')}</span>
        <span className={styles.figure}>
          {formatAccounts(pool.remainingAccounts)}
          <span className={styles.figureOf}>
            {' '}
            {t('quota_management.forecast_accounts_of', { total: pool.accountsWithData })}
          </span>
        </span>
        <span className={styles.detail}>
          {pool.nextResetAtMs === null
            ? t('quota_management.forecast_reset_unknown')
            : t('quota_management.forecast_reset_in', {
                duration: formatForecastDuration(pool.nextResetAtMs - nowMs, t),
              })}
        </span>
      </article>

      <article className={styles.tile}>
        <span className={styles.label}>{t('quota_management.forecast_burn_label')}</span>
        {pool.burn.sufficient ? (
          <>
            <span className={styles.figure}>{perUnit(burn.rate)}</span>
            <span className={styles.detail}>
              {t('quota_management.forecast_burn_range', {
                low: formatAccounts(burn.low),
                high: formatAccounts(burn.high),
              })}
            </span>
          </>
        ) : (
          <>
            <span className={`${styles.figure} ${styles.muted}`}>
              {t('quota_management.forecast_collecting')}
            </span>
            <span className={styles.detail}>{t('quota_management.forecast_collecting_hint')}</span>
          </>
        )}
      </article>

      <article className={styles.tile}>
        <span className={styles.label}>{t('quota_management.forecast_runway_label')}</span>
        <span className={`${styles.status} ${styles[runwayTone(runway)]}`}>
          {t(`quota_management.forecast_status_${runway.kind}`)}
        </span>
        <span className={styles.detail}>{formatRunway(runway, t)}</span>
      </article>
    </div>
  );
}
