/**
 * Which row on a quota card recovers first, per provider.
 */

import { describe, expect, test } from 'bun:test';
import {
  XAI_WEEKLY_ROW_ID,
  collectQuotaRowInstants,
  nextRecoveryMs,
  pickSoonestRowId,
  pickUrgentRowId,
  resetCreditRowId,
} from '@/features/quota/resetSchedule';
import type { CodexResetCredit, QuotaSnapshot } from '@/features/quota/snapshot/types';
import { DAY_MS, HOUR_MS } from '@/utils/time/durations';

const NOW = new Date(2026, 7, 2, 12).getTime();
const iso = (ms: number) => new Date(ms).toISOString();

const section = <T>(value: T) => ({
  status: 'ok' as const,
  value,
  value_at: null,
  checked_at: null,
});

/** A snapshot with windows `[id, reset_at]` and, for Codex, reset credits. */
const snapshot = (
  provider: 'claude' | 'codex',
  windows: [string, string | null][],
  credits: CodexResetCredit[] = []
): QuotaSnapshot => ({
  provider,
  windows: windows.map(([id, resetAt]) => ({
    id,
    class: '',
    used_percent: 10,
    limit_reached: false,
    reset_at: resetAt,
    period_seconds: null,
    section: `${provider}.usage`,
    source: 'poll',
    observed_at: iso(NOW),
  })),
  extras:
    provider === 'codex'
      ? {
          kind: 'codex',
          plan: section('plus'),
          access: section({ allowed: true, limit_reached: false }),
          credits: section({ has_credits: null, unlimited: null, balance: null }),
          subscription: section({ active_until: null }),
          reset_credits: section({
            available: credits.length,
            applicable_available: null,
            credits,
          }),
        }
      : null,
  last_poll: null,
  updated_at: iso(NOW),
});

const credit = (id: string, status: string, expiresAt: string): CodexResetCredit => ({
  id,
  status,
  granted_at: null,
  expires_at: expiresAt,
});

const claudeSnapshot = snapshot('claude', [
  ['five_hour', iso(NOW + 3 * HOUR_MS)],
  ['seven_day', iso(NOW + 4 * DAY_MS)],
]);

const codexCredits = [
  credit('credit-a', 'available', iso(NOW + 11 * DAY_MS)),
  credit('credit-b', 'available', iso(NOW + 2 * DAY_MS)),
];

const codexSnapshot = snapshot(
  'codex',
  [
    ['primary', iso(NOW + 3 * HOUR_MS)],
    ['secondary', iso(NOW + 6 * DAY_MS)],
  ],
  codexCredits
);

describe('collectQuotaRowInstants', () => {
  test('collects every Claude window', () => {
    expect(collectQuotaRowInstants('claude', undefined, claudeSnapshot)).toEqual([
      { rowId: 'five_hour', atMs: NOW + 3 * HOUR_MS, kind: 'window' },
      { rowId: 'seven_day', atMs: NOW + 4 * DAY_MS, kind: 'window' },
    ]);
  });

  test('collects Codex windows and available reset credits together', () => {
    const instants = collectQuotaRowInstants('codex', undefined, codexSnapshot);
    expect(instants).toHaveLength(4);
    expect(instants.filter((i) => i.kind === 'credit').map((i) => i.rowId)).toEqual([
      'credit-a',
      'credit-b',
    ]);
  });

  test('ignores reset credits that are not available', () => {
    const consumed = snapshot(
      'codex',
      [],
      [
        credit('used', 'consumed', iso(NOW + HOUR_MS)),
        credit('live', 'available', iso(NOW + 2 * HOUR_MS)),
      ]
    );
    expect(
      collectQuotaRowInstants('codex', undefined, consumed)
        .filter((i) => i.kind === 'credit')
        .map((i) => i.rowId)
    ).toEqual(['live']);
  });

  test('collects the xAI weekly window', () => {
    const quota = { status: 'success', billing: { periodType: 'weekly', resetAtMs: NOW + DAY_MS } };
    expect(collectQuotaRowInstants('xai', quota)).toEqual([
      { rowId: XAI_WEEKLY_ROW_ID, atMs: NOW + DAY_MS, kind: 'window' },
    ]);
  });

  test('ignores an xAI monthly summary — a billing cycle is not capacity returning', () => {
    const quota = {
      status: 'success',
      billing: { periodType: 'monthly', resetAtMs: NOW + DAY_MS },
    };
    expect(collectQuotaRowInstants('xai', quota)).toEqual([]);
  });

  test('flattens Antigravity buckets across groups', () => {
    const quota = {
      status: 'success',
      groups: [
        { id: 'g1', buckets: [{ id: 'b1', resetAtMs: NOW + HOUR_MS }] },
        { id: 'g2', buckets: [{ id: 'b2', resetAtMs: NOW + 2 * HOUR_MS }] },
      ],
    };
    expect(collectQuotaRowInstants('antigravity', quota).map((i) => i.rowId)).toEqual(['b1', 'b2']);
  });

  test('collects Kimi rows', () => {
    const quota = { status: 'success', rows: [{ id: 'r1', resetAtMs: NOW + HOUR_MS }] };
    expect(collectQuotaRowInstants('kimi', quota).map((i) => i.rowId)).toEqual(['r1']);
  });

  test('returns nothing without a snapshot or a loaded live state', () => {
    for (const status of ['idle', 'loading', 'error']) {
      expect(collectQuotaRowInstants('kimi', { status, rows: [] })).toEqual([]);
    }
    expect(collectQuotaRowInstants('claude', undefined)).toEqual([]);
    expect(collectQuotaRowInstants('claude', { status: 'idle' }, null)).toEqual([]);
  });

  test('drops rows with no usable instant rather than emitting NaN', () => {
    const quota = snapshot('claude', [
      ['ok', iso(NOW + HOUR_MS)],
      ['null', null],
      ['garbage', 'not a date'],
    ]);
    expect(collectQuotaRowInstants('claude', undefined, quota).map((i) => i.rowId)).toEqual(['ok']);
  });

  test('drops a reset credit whose expiry will not parse', () => {
    const quota = snapshot('codex', [], [credit('bad', 'available', 'not a date')]);
    expect(collectQuotaRowInstants('codex', undefined, quota)).toEqual([]);
  });
});

