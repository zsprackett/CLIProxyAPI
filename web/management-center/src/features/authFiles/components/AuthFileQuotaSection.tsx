import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  captureQuotaCacheGeneration,
  commitIfQuotaCacheCurrent,
  useNotificationStore,
  useQuotaStore,
} from '@/stores';
import type { AuthFileItem } from '@/types';
import { getStatusFromError, resolveQuotaErrorMessage } from '@/utils/quota';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { isRuntimeOnlyAuthFile, type QuotaProviderType } from '@/features/authFiles/constants';
import { Button } from '@/components/ui/Button';
import { IconRefreshCw } from '@/components/ui/icons';
import { bindQuotaClasses } from '@/features/quota/types';
import {
  applyPolledSnapshot,
  newerQuotaReading,
  readQuotaSnapshot,
} from '@/features/quota/snapshot/windows';
import { useNow } from '@/hooks/useNow';
import { QUOTA_ADAPTERS, type QuotaCardState } from '@/features/quota/providers';
import { SnapshotBody } from '@/features/quota/providers/SnapshotBody';
import styles from './AuthFileQuota.module.scss';

/** Compact quota classes of the auth-file card; a missing class key throws at module load. */
const compactQuotaClasses = bindQuotaClasses(styles, 'AuthFileQuota.module.scss');

const assertNever = (value: never): never => {
  throw new Error(`Unsupported quota type: ${value}`);
};

type QuotaMapUpdater = (
  updater: (prev: Record<string, QuotaCardState>) => Record<string, QuotaCardState>
) => void;

export type AuthFileQuotaSectionProps = {
  file: AuthFileItem;
  quotaType: QuotaProviderType;
  disableControls: boolean;
};

