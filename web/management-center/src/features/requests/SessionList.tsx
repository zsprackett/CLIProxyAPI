import { useTranslation } from 'react-i18next';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import type { SessionSummary } from '@/services/api/requestLog';
import { formatCompactNumber, formatPercent } from '@/utils/format';
import { causeKey, shortId } from './requestsModel';
import styles from './RequestTable.module.scss';

export interface SessionListProps {
  sessions: readonly SessionSummary[];
  labels: ReadonlyMap<string, string>;
  onSelectSession?: (sessionId: string) => void;
}

export function SessionList({ sessions, labels, onSelectSession }: SessionListProps) {
  const { t, i18n } = useTranslation();
  return (
    <Table className={styles.table}>
      <TableHeader>
        <TableRow>
          <TableHead>{t('request_log.col_last_activity')}</TableHead>
          <TableHead>{t('request_log.col_session')}</TableHead>
          <TableHead>{t('request_log.col_client')}</TableHead>
          <TableHead alignRight>{t('request_log.col_requests')}</TableHead>
          <TableHead alignRight>{t('request_log.col_hit_ratio')}</TableHead>
          <TableHead alignRight>{t('request_log.col_lost_tokens')}</TableHead>
          <TableHead>{t('request_log.col_top_cause')}</TableHead>
          <TableHead>{t('request_log.col_models')}</TableHead>
          <TableHead>{t('request_log.col_credentials')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sessions.map((session) => {
          const client = labels.get(session.keyHash) ?? '';
          return (
            <TableRow
              key={session.sessionId}
              data-session={session.sessionId}
              className={styles.clickableRow}
              onClick={() => onSelectSession?.(session.sessionId)}
            >
              <TableCell className={styles.nowrap}>
                {session.lastAtMs
                  ? new Date(session.lastAtMs).toLocaleString(i18n.language, {
                      dateStyle: 'short',
                      timeStyle: 'medium',
                    })
                  : '-'}
              </TableCell>
              <TableCell>
                <button
                  type="button"
                  className={styles.sessionLink}
                  title={session.sessionId}
                  aria-label={t('request_log.session_open', { id: shortId(session.sessionId) })}
                  onClick={(event) => {
                    event.stopPropagation();
                    onSelectSession?.(session.sessionId);
                  }}
                >
                  {shortId(session.sessionId)}
                </button>
              </TableCell>
              <TableCell>
                {client || <span className={styles.muted}>{t('request_log.no_client_key')}</span>}
              </TableCell>
              <TableCell alignRight className={styles.nowrap}>
                {session.requests}
                {session.failed ? <span className={styles.failed}> ({session.failed})</span> : null}
              </TableCell>
              <TableCell alignRight className={styles.nowrap}>
                {formatPercent(session.hitRatio * 100)}
              </TableCell>
              <TableCell alignRight className={styles.nowrap}>
                {formatCompactNumber(session.lostTokens)}
              </TableCell>
              <TableCell>
                {session.topCause ? (
                  t(causeKey(session.topCause))
                ) : (
                  <span className={styles.muted}>{t('request_log.none')}</span>
                )}
              </TableCell>
              <TableCell className={styles.ellipsis} title={session.models.join(', ')}>
                {session.models.join(', ') || '-'}
              </TableCell>
              <TableCell className={styles.ellipsis} title={session.authIds.join(', ')}>
                {session.authIds.join(', ') || '-'}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}
