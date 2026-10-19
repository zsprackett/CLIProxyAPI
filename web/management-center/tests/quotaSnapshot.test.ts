import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { observedLedgerData, resolveLedgerRow } from '@/features/quota/ledgerModel';
import { buildTimelineLane } from '@/features/quota/quotaTimelineModel';
import { sortQuotaEntries } from '@/features/quota/logic';
import { nextRecoveryMs } from '@/features/quota/resetSchedule';
import type { QuotaSnapshot, QuotaWindow } from '@/features/quota/snapshot/types';
import { LedgerWindowList, WindowList } from '@/features/quota/snapshot/WindowList';
import { readQuotaSnapshot } from '@/features/quota/snapshot/windows';
import type { AuthFileItem } from '@/types';

const GOLDEN_PATH = new URL(
  '../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json',
  import.meta.url
);

const NOW = Date.parse('2026-10-19T18:00:00Z');
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const iso = (ms: number) => new Date(ms).toISOString();

const golden: QuotaSnapshot = JSON.parse(readFileSync(GOLDEN_PATH, 'utf8'));

const render = (snapshot: QuotaSnapshot) => {
  const known = readQuotaSnapshot(snapshot.provider, snapshot);
  if (!known) throw new Error('snapshot has no windows');
  return renderToStaticMarkup(
    createElement(WindowList, {
      provider: known.provider,
      snapshot: known,
      windows: known.windows,
      nowMs: NOW,
    })
  );
};

const barCount = (markup: string) => (markup.match(/width:/g) ?? []).length;

/** The visible text of each rendered window, in order: label, percent, then the rest. */
const renderedWindows = (markup: string) =>
  markup
    .split('<div class="window">')
    .slice(1)
    .map((chunk) => {
      const text = (className: string) =>
        new RegExp(`<span class="${className}">([^<]*)</span>`).exec(chunk)?.[1] ?? null;
      return {
        label: text('windowLabel'),
        percent: text('windowPercent'),
        limitReached: chunk.includes('Limit reached'),
      };
    });

let originalLanguage = 'en';
beforeAll(async () => {
  originalLanguage = i18n.language;
  await i18n.changeLanguage('en');
});
afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});

describe('Go golden snapshot', () => {
  test('matches the TS contract', () => {
    expect(Object.keys(golden).sort()).toEqual(
      ['extras', 'last_poll', 'provider', 'updated_at', 'windows'].sort()
    );
    const required: (keyof QuotaWindow)[] = [
      'id',
      'class',
      'used_percent',
      'limit_reached',
      'reset_at',
      'period_seconds',
      'section',
      'source',
      'observed_at',
    ];
    for (const window of golden.windows) {
      for (const key of required) expect(window).toHaveProperty(key);
    }
    expect(golden.extras?.kind).toBe('claude');
    if (golden.extras?.kind !== 'claude') return;
    for (const section of [golden.extras.plan, golden.extras.extra_usage]) {
      for (const key of ['status', 'value', 'value_at', 'checked_at']) {
        expect(section).toHaveProperty(key);
      }
    }
  });

  test('builds a ledger row with the remaining percents', () => {
    const file: AuthFileItem = { name: 'claude-a.json', type: 'claude', quota_snapshot: golden };
    const row = observedLedgerData('claude', file, NOW);
    expect(row?.source).toBe('observed');
    expect(row?.planType).toBe('plan_max');
    expect(row?.observedAtMs).toBe(Date.parse('2026-10-19T17:30:00Z'));
    expect(row?.windows.map((w) => [w.id, w.labelKey, w.remaining])).toEqual([
      ['five_hour', 'claude_quota.five_hour', 75],
      ['seven_day', 'claude_quota.seven_day', 39.5],
      ['seven_day_fable', 'claude_quota.seven_day_fable', null],
    ]);
  });

  test('renders the labels, percents, and unknown Fable window', () => {
    const markup = render(golden);
    expect(markup).toContain('5-hour limit');
    expect(markup).toContain('7-day limit');
    expect(markup).toContain('7-day Fable 5');
    expect(markup).toContain('75%');
    expect(markup).toContain('40%');
    expect(markup).toContain('Unknown');
    expect(barCount(markup)).toBe(2);
    expect(markup).not.toContain('width:0%');
    expect(markup).not.toContain('Stale since');
  });

  test('feeds the timeline and the recovery sort without a live reading', () => {
    const session = buildTimelineLane({
      name: 'a',
      displayName: 'a',
      provider: 'claude',
      quota: undefined,
      snapshot: golden,
      maxPeriodHours: 5,
    });
    expect(session.anchorMs).toBe(Date.parse('2026-10-19T20:00:00Z'));
    expect(session.periodHours).toBe(5);
    expect(session.remaining).toBe(75);
    expect(session.limits.map((limit) => [limit.labelKey, limit.remaining])).toEqual([
      ['claude_quota.five_hour', 75],
      ['claude_quota.seven_day', 39.5],
    ]);

    expect(nextRecoveryMs('claude', undefined, NOW, golden)).toBe(
      Date.parse('2026-10-19T20:00:00Z')
    );
    expect(nextRecoveryMs('claude', { status: 'idle' }, NOW, null)).toBeNull();
  });
});

