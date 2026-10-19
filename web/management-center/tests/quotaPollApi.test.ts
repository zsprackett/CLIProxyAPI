import { describe, expect, spyOn, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { apiClient } from '@/services/api/client';
import { pollCredentialQuota } from '@/features/quota/snapshot/api';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';

const golden: QuotaSnapshot = JSON.parse(
  readFileSync(
    new URL('../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json', import.meta.url),
    'utf8'
  )
);

const statusError = (status: number) =>
  Object.assign(new Error(`request failed (${status})`), { name: 'ApiError', status });

describe('pollCredentialQuota', () => {
  test('posts the auth index to the poll endpoint and returns the snapshot', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue({ quota_snapshot: golden });
    try {
      const snapshot = await pollCredentialQuota('a1b2c3');
      expect(post.mock.calls).toEqual([['/credentials/quota/poll', { auth_index: 'a1b2c3' }]]);
      expect(snapshot.provider).toBe('claude');
      expect(snapshot.windows.map((window) => window.id)).toEqual([
        'five_hour',
        'seven_day',
        'seven_day_fable',
      ]);
    } finally {
      post.mockRestore();
    }
  });

  test.each([404, 409, 422])('a %d answer rejects with that status', async (status) => {
    const post = spyOn(apiClient, 'post').mockRejectedValue(statusError(status));
    try {
      await expect(pollCredentialQuota('a1b2c3')).rejects.toMatchObject({ status });
    } finally {
      post.mockRestore();
    }
  });

  test('a body without a snapshot rejects', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue({});
    try {
      await expect(pollCredentialQuota('a1b2c3')).rejects.toThrow();
    } finally {
      post.mockRestore();
    }
  });
});
