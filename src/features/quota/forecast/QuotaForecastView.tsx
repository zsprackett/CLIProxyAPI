import { useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { EmptyState } from '@/components/ui/EmptyState';
import { Skeleton } from '@/components/ui/Skeleton';
import type { QuotaForecast, QuotaForecastProvider } from '@/services/api/quotaForecast';
import type { ResolvedTheme } from '@/types';
import { formatInstantShort } from '@/utils/quota';
import { AttributionTable } from './AttributionTable';
import { BurnHeatmap } from './BurnHeatmap';
import { ForecastAlerts } from './ForecastAlerts';
import { ForecastChart } from './ForecastChart';
import { ForecastHeadline } from './ForecastHeadline';
import { ForecastSummaryList } from './ForecastSummaryList';
import { PaceGauge } from './PaceGauge';
import {
  forecastClassLabel,
  pickDefaultClass,
  resolveClientLabels,
  type ForecastChartView,
} from './forecastModel';
import styles from './QuotaForecast.module.scss';

export interface QuotaForecastViewProps {
  /** Null on the All tab: summaries for every pool, no detail. */
  provider: QuotaForecastProvider | null;
  data: QuotaForecast | null;
  loading: boolean;
  error: string;
  nowMs: number;
  showIdentities: boolean;
  resolvedTheme: ResolvedTheme;
  /** Configured client keys and their saved names, used only to label attribution rows. */
  apiKeys?: readonly string[];
  apiKeyNames?: Record<string, string>;
  apiBase?: string;
  initialChartView?: ForecastChartView;
}

const NO_KEYS: readonly string[] = [];
const NO_NAMES: Record<string, string> = {};

export function QuotaForecastView({
  provider,
  data,
  loading,
  error,
  nowMs,
  showIdentities,
  resolvedTheme,
  apiKeys = NO_KEYS,
  apiKeyNames = NO_NAMES,
  apiBase = '',
  initialChartView,
}: QuotaForecastViewProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const [chosenClass, setChosenClass] = useState<string | null>(null);

  const pools = useMemo(
    () => (data?.pools ?? []).filter((pool) => !provider || pool.provider === provider),
    [data, provider]
  );
  const pool =
    pools.find((item) => item.class === chosenClass) ??
    pools.find((item) => item.class === pickDefaultClass(pools)) ??
    null;
  const clients = pool?.attribution?.clients;
  const clientLabels = useMemo(
    () => resolveClientLabels(clients ?? [], apiKeys, apiKeyNames, apiBase),
    [apiBase, apiKeyNames, apiKeys, clients]
  );

  const header = (
    <h2 id={titleId} className={styles.title}>
      {t('quota_management.forecast_title')}
    </h2>
  );

  if (!data) {
    if (loading) {
      return (
        <section className={styles.forecast} aria-labelledby={titleId} aria-busy="true">
          {header}
          <Skeleton height={112} rounded={14} />
        </section>
      );
    }
    if (!error) return null;
    return (
      <section className={styles.forecast} aria-labelledby={titleId}>
        {header}
        <div className={styles.error} role="alert">
          {t('quota_management.forecast_error', { message: error })}
        </div>
      </section>
    );
  }

  if (!data.enabled) {
    const reason =
      data.reason === 'home_mode' || data.reason === 'unavailable' ? data.reason : 'disabled';
    return (
      <section className={styles.forecast} aria-labelledby={titleId}>
        {header}
        <EmptyState
          title={t('quota_management.forecast_disabled_title')}
          description={t(`quota_management.forecast_${reason}_desc`)}
        />
      </section>
    );
  }

  const alerts = data.alerts.filter((alert) => !provider || alert.provider === provider);

  return (
    <section className={styles.forecast} aria-labelledby={titleId}>
      <header className={styles.head}>
        <div>
          {header}
          <p className={styles.meta}>
            {data.recordingSinceMs !== null &&
              t('quota_management.forecast_recording_since', {
                date: formatInstantShort(data.recordingSinceMs),
              })}
            {data.recordingSinceMs !== null && data.lookbackHours > 0 && ' · '}
            {data.lookbackHours > 0 &&
              t('quota_management.forecast_lookback', { hours: data.lookbackHours })}
          </p>
        </div>
        {provider && pools.length > 1 && (
          <div
            className={styles.modes}
            role="group"
            aria-label={t('quota_management.forecast_class_label')}
          >
            {pools.map((item) => (
              <button
                key={item.class}
                type="button"
                aria-pressed={item.class === pool?.class}
                onClick={() => setChosenClass(item.class)}
              >
                {forecastClassLabel(t, item.class)}
              </button>
            ))}
          </div>
        )}
      </header>

      {!data.quotaPollEnabled && (
        <p className={styles.hint}>{t('quota_management.forecast_poll_hint')}</p>
      )}

      <ForecastAlerts alerts={alerts} nowMs={nowMs} />

      {pools.length === 0 ? (
        <p className={styles.empty}>{t('quota_management.forecast_no_pools')}</p>
      ) : !provider ? (
        <ForecastSummaryList pools={pools} nowMs={nowMs} resolvedTheme={resolvedTheme} />
      ) : (
        pool && (
          <>
            <ForecastHeadline pool={pool} nowMs={nowMs} />
            {pool.chart && (
              <div className={styles.panel}>
                <ForecastChart
                  chart={pool.chart}
                  capacity={pool.accountsWithData}
                  remainingAccounts={pool.remainingAccounts}
                  initialView={initialChartView}
                />
              </div>
            )}
            <div className={styles.columns}>
              {(pool.pace || pool.accounts.length > 0) && (
                <div className={styles.panel}>
                  <PaceGauge
                    pace={pool.pace}
                    accounts={pool.accounts}
                    showIdentities={showIdentities}
                  />
                </div>
              )}
              {pool.hourlyBurn.length > 0 && (
                <div className={styles.panel}>
                  <BurnHeatmap hourlyBurn={pool.hourlyBurn} />
                </div>
              )}
            </div>
            {pool.attribution && (
              <div className={styles.panel}>
                <AttributionTable attribution={pool.attribution} clientLabels={clientLabels} />
              </div>
            )}
          </>
        )
      )}
    </section>
  );
}