const window = (overrides: Partial<QuotaWindow>): QuotaWindow => ({
  id: 'weekly',
  class: '',
  used_percent: null,
  limit_reached: false,
  reset_at: iso(NOW + 2 * DAY),
  period_seconds: 604800,
  section: 'headers',
  source: 'poll',
  observed_at: iso(NOW - HOUR),
  ...overrides,
});

const section = <T>(status: 'ok' | 'failed' | 'absent', value: T | null = null) => ({
  status,
  value,
  value_at: value === null ? null : iso(NOW - 3 * HOUR),
  checked_at: iso(NOW - HOUR),
});

describe('stale and exhausted windows', () => {
  const xai: QuotaSnapshot = {
    provider: 'xai',
    windows: [
      window({
        id: 'weekly',
        class: 'xai:weekly',
        used_percent: 42,
        section: 'xai.weekly',
        observed_at: iso(NOW - 3 * HOUR),
      }),
    ],
    extras: {
      kind: 'xai',
      access: 'grok_cli',
      weekly: { ...section('failed', null), error: 'HTTP 503' },
      monthly: section('absent'),
      account: section('absent'),
      paid_health: section('absent'),
    },
    last_poll: { at: iso(NOW - HOUR), trigger: 'background', error: 'HTTP 503' },
    updated_at: iso(NOW - HOUR),
  };

  test('an xAI weekly window with a failed section keeps its percent and says stale', () => {
    const markup = render(xai);
    expect(markup).toContain('Weekly limit');
    expect(markup).toContain('58%');
    expect(markup).toContain('Stale since 3 hours ago');
    expect(markup).not.toContain('--');

    const file: AuthFileItem = { name: 'xai.json', type: 'xai', quota_snapshot: xai };
    const [weekly] = observedLedgerData('xai', file, NOW)?.windows ?? [];
    expect(weekly.remaining).toBe(58);
    expect(weekly.staleSinceMs).toBe(NOW - 3 * HOUR);
  });

  test('the xAI monthly spend cap is not a recovery instant', () => {
    const monthlyOnly: QuotaSnapshot = {
      ...xai,
      windows: [window({ id: 'monthly', class: 'xai:monthly', section: 'xai.monthly' })],
    };
    expect(nextRecoveryMs('xai', undefined, NOW, monthlyOnly)).toBeNull();
  });

  test('a Codex window at its limit with no percent shows limit reached and 0%', () => {
    const codex: QuotaSnapshot = {
      provider: 'codex',
      windows: [
        window({
          id: 'primary',
          class: 'codex:5h',
          limit_reached: true,
          used_percent: null,
          reset_at: iso(NOW + 2 * HOUR),
          period_seconds: 18000,
          section: 'codex.usage',
        }),
      ],
      extras: null,
      last_poll: null,
      updated_at: iso(NOW - HOUR),
    };
    const markup = render(codex);
    expect(markup).toContain('5-hour limit');
    expect(markup).toContain('Limit reached');
    expect(markup).toContain('0%');
    expect(markup).not.toContain('Unknown');
  });

  test('a known percent wins over a namespace-wide limit_reached', () => {
    const codex: QuotaSnapshot = {
      provider: 'codex',
      windows: [
        window({
          id: 'primary',
          class: 'codex:5h',
          used_percent: 100,
          limit_reached: true,
          reset_at: iso(NOW + 2 * HOUR),
          period_seconds: 18000,
          section: 'codex.usage',
        }),
        window({
          id: 'secondary',
          class: 'codex:weekly',
          used_percent: 30,
          limit_reached: true,
          reset_at: iso(NOW + 3 * DAY),
          section: 'codex.usage',
        }),
      ],
      extras: null,
      last_poll: null,
      updated_at: iso(NOW - HOUR),
    };

    expect(renderedWindows(render(codex))).toEqual([
      { label: '5-hour limit', percent: '0%', limitReached: true },
      { label: 'Weekly limit', percent: '70%', limitReached: false },
    ]);

    const lane = buildTimelineLane({
      name: 'c',
      displayName: 'c',
      provider: 'codex',
      quota: undefined,
      snapshot: codex,
      maxPeriodHours: 14 * 24,
    });
    expect(lane.remaining).toBe(70);
    expect(lane.limits.map((limit) => [limit.labelKey, limit.remaining])).toEqual([
      ['codex_quota.primary_window', 0],
      ['codex_quota.secondary_window', 70],
    ]);
  });

  test('a passed reset settles a known percent to full and leaves an unknown one awaiting data', () => {
    const claude: QuotaSnapshot = {
      provider: 'claude',
      windows: [
        window({
          id: 'five_hour',
          class: 'claude:5h',
          used_percent: null,
          limit_reached: true,
          reset_at: iso(NOW - HOUR),
          period_seconds: 18000,
        }),
        window({
          id: 'seven_day',
          class: 'claude:7d',
          used_percent: 60,
          reset_at: iso(NOW - HOUR),
        }),
      ],
      extras: null,
      last_poll: null,
      updated_at: iso(NOW - 2 * HOUR),
    };
    expect(renderedWindows(render(claude))).toEqual([
      { label: '5-hour limit', percent: 'Reset, awaiting data', limitReached: false },
      { label: '7-day limit', percent: '100%', limitReached: false },
    ]);

    const live = {
      status: 'success',
      windows: [
        { id: 'five-hour', label: 'Live 5h', usedPercent: null, resetAtMs: NOW - HOUR },
        { id: 'seven-day', label: 'Live 7d', usedPercent: 60, resetAtMs: NOW - HOUR },
      ],
    };
    const row = resolveLedgerRow('claude', { name: 'c.json', type: 'claude' }, live, NOW);
    const markup = renderToStaticMarkup(
      createElement(LedgerWindowList, { windows: row.windows, nowMs: NOW })
    );
    expect(renderedWindows(markup)).toEqual([
      { label: 'Live 5h', percent: 'Reset, awaiting data', limitReached: false },
      { label: 'Live 7d', percent: '100%', limitReached: false },
    ]);
  });

  test('Codex code review and additional windows are labeled by their period, not their slot', () => {
    const codex: QuotaSnapshot = {
      provider: 'codex',
      windows: [
        window({ id: 'code_review.primary', class: '', used_percent: 10, period_seconds: 604800 }),
        window({ id: 'code_review.secondary', class: '', used_percent: 10, period_seconds: null }),
        window({
          id: 'additional.spark.primary',
          class: '',
          label: 'Spark',
          used_percent: 10,
          period_seconds: 18000,
        }),
        window({
          id: 'additional.spark.secondary',
          class: '',
          label: 'Spark',
          used_percent: 10,
          period_seconds: 30 * 86400,
        }),
      ],
      extras: null,
      last_poll: null,
      updated_at: iso(NOW - HOUR),
    };
    expect(renderedWindows(render(codex)).map((rendered) => rendered.label)).toEqual([
      'Code review weekly limit',
      'Code review limit',
      'Spark 5-hour limit',
      'Spark limit (30 days)',
    ]);
  });

  test('a Codex window with no class gets a readable label, never its slot id', () => {
    const codex = (windows: QuotaWindow[]): QuotaSnapshot => ({
      provider: 'codex',
      windows,
      extras: null,
      last_poll: null,
      updated_at: iso(NOW - HOUR),
    });
    const unclassified = render(
      codex([
        window({ id: 'primary', class: '', used_percent: 10, period_seconds: null }),
        window({
          id: 'secondary',
          class: 'codex:monthly',
          used_percent: 10,
          period_seconds: 30 * 86400,
        }),
      ])
    );
    expect(renderedWindows(unclassified).map((rendered) => rendered.label)).toEqual([
      'Usage limit',
      'Monthly limit',
    ]);
    expect(unclassified).not.toContain('primary');

    const upstreamLabel = render(
      codex([
        window({
          id: 'primary',
          class: '',
          label: 'Burst',
          used_percent: 10,
          period_seconds: null,
        }),
      ])
    );
    expect(renderedWindows(upstreamLabel).map((rendered) => rendered.label)).toEqual(['Burst']);
  });

  test('an empty snapshot or another provider reads as no data', () => {
    const empty = { provider: '', windows: [], extras: null, last_poll: null, updated_at: null };
    expect(readQuotaSnapshot('kimi', empty)).toBeNull();
    expect(readQuotaSnapshot('codex', golden)).toBeNull();
    expect(observedLedgerData('claude', { name: 'c.json', quota_snapshot: empty }, NOW)).toBeNull();
  });
});

