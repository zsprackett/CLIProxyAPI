/**
 * Quota provider adapter = data layer (React-free) + render body.
 *
 * The page uses the type-erased QuotaAdapter view (the same narrow-interface
 * cast as AuthFileQuotaSection); each data module exports the strong types.
 * Claude and Codex have no live body: their card renders the snapshot through
 * SNAPSHOT_ADAPTERS in snapshotBodies.tsx.
 */

import type { ComponentType } from 'react';
import type { TFunction } from 'i18next';
import { useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import type { QuotaSnapshot } from '../snapshot/types';
import type { QuotaBodyProps } from '../types';
import type { QuotaProviderType, QuotaStore } from './types';
import { ANTIGRAVITY_CONFIG } from './antigravity/data';
import { AntigravityQuotaBody } from './antigravity/AntigravityQuotaBody';
import { DEVIN_CONFIG } from './devin/data';
import { DevinQuotaBody } from './devin/DevinQuotaBody';
import { META_CONFIG } from './meta/data';
import { MetaQuotaBody } from './meta/MetaQuotaBody';
import { KIMI_CONFIG } from './kimi/data';
import { KimiQuotaBody } from './kimi/KimiQuotaBody';
import { CLAUDE_CONFIG, CODEX_CONFIG } from './snapshotPoll';
import { XAI_CONFIG } from './xai/data';
import { XaiQuotaBody } from './xai/XaiQuotaBody';

/** The common skeleton of every provider quota state (a structural subset of each). */
export interface QuotaCardState {
  status: 'idle' | 'loading' | 'success' | 'error';
  error?: string;
  errorStatus?: number;
  /** When a success state was fetched, so `newerQuotaReading` can compare it with the snapshot. */
  fetchedAtMs?: number;
}

export interface QuotaAdapter {
  type: QuotaProviderType;
  i18nPrefix: string;
  filterFn: (file: AuthFileItem) => boolean;
  fetchQuota: (file: AuthFileItem, t: TFunction) => Promise<unknown>;
  enrichQuota?: (file: AuthFileItem, data: unknown, t: TFunction) => Promise<unknown>;
  resetQuota?: (file: AuthFileItem, t: TFunction) => Promise<unknown>;
  canResetQuota?: (snapshot: QuotaSnapshot) => boolean;
  storeResult?: (file: AuthFileItem, data: unknown) => void;
  storeSelector: (state: QuotaStore) => Record<string, QuotaCardState>;
  storeSetter: keyof QuotaStore;
  buildLoadingState: () => QuotaCardState;
  buildSuccessState: (data: unknown) => QuotaCardState;
  buildErrorState: (message: string, status?: number) => QuotaCardState;
  /** Absent for snapshot providers, whose data is the credential's snapshot. */
  Body?: ComponentType<QuotaBodyProps<QuotaCardState>>;
}

/** Stamps every success state with its fetch time; all live writes go through these adapters. */
const withFetchedAt = (adapter: QuotaAdapter): QuotaAdapter => ({
  ...adapter,
  buildSuccessState: (data) => ({ ...adapter.buildSuccessState(data), fetchedAtMs: Date.now() }),
});

export const QUOTA_ADAPTERS: Record<QuotaProviderType, QuotaAdapter> = {
  antigravity: withFetchedAt({
    ...ANTIGRAVITY_CONFIG,
    Body: AntigravityQuotaBody,
  } as unknown as QuotaAdapter),
  claude: CLAUDE_CONFIG as unknown as QuotaAdapter,
  codex: CODEX_CONFIG as unknown as QuotaAdapter,
  devin: withFetchedAt({ ...DEVIN_CONFIG, Body: DevinQuotaBody } as unknown as QuotaAdapter),
  kimi: withFetchedAt({ ...KIMI_CONFIG, Body: KimiQuotaBody } as unknown as QuotaAdapter),
  meta: withFetchedAt({ ...META_CONFIG, Body: MetaQuotaBody } as unknown as QuotaAdapter),
  xai: withFetchedAt({ ...XAI_CONFIG, Body: XaiQuotaBody } as unknown as QuotaAdapter),
};

export type QuotaMapUpdater = (
  updater: (prev: Record<string, QuotaCardState>) => Record<string, QuotaCardState>
) => void;

/** The store setter of the adapter (reads getState, no subscription). */
export const getQuotaSetter = (adapter: QuotaAdapter): QuotaMapUpdater =>
  useQuotaStore.getState()[adapter.storeSetter] as unknown as QuotaMapUpdater;

/** The quota cache of the adapter (reads getState, no subscription). */
export const getQuotaMap = (adapter: QuotaAdapter): Record<string, QuotaCardState> =>
  adapter.storeSelector(useQuotaStore.getState() as unknown as QuotaStore);
