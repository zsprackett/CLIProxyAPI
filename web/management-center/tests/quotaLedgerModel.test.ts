import { describe, expect, test } from 'bun:test';
import {
  effectiveRemaining,
  isCredentialCoolingDown,
  maskIdentity,
  observedLedgerData,
  predictSoonestResetPick,
  resolveLedgerRow,
  summarizeProvider,
  weeklyWindow,
  type LedgerCredential,
  type LedgerWindow,
} from '../src/features/quota/ledgerModel';
import type { QuotaSnapshot, QuotaWindow } from '../src/features/quota/snapshot/types';
import type { AuthFileItem } from '../src/types';

const NOW = Date.parse('2026-09-10T12:00:00Z');
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const iso = (ms: number) => new Date(ms).toISOString();

const snapWindow = (overrides: Partial<QuotaWindow>): QuotaWindow => ({
  id: 'seven_day',
  class: 'claude:7d',
  used_percent: null,
  limit_reached: false,
  reset_at: null,
  period_seconds: 604800,
  section: 'headers',
  source: 'headers',
  observed_at: iso(NOW - HOUR),
  ...overrides,
});

const snapshot = (
  provider: QuotaSnapshot['provider'],
  windows: QuotaWindow[],
  extras: QuotaSnapshot['extras'] = null
): QuotaSnapshot => ({
  provider,
  windows,
  extras,
  last_poll: null,
  updated_at: iso(NOW - HOUR),
});

const planSection = (value: string) => ({
  status: 'ok' as const,
  value,
  value_at: iso(NOW - HOUR),
  checked_at: iso(NOW - HOUR),
});

const absent = { status: 'absent' as const, value: null, value_at: null, checked_at: null };

const claudeFile = (quotaSnapshot?: QuotaSnapshot): AuthFileItem => ({
  name: 'claude-tom@example.dev.json',
  type: 'claude',
  quota_snapshot: quotaSnapshot,
});

describe('observed Claude quota', () => {
  test('reads used percents and resets from the snapshot', () => {
    const file = claudeFile(
      snapshot('claude', [
        snapWindow({
          id: 'five_hour',
          class: 'claude:5h',
          used_percent: 25,
          reset_at: iso(NOW + 2 * HOUR),
          period_seconds: 18000,
        }),
        snapWindow({
          used_percent: 60,
          reset_at: iso(NOW + 3 * DAY),
          observed_at: iso(NOW - 2 * HOUR),
        }),
      ])
    );

    const data = observedLedgerData('claude', file, NOW);
    expect(data?.source).toBe('observed');
    expect(data?.observedAtMs).toBe(NOW - HOUR);
    expect(data?.windows.map((w) => [w.id, w.remaining, w.resetAtMs, w.periodHours])).toEqual([
      ['five_hour', 75, NOW + 2 * HOUR, 5],
      ['seven_day', 40, NOW + 3 * DAY, 168],
    ]);
  });

  test('labels the Fable-scoped window and reads the plan from extras', () => {
    const file = claudeFile(
      snapshot(
        'claude',
        [
          snapWindow({
            id: 'seven_day_fable',
            class: 'claude:7d_oi',
            model_scope: 'fable',
            used_percent: 42,
            reset_at: iso(NOW + DAY),
          }),
        ],
        { kind: 'claude', plan: planSection('plan_max'), extra_usage: absent }
      )
    );

    const data = observedLedgerData('claude', file, NOW);
    expect(data?.planType).toBe('plan_max');
    expect(data?.windows[0].remaining).toBe(58);
    expect(data?.windows[0].labelKey).toBe('claude_quota.seven_day_fable');
  });

  test('treats a window that reset after observation as full', () => {
    const file = claudeFile(
      snapshot('claude', [
        snapWindow({
          id: 'five_hour',
          used_percent: 100,
          limit_reached: true,
          reset_at: iso(NOW - HOUR),
        }),
      ])
    );

    const [window] = observedLedgerData('claude', file, NOW)?.windows ?? [];
    expect(window.remaining).toBe(100);
    expect(window.resetAtMs).toBeNull();
    expect(window.limitReached).toBe(false);
  });

  test('returns null without a snapshot or without windows', () => {
    expect(observedLedgerData('claude', claudeFile(), NOW)).toBeNull();
    expect(observedLedgerData('claude', claudeFile(snapshot('claude', [])), NOW)).toBeNull();
    expect(observedLedgerData('claude', claudeFile(snapshot('', [])), NOW)).toBeNull();
  });
});

