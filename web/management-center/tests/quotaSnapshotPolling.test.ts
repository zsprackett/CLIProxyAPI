/**
 * "Load quota" on a Claude or Codex card asks the backend to poll: one
 * POST /credentials/quota/poll and no api-call request. Spending a Codex manual
 * reset is still an api-call write, followed by one poll.
 */

import { afterEach, beforeEach, describe, expect, spyOn, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import i18n from '@/i18n';
import { QUOTA_ADAPTERS } from '@/features/quota/providers';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import { apiCallApi } from '@/services/api';
import { apiClient } from '@/services/api/client';
import { useQuotaStore } from '@/stores/useQuotaStore';
import type { AuthFileItem } from '@/types';
import { codexSnapshot } from './fixtures/quotaSnapshotCodex';

const claudeGolden: QuotaSnapshot = JSON.parse(
  readFileSync(
    new URL('../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json', import.meta.url),
    'utf8'
  )
);

const claudeFile: AuthFileItem = { name: 'claude-a.json', type: 'claude', auth_index: 'idx-a' };
const codexFile: AuthFileItem = {
  name: 'codex-a.json',
  type: 'codex',
  auth_index: 'idx-c',
  chatgpt_account_id: 'acct-123',
};

const t = i18n.t.bind(i18n);

let post: ReturnType<typeof spyOn>;
let apiCall: ReturnType<typeof spyOn>;

beforeEach(() => {
  useQuotaStore.getState().clearQuotaCache();
  apiCall = spyOn(apiCallApi, 'request').mockResolvedValue({
    statusCode: 200,
    header: {},
    bodyText: '{}',
    body: {},
  });
});

afterEach(() => {
  post?.mockRestore();
  apiCall.mockRestore();
});

/** Loads quota the way the card does: fetch, then the success commit. */
const loadQuota = async (file: AuthFileItem) => {
  const adapter = QUOTA_ADAPTERS[file.type as 'claude' | 'codex'];
  const data = await adapter.fetchQuota(file, t);
  adapter.storeResult?.(file, data);
  return adapter.buildSuccessState(data);
};

describe('Load quota', () => {
  test('a Claude card sends one poll and no api-call, and stores the snapshot', async () => {
    post = spyOn(apiClient, 'post').mockResolvedValue({ quota_snapshot: claudeGolden });

    const state = await loadQuota(claudeFile);

    expect(post.mock.calls).toEqual([['/credentials/quota/poll', { auth_index: 'idx-a' }]]);
    expect(apiCall).not.toHaveBeenCalled();
    expect(useQuotaStore.getState().quotaSnapshots).toEqual({
      'idx-a': { name: 'claude-a.json', snapshot: claudeGolden },
    });
    expect(state.status).toBe('idle');
  });

  test('a Codex card sends one poll and no api-call', async () => {
    const snapshot = codexSnapshot();
    post = spyOn(apiClient, 'post').mockResolvedValue({ quota_snapshot: snapshot });

    await loadQuota(codexFile);

    expect(post.mock.calls).toEqual([['/credentials/quota/poll', { auth_index: 'idx-c' }]]);
    expect(apiCall).not.toHaveBeenCalled();
    expect(useQuotaStore.getState().quotaSnapshots).toEqual({
      'idx-c': { name: 'codex-a.json', snapshot },
    });
  });

  test('an upstream failure with no windows to show fails the click with its error', async () => {
    post = spyOn(apiClient, 'post').mockResolvedValue({
      quota_snapshot: {
        ...claudeGolden,
        windows: [],
        last_poll: { at: '2026-10-19T18:00:00Z', trigger: 'on_demand', error: 'HTTP 401' },
      },
    });

    await expect(loadQuota(claudeFile)).rejects.toThrow('HTTP 401');
    expect(useQuotaStore.getState().quotaSnapshots).toEqual({});
  });

  test('a credential without an auth index fails without a request', async () => {
    post = spyOn(apiClient, 'post');
    await expect(loadQuota({ name: 'claude-b.json', type: 'claude' })).rejects.toThrow(
      'Auth file missing auth_index'
    );
    expect(post).not.toHaveBeenCalled();
  });
});

describe('Codex manual reset', () => {
  test('spends one credit through api-call, then polls once', async () => {
    const snapshot = codexSnapshot();
    post = spyOn(apiClient, 'post').mockResolvedValue({ quota_snapshot: snapshot });

    const data = await QUOTA_ADAPTERS.codex.resetQuota?.(codexFile, t);

    expect(apiCall.mock.calls.length).toBe(1);
    const [request] = apiCall.mock.calls[0] as [Record<string, unknown>];
    expect(request).toMatchObject({
      authIndex: 'idx-c',
      method: 'POST',
      url: 'https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume',
      header: {
        Authorization: 'Bearer $TOKEN$',
        'Content-Type': 'application/json',
        'Chatgpt-Account-Id': 'acct-123',
      },
    });
    expect(Object.keys(JSON.parse(request.data as string))).toEqual(['redeem_request_id']);
    expect(post.mock.calls).toEqual([['/credentials/quota/poll', { auth_index: 'idx-c' }]]);
    expect(data).toEqual(snapshot);
  });

  test('a refused spend does not poll', async () => {
    post = spyOn(apiClient, 'post');
    apiCall.mockResolvedValue({ statusCode: 409, header: {}, bodyText: 'no credit', body: null });

    await expect(QUOTA_ADAPTERS.codex.resetQuota?.(codexFile, t)).rejects.toMatchObject({
      status: 409,
    });
    expect(post).not.toHaveBeenCalled();
  });

  test('is offered only while the snapshot has an available credit', () => {
    const snapshot = codexSnapshot();
    expect(QUOTA_ADAPTERS.codex.canResetQuota?.(snapshot)).toBe(true);
    if (snapshot.extras?.kind === 'codex') snapshot.extras.reset_credits.value = null;
    expect(QUOTA_ADAPTERS.codex.canResetQuota?.(snapshot)).toBe(false);
    expect(QUOTA_ADAPTERS.claude.canResetQuota).toBeUndefined();
  });
});
