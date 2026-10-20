/**
 * Data contract of the quota providers.
 *
 * Data modules only fetch and build states: they import no React and no SCSS,
 * so bun:test logic tests can use them directly. The *QuotaBody components in
 * the same folder render.
 */

import type { TFunction } from 'i18next';
import type {
  AntigravityQuotaState,
  AuthFileItem,
  DevinQuotaState,
  KimiQuotaState,
  MetaQuotaState,
  SnapshotPollState,
  XaiQuotaState,
} from '@/types';
import type { QuotaSnapshot } from '../snapshot/types';

export type QuotaUpdater<T> = T | ((prev: T) => T);

export type QuotaProviderType =
  'antigravity' | 'claude' | 'codex' | 'devin' | 'kimi' | 'xai' | 'meta';

/** The shape of useQuotaStore that storeSelector and storeSetter rely on. */
export interface QuotaStore {
  antigravityQuota: Record<string, AntigravityQuotaState>;
  devinQuota: Record<string, DevinQuotaState>;
  kimiQuota: Record<string, KimiQuotaState>;
  metaQuota: Record<string, MetaQuotaState>;
  xaiQuota: Record<string, XaiQuotaState>;
  quotaPolls: Record<string, SnapshotPollState>;
  setAntigravityQuota: (updater: QuotaUpdater<Record<string, AntigravityQuotaState>>) => void;
  setDevinQuota: (updater: QuotaUpdater<Record<string, DevinQuotaState>>) => void;
  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;
  setMetaQuota: (updater: QuotaUpdater<Record<string, MetaQuotaState>>) => void;
  setXaiQuota: (updater: QuotaUpdater<Record<string, XaiQuotaState>>) => void;
  setQuotaPolls: (updater: QuotaUpdater<Record<string, SnapshotPollState>>) => void;
  clearQuotaCache: () => void;
}

export interface QuotaProviderData<TState, TData> {
  type: QuotaProviderType;
  i18nPrefix: string;
  filterFn: (file: AuthFileItem) => boolean;
  fetchQuota: (file: AuthFileItem, t: TFunction) => Promise<TData>;
  /** Optional details loaded only after the primary quota has been committed. */
  enrichQuota?: (file: AuthFileItem, data: TData, t: TFunction) => Promise<TData>;
  resetQuota?: (file: AuthFileItem, t: TFunction) => Promise<TData>;
  /** Whether the card offers a reset, from the credential's snapshot. */
  canResetQuota?: (snapshot: QuotaSnapshot) => boolean;
  /** Keeps a fetched result outside the card state; called with the success commit. */
  storeResult?: (file: AuthFileItem, data: TData) => void;
  /** An upstream failure inside a successful answer; a click reports it instead of success. */
  pollError?: (data: TData) => string | undefined;
  storeSelector: (state: QuotaStore) => Record<string, TState>;
  storeSetter: keyof QuotaStore;
  buildLoadingState: () => TState;
  buildSuccessState: (data: TData) => TState;
  buildErrorState: (message: string, status?: number) => TState;
}
