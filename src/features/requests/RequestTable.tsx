import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import type { RequestEntry } from '@/services/api/requestLog';
import { formatCompactNumber } from '@/utils/format';
import { formatLatency, shortId } from './requestsModel';
import { cacheLines } from './cacheLines';
import { VerdictBadge } from './VerdictBadge';
import styles from './RequestTable.module.scss';

export interface RequestTableProps {
  rows: readonly RequestEntry[];
  /** Label per client key hash; an empty label means the request carried no client key. */
  labels: ReadonlyMap<string, string>;
  /** Rows that start expanded (by seq). */
  initialExpanded?: readonly number[];
  onSelectSession?: (sessionId: string) => void;
}

const COLUMN_COUNT = 10;

const formatRowTime = (ms: number, locale: string): string => {
  if (!ms) return '-';
  const date = new Date(ms);
  const sameDay = date.toDateString() === new Date().toDateString();
  return date.toLocaleString(
    locale,
    sameDay
      ? { timeStyle: 'medium' }
      : {
          dateStyle: 'short',
          timeStyle: 'medium',
        }
  );
};

export function RequestTable({
  rows,
  labels,
  initialExpanded = [],
  onSelectSession,
}: RequestTableProps) {
  const { t, i18n } = useTranslation();
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set(initialExpanded));

  const toggle = (seq: number) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(seq)) next.add(seq);
      return next;
    });

  return (
    <Table className={styles.table}>
      <TableHeader>
        <TableRow>
          <TableHead>{t('request_log.col_time')}</TableHead>
          <TableHead>{t('request_log.col_status')}</TableHead>
          <TableHead>{t('request_log.col_session')}</TableHead>
          <TableHead>{t('request_log.col_client')}</TableHead>
          <TableHead>{t('request_log.col_credential')}</TableHead>
          <TableHead>{t('request_log.col_model')}</TableHead>
          <TableHead alignRight>{t('request_log.col_latency')}</TableHead>
          <TableHead alignRight>{t('request_log.col_ttft')}</TableHead>
          <TableHead>{t('request_log.col_tokens')}</TableHead>
          <TableHead>{t('request_log.col_cache')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => {
          const open = expanded.has(row.seq);
          const client = labels.get(row.keyHash) ?? '';
          const tokenTitle = [
            `${t('request_log.tokens_input')}: ${row.tokens.input}`,
            `${t('request_log.tokens_cache_read')}: ${row.tokens.cacheRead}`,
            `${t('request_log.tokens_cache_write')}: ${row.tokens.cacheWrite}`,
            `${t('request_log.tokens_output')}: ${row.tokens.output}`,
          ].join('\n');
          const lines = cacheLines(t, row.cache);
          return (
            <Fragment key={row.seq}>
              <TableRow
                data-seq={row.seq}
                data-failed={row.failed ? 'true' : undefined}
                selected={open}
                className={styles.row}
                onClick={() => toggle(row.seq)}
                onKeyDown={(event) => {
                  if (event.target !== event.currentTarget) return;
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    toggle(row.seq);
                  }
                }}
                tabIndex={0}
                aria-expanded={open}
              >
                <TableCell className={styles.nowrap}>
                  {formatRowTime(row.atMs, i18n.language)}
                </TableCell>
                <TableCell>
                  {row.failed ? (
                    <span className={styles.failed} title={row.error || undefined}>
                      {row.status || t('request_log.status_failed')}
                    </span>
                  ) : (
                    <span className={styles.ok}>{t('request_log.status_ok')}</span>
                  )}
                </TableCell>
                <TableCell>
                  {row.sessionId ? (
                    <button
                      type="button"
                      className={styles.sessionLink}
                      title={row.sessionId}
                      aria-label={t('request_log.session_open', { id: shortId(row.sessionId) })}
                      onClick={(event) => {
                        event.stopPropagation();
                        onSelectSession?.(row.sessionId);
                      }}
                    >
                      {shortId(row.sessionId)}
                    </button>
                  ) : (
                    <span className={styles.muted}>-</span>
                  )}
                </TableCell>
                <TableCell>
                  {client || <span className={styles.muted}>{t('request_log.no_client_key')}</span>}
                </TableCell>
                <TableCell className={styles.ellipsis}>{row.authId || '-'}</TableCell>
                <TableCell className={styles.ellipsis}>{row.model || '-'}</TableCell>
                <TableCell alignRight className={styles.nowrap}>
                  {formatLatency(row.latencyMs)}
                </TableCell>
                <TableCell alignRight className={styles.nowrap}>
                  {formatLatency(row.ttftMs)}
                </TableCell>
                <TableCell className={styles.nowrap}>
                  <span className={styles.tokens} title={tokenTitle}>
                    <span>{formatCompactNumber(row.tokens.input)}</span>
                    <span className={styles.read}>{formatCompactNumber(row.tokens.cacheRead)}</span>
                    <span className={styles.write}>
                      {formatCompactNumber(row.tokens.cacheWrite)}
                    </span>
                    <span>{formatCompactNumber(row.tokens.output)}</span>
                  </span>
                </TableCell>
                <TableCell>
                  <VerdictBadge cache={row.cache} />
                </TableCell>
              </TableRow>
              {open ? (
                <TableRow className={styles.detailRow}>
                  <TableCell colSpan={COLUMN_COUNT}>
                    <dl className={styles.detail}>
                      {lines.map((line) => (
                        <div key={line.label}>
                          <dt>{line.label}</dt>
                          <dd>{line.value}</dd>
                        </div>
                      ))}
                      {row.error ? (
                        <div>
                          <dt>{t('request_log.detail_error')}</dt>
                          <dd>{row.error}</dd>
                        </div>
                      ) : null}
                      {row.agent ? (
                        <div>
                          <dt>{t('request_log.detail_agent')}</dt>
                          <dd>{row.agent}</dd>
                        </div>
                      ) : null}
                      <div>
                        <dt>{t('request_log.detail_request_id')}</dt>
                        <dd>{row.requestId}</dd>
                      </div>
                    </dl>
                  </TableCell>
                </TableRow>
              ) : null}
            </Fragment>
          );
        })}
      </TableBody>
    </Table>
  );
}
