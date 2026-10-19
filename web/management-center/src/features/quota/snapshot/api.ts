/**
 * On-demand quota poll: the backend calls the upstream for one credential and
 * answers with the credential's whole merged snapshot.
 *
 * Rejections keep the HTTP status (`ApiError.status`): 404 unknown auth index,
 * 409 Home mode, 422 credential not pollable, 400 bad body. An upstream failure
 * is a 200 with `last_poll.error` set and the old windows kept.
 */

import { apiClient } from '@/services/api/client';
import type { QuotaSnapshot } from './types';

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

export async function pollCredentialQuota(authIndex: string): Promise<QuotaSnapshot> {
  const body = await apiClient.post<unknown>('/credentials/quota/poll', { auth_index: authIndex });
  const snapshot = isRecord(body) ? body.quota_snapshot : undefined;
  if (!isRecord(snapshot) || !Array.isArray(snapshot.windows)) {
    throw new Error('quota poll response has no quota_snapshot');
  }
  return snapshot as unknown as QuotaSnapshot;
}
