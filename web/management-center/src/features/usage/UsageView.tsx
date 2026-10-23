/**
 * Prop-driven Usage page. No hooks beyond useTranslation and the chart's hover
 * state; tested with renderToStaticMarkup over fixture reports.
 *
 *   UsageView
 *   ├─ disabled / error / skeleton gate
 *   ├─ UsageControls     range, bucket, group-by, five filters, metric, stack
 *   ├─ UsageSummary      cost ("at least" when partial), tokens, requests, cache shares
 *   ├─ UsageChart        stacked SVG bars with legend and hover tooltip
 *   ├─ UsageGroupTable   one row per group plus the total
 *   ├─ UsageFootnote     estimate line, unpriced models, prices, folding, retention
 *   └─ UsagePriceTable   collapsed by default
 */

import { useCallback, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Skeleton } from '@/components/ui/Skeleton';
import { USAGE_TOKEN_TYPES, type UsageReport } from '@/services/api/usageHistory';
import { UsageChart } from './UsageChart';
import { UsageControls } from './UsageControls';
import { UsageFootnote } from './UsageFootnote';
import { UsageGroupTable } from './UsageGroupTable';
import { UsagePriceTable, type UsagePricePanel } from './UsagePriceTable';
import { UsageSummary } from './UsageSummary';
import { OTHER_SERIES } from './usageChartModel';
import { groupLabel, type UsageControls as Controls } from './usageModel';
import styles from './UsageView.module.scss';

const TOKEN_TYPES = new Set<string>(USAGE_TOKEN_TYPES);

export interface UsageViewProps {
  controls: Controls;
  /** YYYY-MM-DD in the browser's time zone; presets count back from it. */
  today: string;
  report: UsageReport | null;
  loading: boolean;
  error: string;
  /** Key hash -> display label (saved name, masked key, or #hash). */
  clientLabels: ReadonlyMap<string, string>;
  prices: UsagePricePanel;
  onControlsChange: (next: Controls) => void;
  onExportCsv: () => void;
  onTogglePrices: () => void;
  onRefresh: () => void;
}

const hasTraffic = (report: UsageReport) => report.groups.length > 0 || report.totals.requests > 0;

export function UsageView({
  controls,
  today,
  report,
  loading,
  error,
  clientLabels,
  prices,
  onControlsChange,
  onExportCsv,
  onTogglePrices,
  onRefresh,
}: UsageViewProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language || 'en';
  const folded = report?.foldedGroups ?? 0;
  const translate = useCallback((key: string) => t(key, { n: folded }), [folded, t]);
  const dimension = report?.query.groupBy ?? controls.groupBy;
  const labelOf = useCallback(
    (key: string) => groupLabel(dimension, key, clientLabels, translate),
    [clientLabels, dimension, translate]
  );
  const seriesLabel = useCallback(
    (id: string) => {
      if (id === OTHER_SERIES) return t('usage.other_series');
      if (controls.stackBy === 'tokenType' && controls.metric !== 'requests' && TOKEN_TYPES.has(id))
        return t(`usage.token_${id}`);
      return labelOf(id);
    },
    [controls.metric, controls.stackBy, labelOf, t]
  );
  const optionLabel = useCallback(
    (filterDimension: Controls['groupBy'], value: string) =>
      groupLabel(filterDimension, value, clientLabels, translate),
    [clientLabels, translate]
  );

  const ready = report?.enabled === true;
  const traffic = ready && hasTraffic(report);

  let body: ReactNode;
  if (!report && error) {
    body = (
      <div className={styles.error} role="alert">
        <p>
          {t('usage.error_title')}: {error}
        </p>
        <Button variant="secondary" size="sm" onClick={onRefresh}>
          {t('usage.retry')}
        </Button>
      </div>
    );
  } else if (!report) {
    body = (
      <div className={styles.skeleton} aria-busy="true" aria-label={t('usage.loading')}>
        <Skeleton height={64} rounded={10} />
        <Skeleton height={280} rounded={10} />
        <Skeleton height={160} rounded={10} />
      </div>
    );
  } else if (!report.enabled) {
    const reason =
      report.reason === 'home_mode' || report.reason === 'unavailable' ? report.reason : 'disabled';
    body = (
      <EmptyState title={t(`usage.${reason}_title`)} description={t(`usage.${reason}_desc`)} />
    );
  } else {
    body = (
      <>
        {error ? (
          <div className={styles.error} role="alert">
            {t('usage.error_title')}: {error}
          </div>
        ) : null}
        <UsageControls
          controls={controls}
          today={today}
          options={report.options}
          optionLabel={optionLabel}
          onChange={onControlsChange}
        />
        <div className={loading ? styles.stale : undefined} aria-busy={loading || undefined}>
          {traffic ? (
            <div className={styles.results}>
              <UsageSummary report={report} locale={locale} />
              <UsageChart
                report={report}
                metric={controls.metric}
                stackBy={controls.stackBy}
                seriesLabel={seriesLabel}
                locale={locale}
              />
              <UsageGroupTable report={report} groupLabel={labelOf} locale={locale} />
            </div>
          ) : (
            <EmptyState title={t('usage.empty_title')} description={t('usage.empty_desc')} />
          )}
        </div>
        <UsageFootnote report={report} locale={locale} />
        <UsagePriceTable panel={prices} locale={locale} onToggle={onTogglePrices} />
      </>
    );
  }

  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <div>
          <h1 className={styles.title}>{t('usage.title')}</h1>
          <p className={styles.subtitle}>{t('usage.subtitle')}</p>
        </div>
        <div className={styles.actions}>
          {traffic ? (
            <Button variant="secondary" size="sm" onClick={onExportCsv}>
              {t('usage.export_csv')}
            </Button>
          ) : null}
          <Button variant="secondary" size="sm" onClick={onRefresh} loading={loading && !!report}>
            {t('usage.refresh')}
          </Button>
        </div>
      </header>
      {body}
    </div>
  );
}