describe('the newer whole reading', () => {
  const claudeSnapshot: QuotaSnapshot = {
    provider: 'claude',
    windows: [
      window({
        id: 'seven_day',
        class: 'claude:7d',
        used_percent: 50,
        reset_at: iso(NOW + 2 * HOUR),
        observed_at: iso(NOW - 3 * HOUR),
      }),
    ],
    extras: null,
    last_poll: null,
    updated_at: iso(NOW - HOUR),
  };
  const live = (fetchedAtMs: number) => ({
    status: 'success',
    fetchedAtMs,
    windows: [
      {
        id: 'seven-day',
        label: 'Live 7d',
        usedPercent: 10,
        resetAtMs: NOW + 4 * DAY,
        periodHours: 168,
      },
    ],
  });
  const lane = (fetchedAtMs: number) =>
    buildTimelineLane({
      name: 'a',
      displayName: 'a',
      provider: 'claude',
      quota: live(fetchedAtMs),
      snapshot: claudeSnapshot,
      maxPeriodHours: 14 * 24,
    });

  test('the timeline draws the snapshot when it was updated after the live fetch', () => {
    const fromSnapshot = lane(NOW - 2 * HOUR);
    expect(fromSnapshot.anchorMs).toBe(NOW + 2 * HOUR);
    expect(fromSnapshot.remaining).toBe(50);
    expect(
      fromSnapshot.limits.map((limit) => [limit.labelKey ?? limit.label, limit.remaining])
    ).toEqual([['claude_quota.seven_day', 50]]);

    const fromLive = lane(NOW - 30 * 60 * 1000);
    expect(fromLive.anchorMs).toBe(NOW + 4 * DAY);
    expect(fromLive.remaining).toBe(90);
    expect(
      fromLive.limits.map((limit) => [limit.labelKey ?? limit.label, limit.remaining])
    ).toEqual([['Live 7d', 90]]);
  });

  test('the soonest-recovery sort orders by the newer reading', () => {
    const entries = [
      { type: 'claude' as const, file: { name: 'b.json', type: 'claude' } },
      {
        type: 'claude' as const,
        file: { name: 'a.json', type: 'claude', quota_snapshot: claudeSnapshot },
      },
    ];
    const order = (aFetchedAtMs: number) => {
      const quotas: Record<string, unknown> = {
        'a.json': live(aFetchedAtMs),
        'b.json': { ...live(NOW), windows: [{ id: 'seven-day', resetAtMs: NOW + DAY }] },
      };
      return sortQuotaEntries(entries, 'soonest', (entry) =>
        nextRecoveryMs(
          entry.type,
          quotas[entry.file.name],
          NOW,
          readQuotaSnapshot(entry.type, entry.file.quota_snapshot)
        )
      ).map((entry) => entry.file.name);
    };
    expect(order(NOW - 2 * HOUR)).toEqual(['a.json', 'b.json']);
    expect(order(NOW - 30 * 60 * 1000)).toEqual(['b.json', 'a.json']);
  });
});
