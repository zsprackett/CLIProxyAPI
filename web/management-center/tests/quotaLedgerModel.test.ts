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
import type { AuthFileItem } from '../src/types';

const NOW = Date.parse('2026-09-10T12:00:00Z');
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const unix = (ms: number) => String(Math.floor(ms / 1000));
const iso = (ms: number) => new Date(ms).toISOString();

const claudeFile = (extra: Partial<AuthFileItem> = {}): AuthFileItem => ({
  name: 'claude-tom@example.dev.json',
  type: 'claude',
  ...extra,
});

describe('observed Claude quota', () => {
  test('reads utilization and resets from the recorded headers', () => {
    const file = claudeFile({
      quota: {
        observed_at: iso(NOW - HOUR),
        signals: {
          'Anthropic-Ratelimit-Unified-5h-Utilization': '0.25',
          'Anthropic-Ratelimit-Unified-5h-Reset': unix(NOW + 2 * HOUR),
          'Anthropic-Ratelimit-Unified-7d-Utilization': '0.6',
          'Anthropic-Ratelimit-Unified-7d-Reset': unix(NOW + 3 * DAY),
        },
      },
    });

    const data = observedLedgerData('claude', file, NOW);
    expect(data?.source).toBe('observed');
    expect(data?.observedAtMs).toBe(NOW - HOUR);
    expect(data?.windows.map((w) => [w.id, w.remaining, w.resetAtMs])).toEqual([
      ['five-hour', 75, Math.floor((NOW + 2 * HOUR) / 1000) * 1000],
      ['seven-day', 40, Math.floor((NOW + 3 * DAY) / 1000) * 1000],
    ]);
  });

  test('takes the Fable window from a model-scoped snapshot', () => {
    const file = claudeFile({
      quota: {
        observed_at: iso(NOW - HOUR),
        signals: { 'Anthropic-Ratelimit-Unified-7d-Utilization': '0.1' },
      },
      model_quotas: {
        'claude-fable-5': {
          observed_at: iso(NOW - 2 * HOUR),
          signals: {
            'Anthropic-Ratelimit-Unified-7d_oi-Utilization': '0.42',
            'Anthropic-Ratelimit-Unified-7d_oi-Reset': unix(NOW + DAY),
          },
        },
      },
    });

    const fable = observedLedgerData('claude', file, NOW)?.windows.find(
      (w) => w.id === 'seven-day-fable'
    );
    expect(fable?.remaining).toBe(58);
    expect(fable?.labelKey).toBe('claude_quota.seven_day_fable');
  });

  test('treats a window that reset after observation as full', () => {
    const file = claudeFile({
      quota: {
        observed_at: iso(NOW - 6 * HOUR),
        signals: {
          'Anthropic-Ratelimit-Unified-5h-Utilization': '1',
          'Anthropic-Ratelimit-Unified-5h-Reset': unix(NOW - HOUR),
        },
      },
    });

    const [window] = observedLedgerData('claude', file, NOW)?.windows ?? [];
    expect(window.remaining).toBe(100);
    expect(window.resetAtMs).toBeNull();
  });

  test('returns null without recorded signals', () => {
    expect(observedLedgerData('claude', claudeFile(), NOW)).toBeNull();
    expect(
      observedLedgerData(
        'claude',
        claudeFile({ quota: { observed_at: iso(NOW), signals: {} } }),
        NOW
      )
    ).toBeNull();
  });
});

describe('observed Codex quota', () => {
  test('reads both windows, relative resets, and the plan', () => {
    const file: AuthFileItem = {
      name: 'codex-a@example.com.json',
      type: 'codex',
      quota: {
        observed_at: iso(NOW - HOUR),
        signals: {
          'X-Codex-Plan-Type': 'Pro',
          'X-Codex-Primary-Used-Percent': '20',
          'X-Codex-Primary-Window-Minutes': '300',
          'X-Codex-Primary-Reset-After-Seconds': '7200',
          'X-Codex-Secondary-Used-Percent': '83',
          'X-Codex-Secondary-Window-Minutes': '10080',
          'X-Codex-Secondary-Reset-At': unix(NOW + 2 * DAY),
        },
      },
    };

    const data = observedLedgerData('codex', file, NOW);
    expect(data?.planType).toBe('pro');
    expect(data?.windows.map((w) => [w.id, w.remaining, w.resetAtMs])).toEqual([
      ['five-hour', 80, NOW + HOUR],
      ['weekly', 17, Math.floor((NOW + 2 * DAY) / 1000) * 1000],
    ]);
  });
});

describe('observed xAI quota', () => {
  const xaiFile = (signals: Record<string, string>): AuthFileItem => ({
    name: 'xai-a@example.com.json',
    type: 'xai',
    quota: { observed_at: iso(NOW - HOUR), signals },
  });

  test('reads the polled billing window', () => {
    const data = observedLedgerData(
      'xai',
      xaiFile({
        'X-Xai-Billing-Period-Type': 'weekly',
        'X-Xai-Billing-Used-Percent': '37.5',
        'X-Xai-Billing-Reset-At': unix(NOW + 2 * DAY),
        'X-Xai-Billing-Window-Minutes': '10080',
      }),
      NOW
    );
    expect(data?.source).toBe('observed');
    expect(data?.observedAtMs).toBe(NOW - HOUR);
    expect(data?.windows).toEqual([
      {
        id: 'xai-weekly',
        labelKey: 'quota_management.ledger_window_weekly',
        remaining: 62.5,
        resetAtMs: Math.floor((NOW + 2 * DAY) / 1000) * 1000,
        periodHours: 168,
      },
    ]);
  });

  test('defaults to the monthly period and ignores snapshots without billing signals', () => {
    const [window] =
      observedLedgerData('xai', xaiFile({ 'X-Xai-Billing-Used-Percent': '90' }), NOW)?.windows ??
      [];
    expect(window.id).toBe('xai-monthly');
    expect(window.remaining).toBe(10);
    expect(observedLedgerData('xai', xaiFile({ 'Retry-After': '60' }), NOW)).toBeNull();
  });
});

describe('resolveLedgerRow', () => {
  const file = claudeFile({
    quota: {
      observed_at: iso(NOW - HOUR),
      signals: { 'Anthropic-Ratelimit-Unified-7d-Utilization': '0.5' },
    },
  });

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

  test('overlays an observed snapshot recorded after the live fetch', () => {
    const live = {
      status: 'success',
      planType: 'plan_max',
      fetchedAtMs: NOW - 2 * HOUR,
      windows: [
        { id: 'seven-day', usedPercent: 10, resetAtMs: NOW + DAY, periodHours: 168 },
        { id: 'seven-day-opus', usedPercent: 30, resetAtMs: NOW + DAY, periodHours: 168 },
      ],
    };
    const row = resolveLedgerRow('claude', file, live, NOW);
    expect(row.source).toBe('observed');
    expect(row.observedAtMs).toBe(NOW - HOUR);
    expect(row.planType).toBe('plan_max');
    expect(row.windows.map((w) => [w.id, w.remaining])).toEqual([
      ['seven-day', 50],
      ['seven-day-opus', 70],
    ]);
  });

  test('keeps a live reading fetched after the observed snapshot', () => {
    const live = {
      status: 'success',
      fetchedAtMs: NOW - 30 * 60 * 1000,
      windows: [{ id: 'seven-day', usedPercent: 10, resetAtMs: NOW + DAY, periodHours: 168 }],
    };
    const row = resolveLedgerRow('claude', file, live, NOW);
    expect(row.source).toBe('live');
    expect(row.windows[0].remaining).toBe(90);
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
