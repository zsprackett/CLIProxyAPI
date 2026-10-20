/**
 * The Claude and Codex cards render the backend snapshot: the plan and the
 * other extras, then each window with its remaining percent.
 */

import { describe, expect, test } from 'bun:test';
import { existsSync, readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { SnapshotBody } from '@/features/quota/providers/SnapshotBody';
import type { QuotaExtras, QuotaSnapshot } from '@/features/quota/snapshot/types';
import { QUOTA_CLASS_KEYS, bindQuotaClasses } from '@/features/quota/types';
import { formatInstantShort } from '@/utils/quota';
import {
  CODEX_EXPIRING_CREDIT_AT,
  CODEX_LATER_CREDIT_AT,
  CODEX_NOW,
  codexSnapshot,
} from './fixtures/quotaSnapshotCodex';

const classes = bindQuotaClasses(
  Object.fromEntries(QUOTA_CLASS_KEYS.map((key) => [key, key])),
  'test-host'
);

const golden = (provider: string): QuotaSnapshot =>
  JSON.parse(readFileSync(goldenPath(provider), 'utf8'));
const goldenPath = (provider: string) =>
  new URL(`../../../sdk/cliproxy/auth/testdata/quota_snapshot_${provider}.json`, import.meta.url);

const NOW = Date.parse('2026-10-19T18:00:00Z');

const render = (snapshot: QuotaSnapshot, nowMs = NOW) =>
  renderToStaticMarkup(createElement(SnapshotBody, { snapshot, classes, nowMs }));

/** Label and percent of each rendered window, in order. */
const windowRows = (markup: string) =>
  markup
    .split(/<div class="window"[^>]*>/)
    .slice(1)
    .map((chunk) => {
      const text = (className: string) =>
        new RegExp(`<span class="${className}">([^<]*)</span>`).exec(chunk)?.[1] ?? null;
      return [text('windowLabel'), text('windowPercent')];
    });

const SOONEST_HINT = 'Recovers first on this credential';

/** Labels of the windows marked as the row that recovers first, with their countdown class. */
const soonWindows = (markup: string) =>
  markup
    .split(/(?=<div class="window")/)
    .slice(1)
    .filter((chunk) => chunk.startsWith(`<div class="window" title="${SOONEST_HINT}">`))
    .map((chunk) => {
      expect(chunk).toContain('<span class="resetRelative resetRelativeSoon">');
      return /<span class="windowLabel">([^<]*)<\/span>/.exec(chunk)?.[1];
    });

const withExtras = (snapshot: QuotaSnapshot, patch: Partial<QuotaExtras>): QuotaSnapshot => ({
  ...snapshot,
  extras: { ...snapshot.extras, ...patch } as QuotaExtras,
});

describe('Claude card from the Go golden snapshot', () => {
  test('shows the plan and each window with its remaining percent', () => {
    const markup = render(golden('claude'));
    expect(markup).toContain(
      '<span class="codexPlanLabel">Plan</span><span class="codexPlanValue">Max</span>'
    );
    expect(windowRows(markup)).toEqual([
      ['5-hour limit', '75%'],
      ['7-day limit', '40%'],
      ['7-day Fable 5', 'Unknown'],
    ]);
    expect(markup).not.toContain('Extra Usage');
  });

  test('marks the window that resets within the hour and no other', () => {
    const halfHourBeforeFiveHourReset = Date.parse('2026-10-19T19:30:00Z');
    const markup = render(golden('claude'), halfHourBeforeFiveHourReset);
    expect(soonWindows(markup)).toEqual(['5-hour limit']);
    expect(markup.match(/resetRelativeSoon/g)?.length).toBe(1);
  });

  test('marks no window while every reset is more than an hour away', () => {
    const markup = render(golden('claude'));
    expect(markup).not.toContain(SOONEST_HINT);
    expect(markup).not.toContain('resetRelativeSoon');
  });

  test('shows enabled extra usage in dollars', () => {
    const snapshot = withExtras(golden('claude'), {
      extra_usage: {
        status: 'ok',
        value: { enabled: true, monthly_limit: 5000, used_credits: 1234, utilization: 24.68 },
        value_at: '2026-10-19T17:00:00Z',
        checked_at: '2026-10-19T17:00:00Z',
      },
    } as Partial<QuotaExtras>);
    expect(render(snapshot)).toContain(
      '<span class="codexPlanLabel">Extra Usage</span><span class="codexPlanValue">$12.34 / $50.00</span>'
    );
  });

  test('says why the last poll failed', () => {
    const snapshot: QuotaSnapshot = {
      ...golden('claude'),
      last_poll: { at: '2026-10-19T17:59:00Z', trigger: 'on_demand', error: 'HTTP 429' },
    };
    expect(render(snapshot)).toContain('Last quota poll failed: HTTP 429');
    expect(render(golden('claude'))).not.toContain('Last quota poll failed');
  });
});

describe('Codex card from a contract-shaped snapshot', () => {
  test('shows the plan, credits, renewal, and reset-credit count', () => {
    const markup = render(codexSnapshot(), CODEX_NOW);
    expect(markup).toContain('<span class="elitePlanValue">Pro 20x</span>');
    expect(markup).toContain(
      '<span class="codexPlanLabel">Credit balance</span><span class="codexPlanValue">125</span>'
    );
    expect(markup).toContain(
      '<span class="codexPlanLabel">Manual resets</span><span class="codexPlanValue">2</span>'
    );
    expect(markup).toContain('Renewal time');
  });

  test('shows each window with its label and remaining percent', () => {
    expect(windowRows(render(codexSnapshot(), CODEX_NOW))).toEqual([
      ['5-hour limit', '62%'],
      ['Weekly limit', '40%'],
      ['Code review 5-hour limit', '100%'],
      ['GPT-5.3-Codex-Spark weekly limit', '90%'],
    ]);
  });

  test('lists each reset credit with its expiry and highlights the one expiring within the hour', () => {
    const markup = render(codexSnapshot(), CODEX_NOW);
    expect(markup).toContain('Reset 1');
    expect(markup).toContain('Reset 2');
    expect(markup).toContain(formatInstantShort(CODEX_LATER_CREDIT_AT));
    expect(markup).toContain(formatInstantShort(CODEX_EXPIRING_CREDIT_AT));
    expect(markup).toMatch(/11 days/);
    expect(markup.match(/codexResetCreditRowSoon/g)?.length).toBe(1);
  });

  test('marks the reset credit that expires first when it beats every window', () => {
    const markup = render(codexSnapshot(), CODEX_NOW);
    expect(soonWindows(markup)).toEqual([]);
    expect(markup.match(/codexResetCreditRowSoon/g)?.length).toBe(1);
    expect(markup.match(new RegExp(SOONEST_HINT, 'g'))?.length).toBe(1);
  });

  test('marks the window that resets first, and not the credit, when a window beats every credit', () => {
    const snapshot = codexSnapshot();
    const firstWindow = snapshot.windows[0];
    const earlier: QuotaSnapshot = {
      ...snapshot,
      windows: [
        { ...firstWindow, reset_at: new Date(CODEX_NOW + 10 * 60 * 1000).toISOString() },
        ...snapshot.windows.slice(1),
      ],
    };
    const markup = render(earlier, CODEX_NOW);
    expect(soonWindows(markup)).toEqual(['5-hour limit']);
    expect(markup).not.toContain('codexResetCreditRowSoon');
    expect(markup.match(new RegExp(SOONEST_HINT, 'g'))?.length).toBe(1);
    expect(markup.match(/resetRelativeSoon/g)?.length).toBe(1);
  });

  test('shows unlimited credits instead of a balance', () => {
    const snapshot = codexSnapshot();
    const markup = render(
      withExtras(snapshot, {
        credits: {
          status: 'ok',
          value: { has_credits: true, unlimited: true, balance: null },
          value_at: null,
          checked_at: null,
        },
      } as Partial<QuotaExtras>),
      CODEX_NOW
    );
    expect(markup).toContain(
      '<span class="codexPlanLabel">Credit balance</span><span class="codexPlanValue">Unlimited</span>'
    );
  });

  test('says the reset-credit list failed when its section failed', () => {
    const markup = render(
      withExtras(codexSnapshot(), {
        reset_credits: {
          status: 'failed',
          value: null,
          value_at: null,
          checked_at: '2026-10-19T17:00:00Z',
          error: 'HTTP 500',
        },
      } as Partial<QuotaExtras>),
      CODEX_NOW
    );
    expect(markup).toContain('Failed to load manual reset expiry: HTTP 500');
    expect(markup).not.toContain('Manual resets');
    expect(markup).not.toContain('Reset 1');
  });

  test.each(['en', 'zh-CN', 'zh-TW', 'ru'])(
    'renders Business Premium with its premium badge in %s',
    async (language) => {
      await i18n.changeLanguage(language);
      const snapshot = withExtras(codexSnapshot(), {
        plan: {
          status: 'ok',
          value: 'self_serve_business_prolite',
          value_at: null,
          checked_at: null,
        },
      } as Partial<QuotaExtras>);
      const markup = render(snapshot, CODEX_NOW);
      expect(markup).toContain('<span class="premiumPlanValue">Business Premium</span>');
      expect(markup).not.toContain('self_serve_business_prolite');
    }
  );
});

// The Codex golden lands with the backend half of P2; until then this suite skips.
describe.skipIf(!existsSync(goldenPath('codex')))('Codex card from the Go golden snapshot', () => {
  const beforeEveryReset = Date.parse('2026-10-19T17:10:00Z');

  test('shows the plan, credits, and reset-credit count', () => {
    const markup = render(golden('codex'), beforeEveryReset);
    expect(markup).toContain('<span class="elitePlanValue">Pro 20x</span>');
    expect(markup).toContain(
      '<span class="codexPlanLabel">Credit balance</span><span class="codexPlanValue">42.50</span>'
    );
    expect(markup).toContain(
      '<span class="codexPlanLabel">Manual resets</span><span class="codexPlanValue">2</span>'
    );
    expect(markup).toContain('Reset 1');
    expect(markup).toContain('Reset 2');
  });

  test('shows each window with its label and remaining percent', () => {
    expect(windowRows(render(golden('codex'), beforeEveryReset))).toEqual([
      ['5-hour limit', '88%'],
      ['Weekly limit', '52%'],
      ['bengalfox 5-hour limit', '97%'],
      ['Code review 5-hour limit', '93%'],
      ['Code review weekly limit', '98%'],
    ]);
  });
});
