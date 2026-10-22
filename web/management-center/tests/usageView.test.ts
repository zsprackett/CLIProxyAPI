import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { UsageView, type UsageViewProps } from '@/features/usage/UsageView';
import { DEFAULT_USAGE_CONTROLS } from '@/features/usage/usageModel';
import {
  normalizeUsagePrices,
  normalizeUsageReport,
  type UsageReport,
} from '@/services/api/usageHistory';
import {
  rawBucket,
  rawDisabledReport,
  rawFoldedReport,
  rawPrices,
  rawReport,
} from './fixtures/usageHistory';

const baseProps = (report: UsageReport | null): UsageViewProps => ({
  controls: DEFAULT_USAGE_CONTROLS,
  today: '2026-10-22',
  report,
  loading: false,
  error: '',
  clientLabels: new Map([['a1b2c3d4e5f6', 'Laptop']]),
  prices: { open: false, prices: null, loading: false, error: '' },
  onControlsChange: () => undefined,
  onExportCsv: () => undefined,
  onTogglePrices: () => undefined,
  onRefresh: () => undefined,
});

const render = (report: UsageReport | null, props: Partial<UsageViewProps> = {}) =>
  renderToStaticMarkup(createElement(UsageView, { ...baseProps(report), ...props }));

const ESTIMATE = 'Costs are estimates at public API prices';

describe('UsageView loaded', () => {
  const markup = render(normalizeUsageReport(rawReport()));

  test('shows the summary with an "at least" cost when part of it is unpriced', () => {
    expect(markup).toContain('Usage');
    expect(markup).toContain('at least $0.11');
    expect(markup).toContain('API-price estimate');
    expect(markup).toContain('86.2K');
    expect(markup).toContain('20% failed');
  });

  test('draws one bar segment per series and bucket with theme colors', () => {
    expect(markup).toContain('role="img"');
    expect(markup).toContain('var(--usage-series-1)');
    expect(markup).toContain('var(--usage-series-2)');
    expect(markup).not.toContain('var(--usage-series-other)');
    expect(markup.match(/<rect[^>]*data-series=/g)).toHaveLength(3);
    expect(markup).toContain('Oct 20');
  });

  test('lists every group with a dash for an unpriced cost and a total row', () => {
    expect(markup).toContain('claude-opus-5-5');
    expect(markup).toContain('claude-sonnet-5');
    expect(markup).toContain('devin/swe-1');
    expect(markup).toContain('>Total<');
    expect(markup).toMatch(/>-<\/td>/);
  });

  test('always shows the estimate footnote and names unpriced models', () => {
    expect(markup).toContain(ESTIMATE);
    expect(markup).toContain('devin/devin/swe-1');
    expect(markup).toContain('same-as');
    expect(markup).toContain('America/Toronto');
  });

  test('offers the CSV export and the price table toggle', () => {
    expect(markup).toContain('Export CSV');
    expect(markup).toContain('Show prices');
  });
});

describe('UsageView states', () => {
  test('a fully priced total has no "at least"', () => {
    const markup = render(
      normalizeUsageReport(
        rawReport({ totals: { ...rawReport().totals, unpriced_tokens: 0 }, unpriced_models: [] })
      )
    );
    expect(markup).toContain('$0.11');
    expect(markup).not.toContain('at least');
    expect(markup).toContain(ESTIMATE);
  });

  test('a range without traffic shows the empty state and keeps the controls', () => {
    const markup = render(
      normalizeUsageReport(
        rawReport({
          buckets: [rawBucket('2026-10-22T00:00:00-04:00', '2026-10-23T00:00:00-04:00', [])],
          groups: [],
          totals: {},
          unpriced_models: [],
        })
      )
    );
    expect(markup).toContain('No usage in this range');
    expect(markup).toContain('Group by');
    expect(markup).not.toContain('role="img"');
  });

  test('each disabled reason has its own message and no chart', () => {
    const expected: Record<string, string> = {
      disabled: 'Usage history is off',
      home_mode: 'Usage history is not available in home mode',
      unavailable: 'Usage history is unavailable',
    };
    for (const [reason, title] of Object.entries(expected)) {
      const markup = render(normalizeUsageReport(rawDisabledReport(reason)));
      expect(markup).toContain(title);
      expect(markup).not.toContain('role="img"');
      expect(markup).not.toContain('Export CSV');
    }
    expect(render(normalizeUsageReport(rawDisabledReport('disabled')))).toContain(
      'observability.usage.usage-history'
    );
  });

  test('a first load shows a skeleton and a failed first load shows a retry', () => {
    expect(render(null, { loading: true })).toContain('aria-busy="true"');
    const failed = render(null, { error: 'boom' });
    expect(failed).toContain('role="alert"');
    expect(failed).toContain('boom');
    expect(failed).toContain('Retry');
  });

  test('folded groups render as one Other row with a count, and keys use labels', () => {
    const markup = render(normalizeUsageReport(rawFoldedReport()), {
      controls: { ...DEFAULT_USAGE_CONTROLS, groupBy: 'key' },
    });
    expect(markup).toContain('Other (12 groups)');
    expect(markup).toContain('12 smaller groups are folded into Other');
    expect(markup).toContain('Laptop');
    expect(markup).not.toContain('a1b2c3d4e5f6</td>');
    expect(markup).toContain('var(--usage-series-other)');
    expect(markup).toContain('retention limit');
  });
});

describe('UsageView prices', () => {
  test('the open price table lists rates, sources, derived marks and unpriced models', () => {
    const markup = render(normalizeUsageReport(rawReport()), {
      prices: {
        open: true,
        prices: normalizeUsagePrices(rawPrices()),
        loading: false,
        error: '',
      },
    });
    expect(markup).toContain('Hide prices');
    expect(markup).toContain('Prices per million tokens');
    expect(markup).toContain('claude-opus-4-6-thinking');
    expect(markup).toContain('override');
    expect(markup).toContain('$6.25');
    expect(markup).toContain('Derived from another rate');
    expect(markup).toContain('No price');
  });
});
