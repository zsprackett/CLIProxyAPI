import { useTranslation } from 'react-i18next';
import type { UsageReport } from '@/services/api/usageHistory';
import styles from './UsageView.module.scss';

export interface UsageFootnoteProps {
  report: UsageReport;
  locale: string;
}

const formatTime = (ms: number, locale: string) =>
  new Date(ms).toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' });

const formatDate = (ms: number, locale: string) =>
  new Date(ms).toLocaleDateString(locale, { dateStyle: 'medium' });

/** The estimate line is always shown: most traffic is subscription OAuth, not API billing. */
export function UsageFootnote({ report, locale }: UsageFootnoteProps) {
  const { t } = useTranslation();
  const { prices } = report;
  return (
    <div className={styles.footnote}>
      <p className={styles.estimate}>{t('usage.footnote_estimate')}</p>
      {report.unpricedModels.length > 0 ? (
        <p>{t('usage.footnote_unpriced', { models: report.unpricedModels.join(', ') })}</p>
      ) : null}
      {report.foldedGroups > 0 ? (
        <p>{t('usage.footnote_folded', { n: report.foldedGroups })}</p>
      ) : null}
      {report.query.clamped ? (
        <p>{t('usage.footnote_clamped', { days: report.retentionDays })}</p>
      ) : null}
      <p>
        {t('usage.footnote_prices', {
          source: t(`usage.source_${prices.source}`),
          date: prices.snapshotDate || '-',
        })}
        {prices.fetchedAtMs !== null
          ? ` ${t('usage.footnote_prices_fetched', { time: formatTime(prices.fetchedAtMs, locale) })}`
          : null}
        {prices.lastError
          ? ` ${t('usage.footnote_prices_error', { error: prices.lastError })}`
          : null}
      </p>
      <p>
        {report.query.tz ? t('usage.footnote_tz', { tz: report.query.tz }) : null}
        {report.asOfMs !== null
          ? ` ${t('usage.footnote_as_of', { time: formatTime(report.asOfMs, locale) })}`
          : null}
        {report.recordingSinceMs !== null
          ? ` ${t('usage.footnote_recording_since', {
              date: formatDate(report.recordingSinceMs, locale),
            })}`
          : null}
      </p>
    </div>
  );
}