describe('observed Codex quota', () => {
  test('reads both windows by class and the plan', () => {
    const file: AuthFileItem = {
      name: 'codex-a@example.com.json',
      type: 'codex',
      quota_snapshot: snapshot(
        'codex',
        [
          snapWindow({
            id: 'primary',
            class: 'codex:5h',
            used_percent: 20,
            reset_at: iso(NOW + HOUR),
            period_seconds: 18000,
            section: 'codex.usage',
          }),
          snapWindow({
            id: 'secondary',
            class: 'codex:weekly',
            used_percent: 83,
            reset_at: iso(NOW + 2 * DAY),
            section: 'codex.usage',
          }),
        ],
        {
          kind: 'codex',
          plan: planSection('Pro'),
          access: absent,
          credits: absent,
          subscription: absent,
          reset_credits: absent,
        }
      ),
    };

    const data = observedLedgerData('codex', file, NOW);
    expect(data?.planType).toBe('pro');
    expect(data?.windows.map((w) => [w.id, w.labelKey, w.remaining, w.resetAtMs])).toEqual([
      ['primary', 'codex_quota.primary_window', 80, NOW + HOUR],
      ['secondary', 'codex_quota.secondary_window', 17, NOW + 2 * DAY],
    ]);
    expect(weeklyWindow(data?.windows ?? [])?.id).toBe('secondary');
  });
});

describe('codex weekly summary', () => {
  test('finds the weekly window by class, not by period', () => {
    const file: AuthFileItem = {
      name: 'codex-w@example.com.json',
      type: 'codex',
      quota_snapshot: snapshot(
        'codex',
        [
          snapWindow({
            id: 'additional.primary',
            class: '',
            used_percent: 10,
            reset_at: iso(NOW + 3 * HOUR),
            period_seconds: 604800,
            section: 'codex.usage',
          }),
          snapWindow({
            id: 'secondary',
            class: 'codex:weekly',
            used_percent: 40,
            reset_at: iso(NOW + 2 * DAY),
            period_seconds: null,
            section: 'codex.usage',
          }),
        ],
        {
          kind: 'codex',
          plan: planSection('Pro'),
          access: absent,
          credits: absent,
          subscription: absent,
          reset_credits: absent,
        }
      ),
    };
    const row = observedLedgerData('codex', file, NOW);
    const summary = summarizeProvider(
      'codex',
      [{ ...credential('w', []), provider: 'codex', row: row! }],
      NOW
    );
    expect(summary.soonestWeeklyResetMs).toBe(NOW + 2 * DAY);
  });
});

describe('observed xAI quota', () => {
  test('reads the weekly and monthly windows', () => {
    const file: AuthFileItem = {
      name: 'xai-a@example.com.json',
      type: 'xai',
      quota_snapshot: snapshot('xai', [
        snapWindow({
          id: 'weekly',
          class: 'xai:weekly',
          used_percent: 37.5,
          reset_at: iso(NOW + 2 * DAY),
          section: 'xai.weekly',
        }),
        snapWindow({
          id: 'monthly',
          class: 'xai:monthly',
          used_percent: 90,
          reset_at: iso(NOW + 20 * DAY),
          period_seconds: 30 * 86400,
          section: 'xai.monthly',
        }),
      ]),
    };
    const data = observedLedgerData('xai', file, NOW);
    expect(data?.source).toBe('observed');
    expect(data?.windows).toEqual([
      {
        id: 'weekly',
        class: 'xai:weekly',
        labelKey: 'quota_management.ledger_window_weekly',
        remaining: 62.5,
        resetAtMs: NOW + 2 * DAY,
        periodHours: 168,
        limitReached: false,
        staleSinceMs: null,
      },
      {
        id: 'monthly',
        class: 'xai:monthly',
        labelKey: 'quota_management.ledger_window_monthly',
        remaining: 10,
        resetAtMs: NOW + 20 * DAY,
        periodHours: 720,
        limitReached: false,
        staleSinceMs: null,
      },
    ]);
  });
});

