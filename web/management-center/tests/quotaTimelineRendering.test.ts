import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '../src/i18n/index';
import { QuotaTimeline } from '../src/features/quota/components/QuotaTimeline';
import type { QuotaFileEntry } from '../src/features/quota/logic';
import type { QuotaSnapshot } from '../src/features/quota/snapshot/types';
import { buildKimiQuotaRows } from '../src/utils/quota';

/** A Claude snapshot with one 7-day window, 25% used. */
const weeklyOnlySnapshot: QuotaSnapshot = {
  provider: 'claude',
  windows: [
    {
      id: 'seven_day',
      class: 'claude:7d',
      used_percent: 25,
      limit_reached: false,
      reset_at: new Date(2026, 7, 1, 12).toISOString(),
      period_seconds: 604800,
      section: 'claude.usage',
      source: 'poll',
      observed_at: null,
    },
  ],
  extras: null,
  last_poll: null,
  updated_at: null,
};

const weeklyOnly: QuotaFileEntry[] = [
  {
    file: { name: 'weekly-only.json', type: 'claude', quota_snapshot: weeklyOnlySnapshot },
    type: 'claude',
  },
];

const baseProps = {
  entries: weeklyOnly,
  displayNameFor: (name: string) => name,
  resolvedTheme: 'light' as const,
  now: new Date(2026, 6, 29, 12).getTime(),
};

describe('QuotaTimeline rendering', () => {
  test('shows the selected period date instead of always labelling it Today', () => {
    const markup = renderToStaticMarkup(
      createElement(QuotaTimeline, {
        ...baseProps,
        initialOffset: 1,
        quotaFor: () => undefined,
      })
    );

    // The next weekly period starts on Sunday 08/02. The button remains the
    // shortcut back to Today (aria-label/title), but its visible label now
    // reflects the period selected with the previous/next arrows.
    expect(markup).toMatch(
      /<button type="button" aria-label="[^"]+" title="[^"]+">08\/02<\/button>/
    );
  });

  test('keeps the panel and controls visible when 5-hour mode has no matching lanes', () => {
    const markup = renderToStaticMarkup(
      createElement(QuotaTimeline, {
        ...baseProps,
        initialMode: 'session',
        quotaFor: () => undefined,
      })
    );

    expect(markup).toContain('<section');
    expect(markup).toContain('aria-pressed="true"');
    expect(markup).toContain('role="status"');
  });

  test('renders a Kimi 5-hour lane from the protobuf-style time unit', () => {
    const rows = buildKimiQuotaRows({
      usage: {
        used: '1',
        limit: '100',
        resetTime: '2099-08-06T13:59:23.136523Z',
      },
      limits: [
        {
          window: { duration: 300, timeUnit: 'TIME_UNIT_MINUTE' },
          detail: {
            used: '2',
            limit: '100',
            resetTime: '2099-07-31T06:59:23.136523Z',
          },
        },
      ],
    });

    const markup = renderToStaticMarkup(
      createElement(QuotaTimeline, {
        entries: [
          {
            file: { name: 'kimi-real-response.json', type: 'kimi' },
            type: 'kimi',
          },
        ],
        displayNameFor: (name: string) => name,
        resolvedTheme: 'light',
        now: new Date('2099-07-31T04:40:00Z').getTime(),
        initialMode: 'session',
        quotaFor: () => ({ status: 'success', rows }),
      })
    );

    expect(markup).toContain('kimi-real-response.json');
    expect(markup).not.toContain('role="status"');
  });

  test('renders an unexpired Codex reset credit as an expiry tick', () => {
    const section = <T>(value: T) => ({
      status: 'ok' as const,
      value,
      value_at: null,
      checked_at: null,
    });
    const codexCreditSnapshot: QuotaSnapshot = {
      provider: 'codex',
      windows: [
        {
          ...weeklyOnlySnapshot.windows[0],
          id: 'secondary',
          class: 'codex:weekly',
          used_percent: 90,
          section: 'codex.usage',
        },
      ],
      extras: {
        kind: 'codex',
        plan: section('plus'),
        access: section({ allowed: true, limit_reached: false }),
        credits: section({ has_credits: null, unlimited: null, balance: null }),
        subscription: section({ active_until: null }),
        reset_credits: section({
          available: 1,
          applicable_available: 1,
          credits: [
            {
              id: 'credit-1',
              status: 'available',
              granted_at: '2026-07-20T12:00:00Z',
              expires_at: '2026-08-03T12:00:00Z',
            },
          ],
        }),
      },
      last_poll: null,
      updated_at: null,
    };
    const markup = renderToStaticMarkup(
      createElement(QuotaTimeline, {
        entries: [
          {
            file: { name: 'codex-credit.json', type: 'codex', quota_snapshot: codexCreditSnapshot },
            type: 'codex',
          },
        ],
        displayNameFor: (name: string) => name,
        resolvedTheme: 'light',
        now: new Date(2026, 6, 29, 12).getTime(),
        quotaFor: () => undefined,
      })
    );

    expect(markup).toContain('role="img"');
    expect(markup).toContain('08/03 12:00');
  });

  test('stays hidden before any credential exposes a usable quota window', () => {
    const markup = renderToStaticMarkup(
      createElement(QuotaTimeline, {
        ...baseProps,
        entries: [{ file: { name: 'unloaded.json', type: 'claude' }, type: 'claude' }],
        quotaFor: () => undefined,
      })
    );

    expect(markup).toBe('');
  });
});
