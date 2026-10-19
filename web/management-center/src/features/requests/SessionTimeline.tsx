import { useTranslation } from 'react-i18next';
import type { RequestEntry } from '@/services/api/requestLog';
import { formatCompactNumber } from '@/utils/format';
import { causeKey, segmentLabel, sessionTimeline, verdictKey } from './requestsModel';
import styles from './SessionTimeline.module.scss';

export interface SessionTimelineProps {
  /** Oldest first, as the backend returns for order=asc. */
  rows: readonly RequestEntry[];
  /** More requests exist than the window holds. */
  truncated: boolean;
}

const CHART_HEIGHT = 160;
const BAR_GAP = 2;

export function SessionTimeline({ rows, truncated }: SessionTimelineProps) {
  const { t } = useTranslation();
  if (!rows.length) return <p className={styles.empty}>{t('request_log.timeline_empty')}</p>;

  const bars = sessionTimeline(rows);
  const barWidth = Math.max(3, Math.min(16, Math.floor(960 / bars.length)));
  const width = bars.length * (barWidth + BAR_GAP);
  const flagged = rows.filter(
    (row) => row.cache.verdict === 'miss' || row.cache.verdict === 'partial'
  );

  return (
    <div className={styles.timeline}>
      <ul className={styles.legend} aria-hidden="true">
        <li data-part="read">{t('request_log.timeline_legend_read')}</li>
        <li data-part="write">{t('request_log.timeline_legend_write')}</li>
        <li data-part="uncached">{t('request_log.timeline_legend_uncached')}</li>
        <li data-flag="partial">{t('request_log.verdict_partial')}</li>
        <li data-flag="miss">{t('request_log.verdict_miss')}</li>
      </ul>
      {truncated ? (
        <p className={styles.note}>{t('request_log.timeline_scope', { count: rows.length })}</p>
      ) : null}
      <div className={styles.scroll}>
        <svg
          className={styles.chart}
          width={width}
          height={CHART_HEIGHT}
          viewBox={`0 0 ${width} ${CHART_HEIGHT}`}
          role="img"
          aria-label={t('request_log.timeline_title')}
        >
          {bars.map((bar, index) => {
            const row = rows[index];
            const x = index * (barWidth + BAR_GAP);
            const height = (bar.heightPct / 100) * CHART_HEIGHT;
            const readH = (bar.readPct / 100) * height;
            const writeH = (bar.writePct / 100) * height;
            const uncachedH = (bar.uncachedPct / 100) * height;
            const flag = bar.verdict === 'miss' || bar.verdict === 'partial' ? bar.verdict : null;
            const label = [
              t('request_log.timeline_bar', {
                seq: bar.seq,
                verdict: bar.failed ? t('request_log.timeline_failed') : t(verdictKey(bar.verdict)),
                read: formatCompactNumber(row.tokens.cacheRead),
                write: formatCompactNumber(row.tokens.cacheWrite),
                input: formatCompactNumber(row.tokens.input),
              }),
              bar.cause ? t(causeKey(bar.cause)) : '',
            ]
              .filter(Boolean)
              .join(' - ');
            return (
              <g key={bar.seq} data-bar={bar.seq} data-flag={flag ?? undefined}>
                <title>{label}</title>
                {flag ? (
                  <rect
                    className={styles.flagBand}
                    data-flag={flag}
                    x={x - 1}
                    y={0}
                    width={barWidth + 2}
                    height={CHART_HEIGHT}
                  />
                ) : null}
                <rect
                  data-part="read"
                  x={x}
                  y={CHART_HEIGHT - readH}
                  width={barWidth}
                  height={readH}
                />
                <rect
                  data-part="write"
                  x={x}
                  y={CHART_HEIGHT - readH - writeH}
                  width={barWidth}
                  height={writeH}
                />
                <rect
                  data-part="uncached"
                  x={x}
                  y={CHART_HEIGHT - readH - writeH - uncachedH}
                  width={barWidth}
                  height={uncachedH}
                />
              </g>
            );
          })}
        </svg>
      </div>
      {flagged.length ? (
        <ul className={styles.flags}>
          {flagged.map((row) => {
            const segment = segmentLabel(row.cache);
            return (
              <li key={row.seq} data-flag={row.cache.verdict}>
                <span className={styles.seq}>#{row.seq}</span>
                <strong>{t(verdictKey(row.cache.verdict))}</strong>
                <span>{row.cache.cause ? t(causeKey(row.cache.cause)) : ''}</span>
                {segment ? (
                  <span className={styles.muted}>{t(segment.key, segment.values)}</span>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}
