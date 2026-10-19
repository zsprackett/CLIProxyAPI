/**
 * Per-provider totals above the ledger.
 *
 * Deliberately does not add percentages across credentials: 1% of one plan is
 * not 1% of another, so the headline is a count of credentials that can serve
 * right now (judged by each one's tightest window). The segmented bar keeps the
 * per-credential detail visible, and the expiring line answers the question
 * the soonest-reset router acts on — how much weekly quota is about to be lost.
 */

import { useTranslation } from 'react-i18next';
import type { ResolvedTheme } from '@/types';
import { formatInstantShort, formatRelativeInstant } from '@/utils/quota';
import type { ProviderSummary } from '../ledgerModel';
import { ProviderMark } from './ProviderMark';
import { remainingTone } from './ledgerTone';
import styles from './QuotaSummaryStrip.module.scss';

export interface QuotaSummaryStripProps {
  summaries: ProviderSummary[];
  resolvedTheme: ResolvedTheme;
  nowMs: number;
}

export function QuotaSummaryStrip({ summaries, resolvedTheme, nowMs }: QuotaSummaryStripProps) {
  const { t, i18n } = useTranslation();
  if (summaries.length === 0) return null;

  return (
    <section className={styles.strip} aria-label={t('quota_management.summary_aria')}>
      {summaries.map((summary) => (
        <article key={summary.provider} className={styles.card}>
          <header className={styles.head}>
            <ProviderMark provider={summary.provider} resolvedTheme={resolvedTheme} />
            <span className={styles.count}>
              {t('quota_management.meta_credentials', { count: summary.credentials })}
            </span>
          </header>

          <div className={styles.headline}>
            {/* Credentials without data are neither usable nor exhausted; leave them out. */}
            {summary.credentials > summary.unknown ? (
              <>
                <span className={styles.figure}>
                  {summary.usable}
                  <span className={styles.figureOf}>
                    {' '}
                    / {summary.credentials - summary.unknown}
                  </span>
                </span>
                <span className={styles.figureLabel}>{t('quota_management.summary_usable')}</span>
              </>
            ) : (
              <>
                <span className={styles.figure}>--</span>
                <span className={styles.figureLabel}>{t('quota_management.summary_no_data')}</span>
              </>
            )}
          </div>

          <div className={styles.segments} aria-hidden="true">
            {summary.segments.map((remaining, index) => (
              <span key={index} className={styles.segment}>
                <span
                  className={`${styles.segmentFill} ${styles[remainingTone(remaining)]}`}
                  style={{ width: `${remaining ?? 0}%` }}
                />
              </span>
            ))}
          </div>

          <div className={styles.detail}>
            {summary.soonestWeeklyResetMs !== null ? (
              <span>
                {t('quota_management.summary_soonest_weekly', {
                  relative: formatRelativeInstant(
                    summary.soonestWeeklyResetMs,
                    nowMs,
                    i18n.resolvedLanguage
                  ),
                  absolute: formatInstantShort(summary.soonestWeeklyResetMs),
                })}
              </span>
            ) : (
              <span className={styles.muted}>{t('quota_management.summary_no_weekly')}</span>
            )}
            {summary.expiringSoon >= 0.05 && (
              <span className={styles.expiring}>
                {t('quota_management.summary_expiring', {
                  amount: summary.expiringSoon.toFixed(1),
                })}
              </span>
            )}
            {summary.unknown > 0 && (
              <span className={styles.muted}>
                {t('quota_management.summary_unknown', { count: summary.unknown })}
              </span>
            )}
          </div>
        </article>
      ))}
    </section>
  );
}
