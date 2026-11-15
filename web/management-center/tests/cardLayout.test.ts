import { afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { CardLayoutSwitch } from '@/components/ui/CardLayoutSwitch';
import {
  packColumns,
  readCardLayout,
  stackedColumnCount,
  updateStackedHeights,
  writeCardLayout,
} from '@/components/ui/cardLayout';
import { ClaudeQuotaBody } from '@/features/quota/providers/claude/ClaudeQuotaBody';
import { CodexQuotaBody } from '@/features/quota/providers/codex/CodexQuotaBody';
import { SnapshotBody } from '@/features/quota/providers/SnapshotBody';
import { QUOTA_CLASS_KEYS, bindQuotaClasses } from '@/features/quota/types';
import type { QuotaExtras, QuotaSnapshot } from '@/features/quota/snapshot/types';
import { CODEX_NOW, codexSnapshot } from './fixtures/quotaSnapshotCodex';

const LOCALES = ['en', 'zh-CN', 'zh-TW', 'ru', 'vi', 'ko', 'tr'];

const claudeSnapshot = (lastPollError?: string): QuotaSnapshot => ({
  provider: 'claude',
  windows: [],
  extras: {
    kind: 'claude',
    plan: { status: 'ok', value: 'plan_pro', value_at: null, checked_at: null },
    extra_usage: { status: 'absent', value: null, value_at: null, checked_at: null },
  },
  last_poll: lastPollError
    ? { at: '2026-01-01T00:00:00Z', trigger: 'on_demand', error: lastPollError }
    : null,
  updated_at: '2026-01-01T00:00:00Z',
});
const classes = bindQuotaClasses(
  Object.fromEntries(QUOTA_CLASS_KEYS.map((key) => [key, key])),
  'test-host'
);

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('stacked packing', () => {
  const at = (...pairs: [number, number][]) => pairs.map(([column, top]) => ({ column, top }));

  test('puts each card in the shortest column so far, ties to the left', () => {
    // 10 | 2 → 2+2 → 4+2 → 6+8 = 14 → card 5 goes back under the 10.
    expect(packColumns([10, 2, 2, 2, 8, 1], 2)).toEqual({
      positions: at([0, 0], [1, 0], [1, 2], [1, 4], [1, 6], [0, 10]),
      height: 14,
    });
    expect(packColumns([1, 1, 1, 1, 1], 3)).toEqual({
      positions: at([0, 0], [1, 0], [2, 0], [0, 1], [1, 1]),
      height: 2,
    });
    expect(packColumns([3, 1], 0)).toEqual({ positions: at([0, 0], [0, 3]), height: 4 });
    expect(packColumns([], 3)).toEqual({ positions: [], height: 0 });
  });

  test('counts the gap under each card', () => {
    // Without gaps the 3-high card stays the tallest column; with 2px gaps card 3 goes under it.
    expect(packColumns([3, 1, 1, 1], 2)).toEqual({
      positions: at([0, 0], [1, 0], [1, 1], [1, 2]),
      height: 3,
    });
    expect(packColumns([3, 1, 1, 1], 2, 2)).toEqual({
      positions: at([0, 0], [1, 0], [1, 3], [0, 5]),
      height: 6,
    });
  });

  test('fits as many columns of the minimum width as the container holds', () => {
    expect(stackedColumnCount(1000, 340, 16)).toBe(2); // 2×340 + 16 = 696; 3 need 1052
    expect(stackedColumnCount(1052, 340, 16)).toBe(3);
    expect(stackedColumnCount(100, 340, 16)).toBe(1);
  });
});

describe('stacked height measurements', () => {
  test('measures uncached loading cards before packing them', () => {
    const measured = ['a', 'b', 'c'].map((key) => ({ key, height: 169, settled: false }));
    const heights = updateStackedHeights(new Map(), measured);
    expect([...heights.values()]).toEqual([169, 169, 169]);
    expect(packColumns([...heights.values()], 2, 16)).toEqual({
      positions: [
        { column: 0, top: 0 },
        { column: 1, top: 0 },
        { column: 0, top: 185 },
      ],
      height: 354,
    });
  });

  test('retains cached heights while loading and updates them when settled', () => {
    const previous = new Map([['a', 300]]);
    expect(updateStackedHeights(previous, [{ key: 'a', height: 169, settled: false }])).toBe(
      previous
    );
    const next = updateStackedHeights(previous, [
      { key: 'a', height: 250, settled: true },
      { key: 'b', height: 169, settled: false },
    ]);
    expect([...next]).toEqual([
      ['a', 250],
      ['b', 169],
    ]);
    expect([...previous]).toEqual([['a', 300]]);
  });

  test('allows loading cards to grow without overlapping the next card', () => {
    const previous = new Map([
      ['a', 287],
      ['b', 287],
    ]);
    const grown = updateStackedHeights(previous, [{ key: 'a', height: 335, settled: false }]);
    expect(grown.get('a')).toBe(335);
    expect(packColumns([...grown.values()], 1, 16)).toEqual({
      positions: [
        { column: 0, top: 0 },
        { column: 0, top: 351 },
      ],
      height: 638,
    });
    expect(updateStackedHeights(grown, [{ key: 'a', height: 300, settled: false }])).toBe(grown);
    const settled = updateStackedHeights(grown, [{ key: 'a', height: 300, settled: true }]);
    expect(settled.get('a')).toBe(300);
    expect(previous.get('a')).toBe(287);
  });

  test('ignores hidden cards, invalid heights and subpixel changes', () => {
    const previous = new Map([['a', 169]]);
    expect(
      updateStackedHeights(previous, [
        { key: 'a', height: 169.5, settled: true },
        { key: 'hidden', height: 0, settled: false },
        { key: 'invalid', height: NaN, settled: true },
      ])
    ).toBe(previous);
  });
});

describe('stored layout choice', () => {
  const originalWindow = globalThis.window;
  afterEach(() => {
    globalThis.window = originalWindow;
  });
  const withStorage = (getItem: (key: string) => string | null, store?: Map<string, string>) => {
    globalThis.window = {
      localStorage: {
        getItem,
        setItem: (key: string, value: string) => store?.set(key, value),
      },
    } as unknown as Window & typeof globalThis;
  };

  test('reads a valid choice and falls back to Even rows otherwise', () => {
    withStorage((key) => (key === 'a' ? 'compact' : key === 'b' ? 'bogus' : null));
    expect(readCardLayout('a')).toBe('compact');
    expect(readCardLayout('b')).toBe('even');
    expect(readCardLayout('missing')).toBe('even');
    withStorage(() => {
      throw new Error('storage blocked');
    });
    expect(readCardLayout('a')).toBe('even');
  });

  test('uses the caller fallback only when nothing valid is stored', () => {
    // Auth Files passes 'compact' for users who had the old compact switch on.
    withStorage((key) => (key === 'set' ? 'stacked' : key === 'bad' ? 'grid' : null));
    expect(readCardLayout('set', 'compact')).toBe('stacked');
    expect(readCardLayout('bad', 'compact')).toBe('compact');
    expect(readCardLayout('missing', 'compact')).toBe('compact');
  });

  test('writes the choice under the page key', () => {
    const store = new Map<string, string>();
    withStorage(() => null, store);
    writeCardLayout('quotaPage.cardLayout', 'stacked');
    expect(store.get('quotaPage.cardLayout')).toBe('stacked');
  });
});

describe('CardLayoutSwitch', () => {
  test('is a named radio group with one focusable, checked choice', () => {
    const markup = renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(CardLayoutSwitch, { value: 'stacked', onChange: () => {} })
      )
    );
    expect(markup).toContain('role="radiogroup" aria-label="Card layout"');
    const radios = [...markup.matchAll(/<button[^>]*role="radio"[^>]*>/g)].map((m) => m[0]);
    expect(radios).toHaveLength(3);
    expect(radios.map((r) => /aria-label="([^"]+)"/.exec(r)?.[1])).toEqual([
      'Even rows',
      'Stacked',
      'Compact',
    ]);
    expect(radios.map((r) => r.includes('aria-checked="true"'))).toEqual([false, true, false]);
    expect(radios.map((r) => /tabindex="(-?\d)"/i.exec(r)?.[1])).toEqual(['-1', '0', '-1']);
  });
});

