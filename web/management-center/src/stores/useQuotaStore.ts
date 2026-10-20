/**
 * Quota cache that survives route switches.
 */

import { create } from 'zustand';
import { getQuotaCacheFileName } from '@/utils/quota/identity';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import {
  fileAuthIndex,
  listingCaughtUp,
  type PolledQuotaSnapshot,
} from '@/features/quota/snapshot/windows';
import type {
  AntigravityQuotaState,
  AuthFileItem,
  DevinQuotaState,
  KimiQuotaState,
  MetaQuotaState,
  SnapshotPollState,
  XaiQuotaState,
} from '@/types';

type QuotaUpdater<T> = T | ((prev: T) => T);

interface QuotaStoreState {
  cacheGeneration: number;
  fileGenerations: Record<string, number>;
  antigravityQuota: Record<string, AntigravityQuotaState>;
  devinQuota: Record<string, DevinQuotaState>;
  kimiQuota: Record<string, KimiQuotaState>;
  metaQuota: Record<string, MetaQuotaState>;
  xaiQuota: Record<string, XaiQuotaState>;
  /** On-demand poll results by auth index; the listing snapshot wins once it is as new. */
  quotaSnapshots: Record<string, PolledQuotaSnapshot>;
  /** Loading and error of the last on-demand poll, by quota cache key. */
  quotaPolls: Record<string, SnapshotPollState>;
  setAntigravityQuota: (updater: QuotaUpdater<Record<string, AntigravityQuotaState>>) => void;
  setDevinQuota: (updater: QuotaUpdater<Record<string, DevinQuotaState>>) => void;
  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;
  setMetaQuota: (updater: QuotaUpdater<Record<string, MetaQuotaState>>) => void;
  setXaiQuota: (updater: QuotaUpdater<Record<string, XaiQuotaState>>) => void;
  setQuotaSnapshot: (authIndex: string, name: string, snapshot: QuotaSnapshot) => void;
  /** Drops the poll results that the listing caught up with; unknown auth indexes stay. */
  reconcileQuotaSnapshots: (files: readonly AuthFileItem[]) => void;
  setQuotaPolls: (updater: QuotaUpdater<Record<string, SnapshotPollState>>) => void;
  clearQuotaCache: (names?: string[]) => void;
}

const resolveUpdater = <T>(updater: QuotaUpdater<T>, prev: T): T => {
  if (typeof updater === 'function') {
    return (updater as (value: T) => T)(prev);
  }
  return updater;
};

export const useQuotaStore = create<QuotaStoreState>((set) => ({
  cacheGeneration: 0,
  fileGenerations: {},
  antigravityQuota: {},
  devinQuota: {},
  kimiQuota: {},
  metaQuota: {},
  xaiQuota: {},
  quotaSnapshots: {},
  quotaPolls: {},
  setAntigravityQuota: (updater) =>
    set((state) => ({
      antigravityQuota: resolveUpdater(updater, state.antigravityQuota),
    })),
  setDevinQuota: (updater) =>
    set((state) => ({
      devinQuota: resolveUpdater(updater, state.devinQuota),
    })),
  setKimiQuota: (updater) =>
    set((state) => ({
      kimiQuota: resolveUpdater(updater, state.kimiQuota),
    })),
  setMetaQuota: (updater) =>
    set((state) => ({ metaQuota: resolveUpdater(updater, state.metaQuota) })),
  setXaiQuota: (updater) =>
    set((state) => ({
      xaiQuota: resolveUpdater(updater, state.xaiQuota),
    })),
  setQuotaSnapshot: (authIndex, name, snapshot) =>
    set((state) => ({
      quotaSnapshots: { ...state.quotaSnapshots, [authIndex]: { name, snapshot } },
    })),
  reconcileQuotaSnapshots: (files) =>
    set((state) => {
      const caughtUp = files.filter((file) => {
        const authIndex = fileAuthIndex(file);
        const polled = authIndex ? state.quotaSnapshots[authIndex] : undefined;
        return polled !== undefined && listingCaughtUp(polled.snapshot, file.quota_snapshot);
      });
      if (caughtUp.length === 0) return state;
      const quotaSnapshots = { ...state.quotaSnapshots };
      caughtUp.forEach((file) => delete quotaSnapshots[fileAuthIndex(file) as string]);
      return { quotaSnapshots };
    }),
  setQuotaPolls: (updater) =>
    set((state) => ({ quotaPolls: resolveUpdater(updater, state.quotaPolls) })),
  clearQuotaCache: (names) =>
    set((state) => {
      if (names) {
        if (names.length === 0) return state;
        const fileGenerations = { ...state.fileGenerations };
        names.forEach((name) => {
          fileGenerations[name] = (fileGenerations[name] ?? 0) + 1;
        });
        const invalidatedNames = new Set(names);
        const omitNames = <T>(
          cache: Record<string, T>,
          nameOf: (key: string, value: T) => string = getQuotaCacheFileName
        ): Record<string, T> => {
          const keysToDelete = Object.keys(cache).filter((key) =>
            invalidatedNames.has(nameOf(key, cache[key]))
          );
          if (keysToDelete.length === 0) return cache;
          const next = { ...cache };
          keysToDelete.forEach((key) => delete next[key]);
          return next;
        };
        return {
          fileGenerations,
          antigravityQuota: omitNames(state.antigravityQuota),
          devinQuota: omitNames(state.devinQuota),
          kimiQuota: omitNames(state.kimiQuota),
          metaQuota: omitNames(state.metaQuota),
          xaiQuota: omitNames(state.xaiQuota),
          quotaSnapshots: omitNames(state.quotaSnapshots, (_authIndex, polled) => polled.name),
          quotaPolls: omitNames(state.quotaPolls),
        };
      }
      return {
        cacheGeneration: state.cacheGeneration + 1,
        fileGenerations: {},
        antigravityQuota: {},
        devinQuota: {},
        kimiQuota: {},
        metaQuota: {},
        xaiQuota: {},
        quotaSnapshots: {},
        quotaPolls: {},
      };
    }),
}));

export const captureQuotaCacheGeneration = (name?: string) => {
  const { cacheGeneration, fileGenerations } = useQuotaStore.getState();
  return { cacheGeneration, fileGenerations, name };
};

export const commitIfQuotaCacheCurrent = (
  generation: ReturnType<typeof captureQuotaCacheGeneration>,
  commit: () => void,
  name: string | undefined = generation.name
): boolean => {
  const current = useQuotaStore.getState();
  if (current.cacheGeneration !== generation.cacheGeneration) return false;
  // File-scoped requests survive mutations to unrelated credentials.
  if (name !== undefined) {
    if ((current.fileGenerations[name] ?? 0) !== (generation.fileGenerations[name] ?? 0)) {
      return false;
    }
  } else if (current.fileGenerations !== generation.fileGenerations) {
    return false;
  }
  commit();
  return true;
};
