import { useTranslation } from 'react-i18next';
import type { ForecastAccount, ForecastPace } from '@/services/api/quotaForecast';
import { maskIdentity } from '../ledgerModel';
import { remainingTone } from '../components/ledgerTone';
import { describePace } from './forecastModel';
import styles from './PaceGauge.module.scss';

export interface PaceGaugeProps {
  pace: ForecastPace | null;
  accounts: ForecastAccount[];
  showIdentities: boolean;
}

const clampPercent = (value: number) => Math.min(100, Math.max(0, value));

function PaceLabel({ deltaPp }: { deltaPp: number }) {
  const { t } = useTranslation();
  const pace = describePace(deltaPp);
  return (
    <span className={`${styles.delta} ${pace.kind === 'over' ? styles.over : ''}`}>
      {t(`quota_management.forecast_pace_${pace.kind}`, { points: pace.points })}
    </span>
  );
}

/** Used share against the share of the window already elapsed, pooled and per credential. */
export function PaceGauge({ pace, accounts, showIdentities }: PaceGaugeProps) {
  const { t } = useTranslation();
  if (!pace && accounts.length === 0) return null;

  return (
    <section className={styles.pace} aria-label={t('quota_management.forecast_pace_title')}>
      <h3 className={styles.title}>{t('quota_management.forecast_pace_title')}</h3>
      {pace && (
        <div className={styles.pooled}>
          <div
            className={styles.bar}
            role="img"
            aria-label={t('quota_management.forecast_pace_summary', {
              used: Math.round(pace.usedPercent),
              expected: Math.round(pace.expectedPercent),
            })}
          >
            <span
              className={`${styles.fill} ${styles[remainingTone(100 - pace.usedPercent)]}`}
              style={{ width: `${clampPercent(pace.usedPercent)}%` }}
            />
            <span
              className={styles.marker}
              style={{ left: `${clampPercent(pace.expectedPercent)}%` }}
              title={t('quota_management.forecast_pace_elapsed')}
            />
          </div>
          <div className={styles.caption}>
            <PaceLabel deltaPp={pace.deltaPp} />
            <span>
              {t('quota_management.forecast_pace_summary', {
                used: Math.round(pace.usedPercent),
                expected: Math.round(pace.expectedPercent),
              })}
            </span>
          </div>
        </div>
      )}

      {accounts.length > 0 && (
        <ul className={styles.accounts} aria-label={t('quota_management.forecast_pace_accounts')}>
          {accounts.map((account) => {
            const name = showIdentities ? account.name : maskIdentity(account.name);
            const elapsed =
              account.elapsedFraction === null ? null : clampPercent(account.elapsedFraction * 100);
            return (
              <li key={account.authId} className={styles.account}>
                <span className={styles.name} title={name}>
                  {name}
                </span>
                <span
                  className={styles.miniBar}
                  role="img"
                  aria-label={t('quota_management.forecast_pace_account_aria', {
                    name,
                    used: Math.round(account.usedPercent),
                    elapsed: elapsed === null ? '—' : Math.round(elapsed),
                  })}
                >
                  <span
                    className={`${styles.fill} ${styles[remainingTone(account.remainingPercent)]}`}
                    style={{ width: `${clampPercent(account.usedPercent)}%` }}
                  />
                  {elapsed !== null && (
                    <span className={styles.marker} style={{ left: `${elapsed}%` }} />
                  )}
                </span>
                <span className={styles.accountMeta}>
                  {account.paceDeltaPp !== null && <PaceLabel deltaPp={account.paceDeltaPp} />}
                  {account.projectedUsedAtReset !== null && account.projectedUsedAtReset > 100 && (
                    <span className={styles.runsOut}>
                      {t('quota_management.forecast_pace_runs_out')}
                    </span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
