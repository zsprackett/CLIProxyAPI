import { useTranslation } from 'react-i18next';
import type { CacheDiagnosis } from '@/services/api/requestLog';
import { cacheLines } from './cacheLines';
import { causeKey, verdictKey } from './requestsModel';
import styles from './VerdictBadge.module.scss';

/** Verdict plus, for a miss or partial, its primary cause. The tooltip carries the rest. */
export function VerdictBadge({ cache }: { cache: CacheDiagnosis }) {
  const { t } = useTranslation();
  const verdict = t(verdictKey(cache.verdict));
  const flagged = cache.verdict === 'miss' || cache.verdict === 'partial';
  const tooltip = [
    verdict,
    ...cacheLines(t, cache).map((line) => `${line.label}: ${line.value}`),
  ].join('\n');
  return (
    <span className={styles.wrap} title={tooltip}>
      <span className={styles.badge} data-verdict={cache.verdict}>
        {verdict}
      </span>
      {flagged && cache.cause ? (
        <span className={styles.cause}>{t(causeKey(cache.cause))}</span>
      ) : null}
    </span>
  );
}
