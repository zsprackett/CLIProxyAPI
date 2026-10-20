/**
 * A click stores the poll result by auth index. The card shows it until the
 * 60 s listing refresh carries a newer snapshot for the same auth index.
 */

import { beforeEach, describe, expect, test } from 'bun:test';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import { applyPolledSnapshot } from '@/features/quota/snapshot/windows';
import { useQuotaStore } from '@/stores/useQuotaStore';
import type { AuthFileItem } from '@/types';

const reading = (updatedAt: string, usedPercent: number): QuotaSnapshot => ({
  provider: 'codex',
  windows: [
    {
      id: 'primary',
      class: 'codex:5h',
      used_percent: usedPercent,
      limit_reached: false,
      reset_at: '2026-10-19T20:00:00Z',
      period_seconds: 18000,
      section: 'codex.usage',
      source: 'poll',
      observed_at: updatedAt,
    },
  ],
  extras: null,
  last_poll: { at: updatedAt, trigger: 'on_demand' },
  updated_at: updatedAt,
});

const listed = (authIndex: string, snapshot: QuotaSnapshot | null): AuthFileItem => ({
  name: `codex-${authIndex}.json`,
  type: 'codex',
  auth_index: authIndex,
  quota_snapshot: snapshot,
});

const shownPercent = (file: AuthFileItem) =>
  (
    applyPolledSnapshot(file, useQuotaStore.getState().quotaSnapshots).quota_snapshot as
      QuotaSnapshot | null | undefined
  )?.windows[0]?.used_percent;

beforeEach(() => {
  useQuotaStore.getState().clearQuotaCache();
});

describe('quota snapshot store', () => {
  test('a poll result for an auth index the listing does not hold yet is kept', () => {
    const polled = reading('2026-10-19T18:00:00Z', 40);
    useQuotaStore.getState().setQuotaSnapshot('new-index', 'codex-new-index.json', polled);

    useQuotaStore.getState().reconcileQuotaSnapshots([listed('other-index', null)]);

    expect(useQuotaStore.getState().quotaSnapshots).toEqual({
      'new-index': { name: 'codex-new-index.json', snapshot: polled },
    });
    expect(shownPercent(listed('new-index', null))).toBe(40);
  });

  test('a listing refresh with an older updated_at does not replace the poll result', () => {
    useQuotaStore
      .getState()
      .setQuotaSnapshot('a', 'codex-a.json', reading('2026-10-19T18:00:00Z', 40));
    const file = listed('a', reading('2026-10-19T17:55:00Z', 10));

    useQuotaStore.getState().reconcileQuotaSnapshots([file]);

    expect(shownPercent(file)).toBe(40);
  });

  test('a listing refresh with a newer updated_at wins and drops the poll result', () => {
    useQuotaStore
      .getState()
      .setQuotaSnapshot('a', 'codex-a.json', reading('2026-10-19T18:00:00Z', 40));
    const file = listed('a', reading('2026-10-19T18:01:00Z', 70));

    expect(shownPercent(file)).toBe(70);
    useQuotaStore.getState().reconcileQuotaSnapshots([file]);
    expect(useQuotaStore.getState().quotaSnapshots).toEqual({});
  });

  test('clearing the quota cache drops poll results', () => {
    useQuotaStore
      .getState()
      .setQuotaSnapshot('a', 'codex-a.json', reading('2026-10-19T18:00:00Z', 40));
    useQuotaStore.getState().clearQuotaCache();
    expect(shownPercent(listed('a', null))).toBeUndefined();
    expect(shownPercent(listed('a', reading('2026-10-19T17:00:00Z', 5)))).toBe(5);
  });
});
