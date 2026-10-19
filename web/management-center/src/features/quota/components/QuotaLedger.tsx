/**
 * Ledger view: one dense row per credential, grouped by provider.
 *
 * Each row shows every window side by side with its remaining percentage and
 * reset time, so a whole fleet fits on one screen. Rows fall back to the
 * passive snapshot the proxy recorded from response headers until a live
 * reading is loaded, and say which one they show.
 */

import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { IconRefreshCw } from '@/components/ui/icons';
import { PlanBadge } from '@/features/authFiles/components/PlanBadge';
import { resolveAuthFilePlan } from '@/features/authFiles/plan';
import type { AuthFileItem, ResolvedTheme } from '@/types';
import { formatInstantShort, formatRelativeInstant, resolveQuotaErrorMessage } from '@/utils/quota';
import { getQuotaDisplayName } from '@/utils/quota/identity';
import { maskIdentity, type LedgerCredential, type LedgerWindow } from '../ledgerModel';
import type { QuotaFileEntry } from '../logic';
import type { QuotaCardState } from '../providers';
import type { QuotaProviderType } from '../providers/types';
import { ProviderMark } from './ProviderMark';
import { remainingTone } from './ledgerTone';
import styles from './QuotaLedger.module.scss';

export interface LedgerItem {
  entry: QuotaFileEntry;
  credential: LedgerCredential;
  quota?: QuotaCardState;
}

export interface QuotaLedgerProps {
  items: LedgerItem[];
  resolvedTheme: ResolvedTheme;
  nowMs: number;
  showIdentities: boolean;
  canRefresh: (entry: QuotaFileEntry) => boolean;
  /** Key of the credential the soonest-reset router is expected to pick next. */
  nextPickKeys: ReadonlySet<string>;
  onRefresh: (entry: QuotaFileEntry) => void;
}

const windowLabel = (t: TFunction, window: LedgerWindow): string =>
  window.labelKey ? t(window.labelKey, window.labelParams) : (window.label ?? window.id);

const planLabel = (
  t: TFunction,
  provider: QuotaProviderType,
  planType: string | null
): string | null => {
  if (!planType) return null;
  if (provider === 'claude') return t(`claude_quota.${planType}`, { defaultValue: '' }) || null;
  if (provider === 'codex') return t(`codex_quota.plan_${planType}`, { defaultValue: planType });
  return null;
};

/**
 * Live quota reports the freshest plan; until it is loaded, fall back to the plan
 * the backend recorded on the credential so observed rows still show it.
 */
const rowPlanLabel = (
  t: TFunction,
  provider: QuotaProviderType,
  livePlanType: string | null,
  file: AuthFileItem
): string | null => {
  const live = planLabel(t, provider, livePlanType);
  if (live) return live;
  const recorded = resolveAuthFilePlan(file);
  return recorded ? t(recorded.labelKey, { defaultValue: recorded.fallback }) : null;
};

function groupByProvider(items: LedgerItem[]): [QuotaProviderType, LedgerItem[]][] {
  const groups = new Map<QuotaProviderType, LedgerItem[]>();
  for (const item of items) {
    const group = groups.get(item.entry.type);
    if (group) group.push(item);
    else groups.set(item.entry.type, [item]);
  }
  return [...groups.entries()];
}

export function QuotaLedger({
  items,
  resolvedTheme,
  nowMs,
  showIdentities,
  canRefresh,
  nextPickKeys,
  onRefresh,
}: QuotaLedgerProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;

  const renderWindow = (window: LedgerWindow) => {
    const percent = window.remaining === null ? '--' : `${Math.round(window.remaining)}%`;
    return (
      <div key={window.id} className={styles.window}>
        <div className={styles.windowHead}>
          <span className={styles.windowLabel}>{windowLabel(t, window)}</span>
          <span className={styles.windowPercent}>{percent}</span>
        </div>
        <span className={styles.bar} aria-hidden="true">
          <span
            className={`${styles.barFill} ${styles[remainingTone(window.remaining)]}`}
            style={{ width: `${window.remaining ?? 0}%` }}
          />
        </span>
        <span className={styles.windowReset}>
          {window.resetAtMs === null ? (
            t('quota_management.ledger_no_reset')
          ) : (
            <>
              <span className={styles.resetRelative}>
                {formatRelativeInstant(window.resetAtMs, nowMs, locale)}
              </span>
              {' · '}
              {formatInstantShort(window.resetAtMs)}
            </>
          )}
        </span>
      </div>
    );
  };

  const renderRow = ({ entry, credential, quota }: LedgerItem) => {
    const { row } = credential;
    const status = quota?.status ?? 'idle';
    const loading = status === 'loading';
    const name = getQuotaDisplayName(entry.file);
    const shownName = showIdentities ? name : maskIdentity(name);
    const plan = rowPlanLabel(t, entry.type, row.planType, entry.file);
    const isNext = nextPickKeys.has(credential.key);
    const errorMessage =
      status === 'error'
        ? resolveQuotaErrorMessage(t, quota?.errorStatus, quota?.error || t('common.unknown_error'))
        : null;

    return (
      <div key={credential.key} className={styles.row}>
        <div className={styles.identity}>
          <span className={styles.name} title={shownName}>
            {shownName}
          </span>
          <span className={styles.meta}>
            {plan && <PlanBadge label={plan} />}
            {isNext && (
              <span className={styles.next} title={t('quota_management.ledger_next_hint')}>
                {t('quota_management.ledger_next')}
              </span>
            )}
            {credential.coolingDown && (
              <span className={styles.cooling}>{t('quota_management.ledger_cooling')}</span>
            )}
            {row.source === 'observed' && row.observedAtMs !== null && (
              <span className={styles.observed} title={t('quota_management.ledger_observed_hint')}>
                {t('quota_management.ledger_observed', {
                  age: formatRelativeInstant(row.observedAtMs, nowMs, locale),
                })}
              </span>
            )}
          </span>
          {errorMessage && <span className={styles.error}>{errorMessage}</span>}
        </div>

        <div className={styles.windows}>
          {row.windows.length > 0 ? (
            row.windows.map(renderWindow)
          ) : (
            <span className={styles.empty}>
              {loading
                ? t('quota_management.ledger_loading')
                : status === 'success'
                  ? t(`${entry.type}_quota.empty_windows`, {
                      defaultValue: t('quota_management.ledger_no_data'),
                    })
                  : t('quota_management.ledger_no_data')}
            </span>
          )}
        </div>

        <div className={styles.actions}>
          <button
            type="button"
            className={styles.refresh}
            onClick={() => onRefresh(entry)}
            disabled={!canRefresh(entry) || loading}
          >
            <IconRefreshCw size={13} className={loading ? styles.spinning : undefined} />
            {row.source === 'live'
              ? t('quota_management.ledger_refresh')
              : t('quota_management.ledger_load')}
          </button>
        </div>
      </div>
    );
  };

  return (
    <div className={styles.ledger}>
      {groupByProvider(items).map(([provider, group]) => (
        <section key={provider} className={styles.group}>
          <header className={styles.groupHead}>
            <ProviderMark provider={provider} resolvedTheme={resolvedTheme} />
            <span className={styles.groupCount}>{group.length}</span>
          </header>
          <div className={styles.rows}>{group.map(renderRow)}</div>
        </section>
      ))}
    </div>
  );
}
