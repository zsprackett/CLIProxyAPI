import { useTranslation } from 'react-i18next';
import { getTypeLabel } from '@/features/authFiles/constants';
import type { ForecastAlert } from '@/services/api/quotaForecast';
import { forecastClassLabel, formatAccounts, formatForecastDuration } from './forecastModel';
import styles from './ForecastAlerts.module.scss';

export interface ForecastAlertsProps {
  alerts: ForecastAlert[];
  nowMs: number;
}

/** Firing alerts are announced; resolved ones stay as a quiet record. */
export function ForecastAlerts({ alerts, nowMs }: ForecastAlertsProps) {
  const { t } = useTranslation();
  if (alerts.length === 0) return null;
  const firing = alerts.filter((alert) => alert.state === 'firing');
  const resolved = alerts.filter((alert) => alert.state !== 'firing');
  const name = (alert: ForecastAlert) => ({
    provider: getTypeLabel(t, alert.provider),
    window: forecastClassLabel(t, alert.class),
  });

  return (
    <div
      className={styles.alerts}
      role="group"
      aria-label={t('quota_management.forecast_alerts_aria')}
    >
      {firing.length > 0 && (
        <div className={styles.firing} role="alert">
          {firing.map((alert) => (
            <p key={alert.class} className={styles.line}>
              {t('quota_management.forecast_alert_firing', {
                ...name(alert),
                exhausts: formatForecastDuration((alert.exhaustsAtMs ?? nowMs) - nowMs, t),
                accounts: formatAccounts(alert.shortAccounts),
              })}
            </p>
          ))}
        </div>
      )}
      {resolved.map((alert) => (
        <p key={alert.class} className={`${styles.line} ${styles.resolved}`}>
          {t('quota_management.forecast_alert_resolved', name(alert))}
        </p>
      ))}
    </div>
  );
}