describe('plan chip marker (kept on Compact glance cards)', () => {
  const planItems = (markup: string) =>
    [
      ...markup.matchAll(
        /<span class="codexPlanItem"( data-quota-plan="")?>(.*?)<\/span><\/span>/g
      ),
    ].map((m) => ({ plan: Boolean(m[1]), text: m[2].replace(/<[^>]+>/g, ' ').trim() }));

  test('Codex marks only the plan, not renewal, credits or manual resets', () => {
    const snapshot = codexSnapshot();
    const extras = {
      ...(snapshot.extras as Extract<QuotaExtras, { kind: 'codex' }>),
      plan: { status: 'ok', value: 'plus', value_at: null, checked_at: null },
    } as Extract<QuotaExtras, { kind: 'codex' }>;
    const items = planItems(
      renderToStaticMarkup(
        createElement(CodexQuotaBody, { snapshot, extras, classes, nowMs: CODEX_NOW })
      )
    );
    expect(items.filter((item) => item.plan).map((item) => item.text)).toEqual(['Plan  Plus']);
    expect(items.filter((item) => !item.plan).length).toBeGreaterThan(0);
  });

  test('Claude marks the plan but not host-added chips', () => {
    const snapshot = claudeSnapshot();
    const markup = renderToStaticMarkup(
      createElement(ClaudeQuotaBody, {
        snapshot,
        extras: snapshot.extras as Extract<QuotaExtras, { kind: 'claude' }>,
        classes,
        nowMs: 0,
        planItems: createElement('span', { className: 'codexPlanItem' }, 'host chip'),
      })
    );
    expect(markup.match(/data-quota-plan=""/g)).toHaveLength(1);
    expect(markup).toContain('<span class="codexPlanItem">host chip</span>');
  });
});

describe('Compact status notices', () => {
  test('a snapshot with a failed poll includes the poll failure notice', () => {
    const markup = renderToStaticMarkup(
      createElement(SnapshotBody, { snapshot: claudeSnapshot('HTTP 401'), classes, nowMs: 0 })
    );
    expect(markup).toContain('class="codexResetCreditsError" role="status"');
    expect(markup).toContain('HTTP 401');
  });

  test('Compact hide rules do not suppress quota status/error notices', async () => {
    const css = await Bun.file('src/features/quota/components/QuotaBody.module.scss').text();
    const hiddenRules = [...css.matchAll(/([^{}]+)\{\s*display:\s*none;/g)];
    expect(hiddenRules.length).toBeGreaterThan(0);
    for (const rule of hiddenRules) {
      expect(rule[1]).not.toContain('.codexResetCreditsError');
    }
  });
});

describe('card layout strings', () => {
  for (const locale of LOCALES) {
    test(`${locale} has every card layout label`, async () => {
      const messages = (await Bun.file(`src/i18n/locales/${locale}.json`).json()) as {
        card_layout: Record<string, unknown>;
      };
      for (const key of ['label', 'even', 'stacked', 'compact', 'show_details', 'hide_details']) {
        expect(typeof messages.card_layout[key]).toBe('string');
        expect(String(messages.card_layout[key]).trim()).not.toBe('');
      }
    });
  }
});
