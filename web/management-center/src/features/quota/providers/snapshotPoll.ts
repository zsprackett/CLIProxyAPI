/**
 * Claude and Codex quota: one on-demand backend poll per click. The result is a
 * snapshot that goes to the snapshot store; the card state keeps only the
 * loading and error of the click.
 */

import type { TFunction } from 'i18next';
import type { AuthFileItem, SnapshotPollState } from '@/types';
import { useQuotaStore } from '@/stores/useQuotaStore';
import { createStatusError, isClaudeFile, isCodexFile, isDisabledAuthFile } from '@/utils/quota';
import { pollCredentialQuota } from '../snapshot/api';
import type { QuotaSnapshot } from '../snapshot/types';
import { fileAuthIndex } from '../snapshot/windows';
import { consumeCodexResetCredit } from './codex/resetCredit';
import type { QuotaProviderData } from './types';

type SnapshotPollConfig = QuotaProviderData<SnapshotPollState, QuotaSnapshot>;

const pollQuota =
  (i18nPrefix: string) =>
  async (file: AuthFileItem, t: TFunction): Promise<QuotaSnapshot> => {
    const authIndex = fileAuthIndex(file);
    if (!authIndex) {
      throw new Error(t(`${i18nPrefix}.missing_auth_index`));
    }
    const snapshot = await pollCredentialQuota(authIndex);
    // With no windows there is nothing to show, so the upstream error is the answer.
    const pollError = snapshot.last_poll?.error;
    if (pollError && snapshot.windows.length === 0) {
      throw createStatusError(pollError);
    }
    return snapshot;
  };

const storeSnapshot = (file: AuthFileItem, snapshot: QuotaSnapshot) => {
  const authIndex = fileAuthIndex(file);
  if (authIndex) useQuotaStore.getState().setQuotaSnapshot(authIndex, file.name, snapshot);
};

const snapshotPollConfig = (
  type: 'claude' | 'codex',
  filterFn: (file: AuthFileItem) => boolean
): SnapshotPollConfig => ({
  type,
  i18nPrefix: `${type}_quota`,
  filterFn,
  fetchQuota: pollQuota(`${type}_quota`),
  storeResult: storeSnapshot,
  storeSelector: (state) => state.quotaPolls,
  storeSetter: 'setQuotaPolls',
  buildLoadingState: () => ({ status: 'loading' }),
  // The snapshot store holds the data; a settled click leaves no card status.
  buildSuccessState: () => ({ status: 'idle' }),
  buildErrorState: (message, status) => ({
    status: 'error',
    error: message,
    errorStatus: status,
    failedAtMs: Date.now(),
  }),
});

export const CLAUDE_CONFIG = snapshotPollConfig(
  'claude',
  (file) => isClaudeFile(file) && !isDisabledAuthFile(file)
);

const pollCodexQuota = pollQuota('codex_quota');

export const CODEX_CONFIG: SnapshotPollConfig = {
  ...snapshotPollConfig('codex', (file) => isCodexFile(file) && !isDisabledAuthFile(file)),
  resetQuota: async (file, t) => {
    await consumeCodexResetCredit(file, t);
    return pollCodexQuota(file, t);
  },
  canResetQuota: (snapshot) =>
    snapshot.extras?.kind === 'codex' && (snapshot.extras.reset_credits?.value?.available ?? 0) > 0,
};