describe('resolveLedgerRow', () => {
  const file = claudeFile(snapshot('claude', [snapWindow({ used_percent: 50 })]));

  test('prefers a loaded live reading over the observed snapshot', () => {
    const row = resolveLedgerRow(
      'claude',
      file,
      {
        status: 'success',
        planType: 'plan_max',
        windows: [{ id: 'seven-day', usedPercent: 10, resetAtMs: NOW + DAY, periodHours: 168 }],
      },
      NOW
    );
    expect(row.source).toBe('live');
    expect(row.planType).toBe('plan_max');
    expect(row.windows[0].remaining).toBe(90);
  });

  test('falls back to the observed snapshot while live data is not loaded', () => {
    expect(resolveLedgerRow('claude', file, { status: 'error', windows: [] }, NOW).source).toBe(
      'observed'
    );
    expect(resolveLedgerRow('claude', file, undefined, NOW).windows[0].remaining).toBe(50);
  });

  const liveTwoWindows = (fetchedAtMs: number) => ({
    status: 'success',
    planType: 'plan_max',
    fetchedAtMs,
    windows: [
      { id: 'seven-day', usedPercent: 10, resetAtMs: NOW + DAY, periodHours: 168 },
      { id: 'seven-day-opus', usedPercent: 30, resetAtMs: NOW + DAY, periodHours: 168 },
    ],
  });

  const newerSnapshotFile = claudeFile({
    ...snapshot('claude', [snapWindow({ used_percent: 50, observed_at: iso(NOW - 3 * HOUR) })], {
      kind: 'claude',
      plan: planSection('plan_pro'),
      extra_usage: absent,
    }),
    updated_at: iso(NOW - HOUR),
  });

  test('a snapshot updated after the live fetch replaces the whole live reading', () => {
    const leaked = resolveLedgerRow('claude', file, liveTwoWindows(NOW - 2 * HOUR), NOW);
    expect(leaked.windows.map((w) => [w.id, w.remaining])).toEqual([['seven_day', 50]]);

    const row = resolveLedgerRow('claude', newerSnapshotFile, liveTwoWindows(NOW - 2 * HOUR), NOW);
    expect(row.source).toBe('observed');
    expect(row.planType).toBe('plan_pro');
    expect(row.windows.map((w) => [w.id, w.remaining])).toEqual([['seven_day', 50]]);
  });

  test('a live reading fetched after the snapshot update replaces the whole snapshot', () => {
    const row = resolveLedgerRow(
      'claude',
      newerSnapshotFile,
      liveTwoWindows(NOW - 30 * 60 * 1000),
      NOW
    );
    expect(row.source).toBe('live');
    expect(row.planType).toBe('plan_max');
    expect(row.windows.map((w) => [w.id, w.remaining])).toEqual([
      ['seven-day', 90],
      ['seven-day-opus', 70],
    ]);
  });

  test('reports nothing for providers without data', () => {
    expect(resolveLedgerRow('kimi', { name: 'kimi.json' }, undefined, NOW).source).toBe('none');
  });
});

const window = (overrides: Partial<LedgerWindow>): LedgerWindow => ({
  id: 'seven-day',
  remaining: 100,
  resetAtMs: null,
  periodHours: 168,
  ...overrides,
});