describe('pickSoonestRowId', () => {
  test('picks the nearest upcoming instant', () => {
    expect(
      pickSoonestRowId(collectQuotaRowInstants('claude', undefined, claudeSnapshot), NOW)
    ).toBe('five_hour');
  });

  test('a credit expiring before every window wins the emphasis', () => {
    const quota = snapshot('codex', [['primary', iso(NOW + 5 * DAY_MS)]], codexCredits);
    expect(pickSoonestRowId(collectQuotaRowInstants('codex', undefined, quota), NOW)).toBe(
      'credit-b'
    );
  });

  test('a window resetting before every credit wins the emphasis', () => {
    expect(pickSoonestRowId(collectQuotaRowInstants('codex', undefined, codexSnapshot), NOW)).toBe(
      'primary'
    );
  });

  test('skips instants that have already passed', () => {
    const instants = [
      { rowId: 'past', atMs: NOW - HOUR_MS, kind: 'window' as const },
      { rowId: 'exactly-now', atMs: NOW, kind: 'window' as const },
      { rowId: 'future', atMs: NOW + HOUR_MS, kind: 'window' as const },
    ];
    expect(pickSoonestRowId(instants, NOW)).toBe('future');
  });

  test('returns null when nothing is pending', () => {
    expect(pickSoonestRowId([], NOW)).toBeNull();
    expect(pickSoonestRowId([{ rowId: 'past', atMs: NOW - 1, kind: 'window' }], NOW)).toBeNull();
  });

  test('breaks ties deterministically on row id', () => {
    const a = [
      { rowId: 'b', atMs: NOW + HOUR_MS, kind: 'window' as const },
      { rowId: 'a', atMs: NOW + HOUR_MS, kind: 'window' as const },
    ];
    expect(pickSoonestRowId(a, NOW)).toBe('a');
    expect(pickSoonestRowId([...a].reverse(), NOW)).toBe('a');
  });
});

describe('pickUrgentRowId', () => {
  test('highlights only the nearest reset strictly inside the final hour', () => {
    const instants = [
      { rowId: 'later-urgent', atMs: NOW + 45 * 60_000, kind: 'window' as const },
      { rowId: 'nearest-urgent', atMs: NOW + 30 * 60_000, kind: 'window' as const },
      { rowId: 'past', atMs: NOW - 1, kind: 'window' as const },
    ];

    expect(pickUrgentRowId(instants, NOW)).toBe('nearest-urgent');
  });

  test('does not highlight at exactly one hour or beyond', () => {
    expect(
      pickUrgentRowId(
        [
          { rowId: 'exactly-one-hour', atMs: NOW + HOUR_MS, kind: 'window' },
          { rowId: 'later', atMs: NOW + 2 * HOUR_MS, kind: 'window' },
        ],
        NOW
      )
    ).toBeNull();
  });
});

describe('resetCreditRowId', () => {
  test('prefers the credit id', () => {
    expect(resetCreditRowId({ id: 'credit-a', expires_at: 'x' }, 3)).toBe('credit-a');
  });

  test('falls back to expiry and index when the payload carries no id', () => {
    // Must stay byte-identical to CodexQuotaBody's React key.
    expect(resetCreditRowId({ id: '', expires_at: '2026-08-13T00:00:00Z' }, 2)).toBe(
      '2026-08-13T00:00:00Z-2'
    );
    expect(resetCreditRowId({ expires_at: '2026-08-13T00:00:00Z' }, 0)).toBe(
      '2026-08-13T00:00:00Z-0'
    );
  });
});

describe('nextRecoveryMs', () => {
  test('returns the soonest upcoming instant across windows and credits', () => {
    expect(nextRecoveryMs('codex', undefined, NOW, codexSnapshot)).toBe(NOW + 3 * HOUR_MS);
  });

  test('ignores instants already in the past', () => {
    const quota = snapshot('claude', [
      ['stale', iso(NOW - DAY_MS)],
      ['live', iso(NOW + DAY_MS)],
    ]);
    expect(nextRecoveryMs('claude', undefined, NOW, quota)).toBe(NOW + DAY_MS);
  });

  test('is null for an unloaded credential, so sorting can sink it', () => {
    expect(nextRecoveryMs('claude', undefined, NOW)).toBeNull();
    expect(nextRecoveryMs('claude', { status: 'idle' }, NOW)).toBeNull();
    expect(nextRecoveryMs('claude', { status: 'error' }, NOW)).toBeNull();
  });

  test('is null when every known instant has passed', () => {
    const quota = snapshot('claude', [['stale', iso(NOW - 1)]]);
    expect(nextRecoveryMs('claude', undefined, NOW, quota)).toBeNull();
  });
});
