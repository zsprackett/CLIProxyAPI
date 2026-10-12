import { useTranslation } from 'react-i18next';
import type { ForecastPool } from '@/services/api/quotaForecast';
import type { ResolvedTheme } from '@/types';
import { ProviderMark } from '../components/ProviderMark';
import {
  describeRunway,
  forecastClassLabel,
  formatAccounts,
  formatRunway,
  runwayTone,
} from './forecastModel';
import styles from './ForecastSummaryList.module.scss';

export interface ForecastSummaryListProps {
  pools: ForecastPool[];
  nowMs: number;
  resolvedTheme: ResolvedTheme;
}

/** All tab: one compact runway line per pooled quota window. */
export function ForecastSummaryList({ pools, nowMs, resolvedTheme }: ForecastSummaryListProps) {
  const { t } = useTranslation();
  if (pools.length === 0) return null;

  return (
    <ul className={styles.list} aria-label={t('quota_management.forecast_all_aria')}>
      {pools.map((pool) => {
        const runway = describeRunway(pool, nowMs);
        return (
          <li key={pool.class} className={styles.row}>
            <span className={styles.pool}>
              <ProviderMark provider={pool.provider} resolvedTheme={resolvedTheme} />
              <span className={styles.window}>{forecastClassLabel(t, pool.class)}</span>
            </span>
            <span className={styles.left}>
              {t('quota_management.forecast_accounts_left', {
                remaining: formatAccounts(pool.remainingAccounts),
                total: pool.accountsWithData,
              })}
            </span>
            <span className={styles.runway}>
              <span className={`${styles.dot} ${styles[runwayTone(runway)]}`} aria-hidden="true" />
              {formatRunway(runway, t)}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