const credential = (
  name: string,
  windows: LedgerWindow[],
  overrides: Partial<LedgerCredential> = {}
): LedgerCredential => ({
  key: name,
  name,
  provider: 'claude',
  disabled: false,
  coolingDown: false,
  row: { source: 'live', windows, planType: null, observedAtMs: null },
  ...overrides,
});

describe('derived figures', () => {
  test('effective remaining is the tightest known window', () => {
    expect(
      effectiveRemaining([window({ remaining: 90 }), window({ id: 'five-hour', remaining: 0 })])
    ).toBe(0);
    expect(effectiveRemaining([window({ remaining: null })])).toBeNull();
  });

  test('weekly window prefers the account-wide one', () => {
    const fable = window({ id: 'seven-day-fable', resetAtMs: NOW + HOUR });
    const shared = window({ id: 'seven-day', resetAtMs: NOW + DAY });
    expect(weeklyWindow([fable, shared])).toBe(shared);
    expect(weeklyWindow([fable])).toBe(fable);
  });

  test('summarizes usable credentials and quota about to expire', () => {
    const summary = summarizeProvider(
      'claude',
      [
        credential('a', [window({ remaining: 58, resetAtMs: NOW + 20 * HOUR })]),
        credential('b', [window({ remaining: 100, resetAtMs: NOW + 4 * DAY })]),
        credential('c', [
          window({ remaining: 51, resetAtMs: NOW + 10 * HOUR }),
          window({ id: 'five-hour', remaining: 0, periodHours: 5 }),
        ]),
        credential('d', []),
      ],
      NOW
    );

    expect(summary.credentials).toBe(4);
    expect(summary.usable).toBe(2);
    expect(summary.unknown).toBe(1);
    expect(summary.segments).toEqual([58, 100, 0, null]);
    expect(summary.soonestWeeklyResetMs).toBe(NOW + 10 * HOUR);
    expect(summary.expiringSoon).toBeCloseTo(1.09, 5);
  });
});

describe('predictSoonestResetPick', () => {
  test('prefers a credential with no known reset, then the soonest reset', () => {
    const soon = credential('b', [window({ resetAtMs: NOW + HOUR })]);
    const later = credential('a', [window({ resetAtMs: NOW + DAY })]);
    expect(predictSoonestResetPick([later, soon], NOW)).toBe('b');
    expect(predictSoonestResetPick([later, soon, credential('z', [])], NOW)).toBe('z');
  });

  test('skips drained, disabled, and cooling credentials; ties resolve by name', () => {
    const at = NOW + HOUR;
    expect(
      predictSoonestResetPick(
        [
          credential('a', [window({ resetAtMs: at, remaining: 0 })]),
          credential('b', [window({ resetAtMs: at })], { coolingDown: true }),
          credential('c', [window({ resetAtMs: at })], { disabled: true }),
          credential('e', [window({ resetAtMs: at })]),
          credential('d', [window({ resetAtMs: at })]),
        ],
        NOW
      )
    ).toBe('d');
  });
});

test('credential cooldowns are read from the snapshot', () => {
  const file: AuthFileItem = {
    name: 'x.json',
    cooldownSnapshot: {
      receivedAtMs: NOW,
      records: [
        { scope: 'model', reason: 'quota', retryAt: '', remainingSeconds: 600 },
        { scope: 'credential', reason: 'quota', retryAt: '', remainingSeconds: 60 },
      ],
    },
  };
  expect(isCredentialCoolingDown(file, NOW + 30_000)).toBe(true);
  expect(isCredentialCoolingDown(file, NOW + 120_000)).toBe(false);
});

test('maskIdentity hides the identifying parts of an email', () => {
  expect(maskIdentity('claude-tom@example.dev.json')).toBe('claude-t•••@e•••.dev.json');
  expect(maskIdentity('codex-alice@corp.io.json')).toBe('codex-a•••@c•••.io.json');
  expect(maskIdentity('bob@example.com')).toBe('b•••@e•••.com');
  expect(maskIdentity('no-email-here.json')).toBe('no-email-here.json');
});
