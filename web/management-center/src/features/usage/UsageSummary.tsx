import { useTranslation } from 'react-i18next';
import type { UsageReport } from '@/services/api/usageHistory';
import { formatPercent, formatTokens, formatUsd, summarize } from './usageModel';
import styles from './UsageView.module.scss';

export interface UsageSummaryProps {
  report: UsageReport;
  locale: string;
}

export function UsageSummary({ report, locale }: UsageSummaryProps) {
  const { t } = useTranslation();
  const summary = summarize(report.totals);
  const cost =
    summary.costUsd === null
      ? t('usage.summary_cost_unpriced')
      : summary.costPartial
        ? t('usage.summary_cost_at_least', { value: formatUsd(summary.costUsd, locale) })
        : formatUsd(summary.costUsd, locale);

  return (
    <div className={styles.tiles}>
      <div className={styles.tile}>
        <span className={styles.tileLabel}>{t('usage.summary_cost')}</span>
        <span className={styles.tileValue}>{cost}</span>
        <span className={styles.tileHint}>{t('usage.summary_estimate')}</span>
      </div>
      <div className={styles.tile}>
        <span className={styles.tileLabel}>{t('usage.summary_tokens')}</span>
        <span className={styles.tileValue}>{formatTokens(summary.totalTokens, locale)}</span>
      </div>
      <div className={styles.tile}>
        <span className={styles.tileLabel}>{t('usage.summary_requests')}</span>
        <span className={styles.tileValue}>
          {new Intl.NumberFormat(locale).format(summary.requests)}
        </span>
        <span className={styles.tileHint}>
          {t('usage.summary_failure_rate', { rate: formatPercent(summary.failureRate, locale) })}
        </span>
      </div>
      <div className={styles.tile}>
        <span className={styles.tileLabel}>{t('usage.summary_cache_read')}</span>
        <span className={styles.tileValue}>{formatPercent(summary.cacheReadShare, locale)}</span>
        <span className={styles.tileHint}>{t('usage.summary_cache_read_hint')}</span>
      </div>
      <div className={styles.tile}>
        <span className={styles.tileLabel}>{t('usage.summary_write_1h')}</span>
        <span className={styles.tileValue}>{formatPercent(summary.write1hShare, locale)}</span>
        <span className={styles.tileHint}>
          {t('usage.summary_write_1h_hint', {
            cost: formatUsd(summary.cacheWriteCostUsd, locale),
          })}
        </span>
      </div>
    </div>
  );
}