export function AuthFileQuotaSection(props: AuthFileQuotaSectionProps) {
  const { file, quotaType, disableControls } = props;
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const [resettingQuota, setResettingQuota] = useState(false);
  const nowMs = useNow();
  const adapter = QUOTA_ADAPTERS[quotaType];
  const cacheKey = getQuotaCacheKey(file);

  const storedQuota = useQuotaStore((state) => {
    if (quotaType === 'antigravity')
      return state.antigravityQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'claude' || quotaType === 'codex') {
      return state.quotaPolls[cacheKey] as QuotaCardState | undefined;
    }
    if (quotaType === 'devin') return state.devinQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'kimi') return state.kimiQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'meta') return state.metaQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'xai') return state.xaiQuota[cacheKey] as QuotaCardState | undefined;
    return assertNever(quotaType);
  });
  const quota = storedQuota;
  const polledSnapshots = useQuotaStore((state) => state.quotaSnapshots);

  const updateQuotaState = useQuotaStore(
    (state) => state[adapter.storeSetter] as unknown as QuotaMapUpdater
  );

  const refreshQuotaForFile = useCallback(async () => {
    if (disableControls) return;
    if (isRuntimeOnlyAuthFile(file)) return;
    if (file.disabled) return;
    if (quota?.status === 'loading') return;

    const cacheGeneration = captureQuotaCacheGeneration(file.name);

    updateQuotaState((prev) => ({
      ...prev,
      [cacheKey]: adapter.buildLoadingState(),
    }));

    try {
      const data = await adapter.fetchQuota(file, t);
      commitIfQuotaCacheCurrent(cacheGeneration, () => {
        updateQuotaState((prev) => ({
          ...prev,
          [cacheKey]: adapter.buildSuccessState(data),
        }));
        adapter.storeResult?.(file, data);
        const pollError = adapter.pollError?.(data);
        if (pollError) {
          showNotification(
            t('auth_files.quota_refresh_failed', { name: file.name, message: pollError }),
            'error'
          );
        } else {
          showNotification(t('auth_files.quota_refresh_success', { name: file.name }), 'success');
        }
      });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.unknown_error');
      const status = getStatusFromError(err);
      commitIfQuotaCacheCurrent(cacheGeneration, () => {
        updateQuotaState((prev) => ({
          ...prev,
          [cacheKey]: adapter.buildErrorState(message, status),
        }));
        showNotification(
          t('auth_files.quota_refresh_failed', { name: file.name, message }),
          'error'
        );
      });
    }
  }, [
    adapter,
    cacheKey,
    disableControls,
    file,
    quota?.status,
    showNotification,
    t,
    updateQuotaState,
  ]);

  const resetQuotaForFile = useCallback(() => {
    if (disableControls) return;
    if (isRuntimeOnlyAuthFile(file)) return;
    if (file.disabled) return;
    if (quota?.status === 'loading') return;
    if (resettingQuota) return;

    const resetQuota = adapter.resetQuota;
    if (!resetQuota) return;

    showConfirmation({
      title: t('codex_quota.reset_confirm_title'),
      message: t('codex_quota.reset_confirm_message', { name: file.name }),
      confirmText: t('codex_quota.reset_confirm_button'),
      variant: 'primary',
      onConfirm: async () => {
        const cacheGeneration = captureQuotaCacheGeneration(file.name);
        setResettingQuota(true);
        try {
          const data = await resetQuota(file, t);
          commitIfQuotaCacheCurrent(cacheGeneration, () => {
            updateQuotaState((prev) => ({
              ...prev,
              [cacheKey]: adapter.buildSuccessState(data),
            }));
            adapter.storeResult?.(file, data);
            showNotification(t('codex_quota.reset_success', { name: file.name }), 'success');
          });
        } catch (err: unknown) {
          const message = err instanceof Error ? err.message : t('common.unknown_error');
          commitIfQuotaCacheCurrent(cacheGeneration, () => {
            showNotification(t('codex_quota.reset_failed', { name: file.name, message }), 'error');
          });
        } finally {
          setResettingQuota(false);
        }
      },
    });
  }, [
    adapter,
    cacheKey,
    disableControls,
    file,
    quota?.status,
    resettingQuota,
    showConfirmation,
    showNotification,
    t,
    updateQuotaState,
  ]);

  const quotaStatus = quota?.status ?? 'idle';
  const snapshot = readQuotaSnapshot(
    quotaType,
    applyPolledSnapshot(file, polledSnapshots).quota_snapshot
  );
  const showSnapshot = newerQuotaReading(quota, snapshot) === 'snapshot';
  const canRefreshQuota = !disableControls && !file.disabled && !resettingQuota;
  const canUseResetQuota = canRefreshQuota && quotaStatus !== 'loading';
  const showResetQuotaAction =
    showSnapshot && snapshot !== null && Boolean(adapter.canResetQuota?.(snapshot));
  const resetQuotaAction =
    adapter.resetQuota && showResetQuotaAction ? (
      <Button
        type="button"
        variant="secondary"
        size="sm"
        className={styles.quotaResetCreditButton}
        onClick={() => resetQuotaForFile()}
        disabled={!canUseResetQuota}
        loading={resettingQuota}
        title={t('codex_quota.reset_button')}
        aria-label={t('codex_quota.reset_button')}
      >
        {!resettingQuota && <IconRefreshCw size={14} />}
        {t('codex_quota.reset_button')}
      </Button>
    ) : undefined;
  const quotaErrorMessage = resolveQuotaErrorMessage(
    t,
    quota?.errorStatus,
    quota?.error || t('common.unknown_error')
  );

  return (
    <div className={styles.quotaSection}>
      {showSnapshot && snapshot && (
        <SnapshotBody snapshot={snapshot} classes={compactQuotaClasses} nowMs={nowMs} />
      )}
      {quotaStatus === 'loading' ? (
        <div className={styles.quotaMessage}>{t(`${adapter.i18nPrefix}.loading`)}</div>
      ) : quotaStatus === 'idle' ? (
        <button
          type="button"
          className={`${styles.quotaMessage} ${styles.quotaMessageAction}`}
          onClick={() => void refreshQuotaForFile()}
          disabled={!canRefreshQuota}
        >
          {t(`${adapter.i18nPrefix}.idle`)}
        </button>
      ) : quotaStatus === 'error' ? (
        <div className={styles.quotaError}>
          {t(`${adapter.i18nPrefix}.load_failed`, {
            message: quotaErrorMessage,
          })}
        </div>
      ) : showSnapshot ? null : quota && adapter.Body ? (
        <adapter.Body quota={quota} classes={compactQuotaClasses} />
      ) : (
        <div className={styles.quotaMessage}>{t(`${adapter.i18nPrefix}.idle`)}</div>
      )}
      {(resetQuotaAction || (quotaStatus !== 'idle' && quotaType === 'devin')) && (
        <div className={styles.quotaCardActions}>
          {resetQuotaAction}
          {quotaType === 'devin' && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className={styles.quotaResetCreditButton}
              onClick={() => void refreshQuotaForFile()}
              disabled={!canRefreshQuota || quotaStatus === 'loading'}
              loading={quotaStatus === 'loading'}
              title={t('auth_files.quota_refresh_hint')}
            >
              {quotaStatus !== 'loading' && <IconRefreshCw size={14} />}
              {t('auth_files.quota_refresh_single')}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
