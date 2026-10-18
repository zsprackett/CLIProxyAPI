import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { RequestTable } from '@/features/requests/RequestTable';
import { RequestsView, type RequestsViewProps } from '@/features/requests/RequestsView';
import { SessionTimeline } from '@/features/requests/SessionTimeline';
import { DEFAULT_CONTROLS } from '@/features/requests/requestsModel';
import { normalizeRequestPage, normalizeSessionPage } from '@/services/api/requestLog';
import {
  rawCold,
  rawEntry,
  rawFailed,
  rawMiss,
  rawNa,
  rawPage,
  rawPartial,
  rawSession,
  rawSessionPage,
  rawToolsMiss,
} from './fixtures/requestLog';

const entries = normalizeRequestPage(
  rawPage([
    rawEntry(7),
    rawPartial(6),
    rawMiss(5),
    rawToolsMiss(4),
    rawCold(3),
    rawNa(2),
    rawFailed(1),
  ])
).entries;

const labels = new Map([['a1b2c3d4e5f6', 'Laptop']]);

const baseProps = (): RequestsViewProps => ({
  tab: 'requests',
  controls: DEFAULT_CONTROLS,
  options: { models: ['claude-opus-4-5'], auths: ['claude-max-1.json'], keys: ['a1b2c3d4e5f6'] },
  labels,
  requests: {
    loading: false,
    error: '',
    enabled: true,
    reason: null,
    rows: entries,
    more: true,
    loadingOlder: false,
    live: true,
  },
  sessions: {
    loading: false,
    error: '',
    enabled: true,
    reason: null,
    sessions: normalizeSessionPage(rawSessionPage([rawSession('6c1d2e3fffff')])).sessions,
  },
  timeline: { sessionId: null, rows: [], loading: false, error: '', truncated: false },
});

const render = (props: Partial<RequestsViewProps>) =>
  renderToStaticMarkup(createElement(RequestsView, { ...baseProps(), ...props }));

let originalLanguage = 'en';
beforeAll(async () => {
  originalLanguage = i18n.language;
  await i18n.changeLanguage('en');
});
afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});

describe('RequestTable', () => {
  let markup = '';
  beforeAll(() => {
    markup = renderToStaticMarkup(createElement(RequestTable, { rows: entries, labels }));
  });

  test('renders one row per request with the client label, model and tokens', () => {
    expect(markup.match(/data-seq="/g)).toHaveLength(7);
    expect(markup).toContain('Laptop');
    expect(markup).toContain('claude-opus-4-5');
    expect(markup).toContain('claude-max-1.json');
    expect(markup).toContain('18 s');
    expect(markup).toContain('1.4 s');
    expect(markup).toContain('81K');
    expect(markup).toContain('sess-1');
  });

  test('renders each verdict badge with its own marker', () => {
    for (const verdict of ['hit', 'partial', 'miss', 'cold', 'na']) {
      expect(markup).toContain(`data-verdict="${verdict}"`);
    }
    expect(markup).toContain('Hit');
    expect(markup).toContain('Partial');
    expect(markup).toContain('Miss');
    expect(markup).toContain('Cold');
  });

  test('names the cause of misses and partials', () => {
    expect(markup).toContain('Idle past the cache TTL');
    expect(markup).toContain('Message changed');
    expect(markup).toContain('Tools changed');
  });

  test('puts the full diagnosis in the verdict tooltip', () => {
    expect(markup).toContain('message 14');
    expect(markup).toContain('system block 1');
    expect(markup).toContain('tools 37 → 38');
    expect(markup).toContain('High');
  });

  test('marks failed requests with the code and the error on hover', () => {
    expect(markup).toContain('data-failed="true"');
    expect(markup).toContain('429');
    expect(markup).toContain('title="rate limited"');
  });

  test('expanded rows show the detail list', () => {
    const open = renderToStaticMarkup(
      createElement(RequestTable, { rows: entries, labels, initialExpanded: [5] })
    );
    expect(open).toContain('All causes');
    expect(open).toContain('Idle past the cache TTL, System prompt changed');
    expect(open).toContain('Expected read');
    expect(open).toContain('Gap vs TTL');
    expect(open).toContain('1.2 h');
  });

  test('explains why an n/a verdict was not judged', () => {
    const open = renderToStaticMarkup(
      createElement(RequestTable, { rows: entries, labels, initialExpanded: [2] })
    );
    expect(open).toContain('no session ID');
  });
});

describe('RequestsView', () => {
  test('renders the requests tab with filters and the live tail switch', () => {
    const markup = render({});
    expect(markup).toContain('Per-request history');
    expect(markup).toContain('Live tail');
    expect(markup).toContain('Client key');
    expect(markup).toContain('All time');
    expect(markup).toContain('Load older');
    expect(markup).toContain('data-seq="7"');
  });

  test('shows a skeleton while loading the first page', () => {
    const markup = render({
      requests: { ...baseProps().requests, loading: true, rows: [], enabled: false },
    });
    expect(markup).toContain('aria-busy="true"');
    expect(markup).not.toContain('data-seq');
  });

  test('shows the error with a retry button', () => {
    const markup = render({
      requests: { ...baseProps().requests, error: 'boom', rows: [], enabled: false },
    });
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('boom');
    expect(markup).toContain('Retry');
  });

  test('shows an empty state, distinct when filters are set', () => {
    const empty = render({ requests: { ...baseProps().requests, rows: [], more: false } });
    expect(empty).toContain('No requests recorded yet');
    const filtered = render({
      requests: { ...baseProps().requests, rows: [], more: false },
      controls: { ...DEFAULT_CONTROLS, model: 'gpt-5' },
    });
    expect(filtered).toContain('No requests match these filters');
    expect(filtered).toContain('Clear filters');
  });

  test('tells how to turn history on when it is disabled', () => {
    const markup = render({
      requests: { ...baseProps().requests, enabled: false, reason: 'disabled', rows: [] },
    });
    expect(markup).toContain('Request history is off');
    expect(markup).toContain('request-history: enabled: true');
    expect(markup).toContain('observability.usage.request-history');
  });

  test('shows home mode distinctly', () => {
    const markup = render({
      requests: { ...baseProps().requests, enabled: false, reason: 'home_mode', rows: [] },
    });
    expect(markup).toContain('not available in home mode');
    expect(markup).not.toContain('request-history: enabled: true');
  });

  test('renders the sessions tab', () => {
    const markup = render({ tab: 'sessions' });
    expect(markup).toContain('6c1d2e3f');
    expect(markup).toContain('97.3%');
    expect(markup).toContain('312K');
    expect(markup).toContain('Message changed');
    expect(markup).toContain('claude-opus-4-5');
    expect(markup).toContain('Laptop');
  });

  test('renders the timeline of the selected session', () => {
    const timelineRows = entries.slice().reverse();
    const markup = render({
      tab: 'sessions',
      timeline: {
        sessionId: 'sess-1',
        rows: timelineRows,
        loading: false,
        error: '',
        truncated: false,
      },
    });
    expect(markup).toContain('Session timeline');
    expect(markup).toContain('Back to sessions');
    expect(markup).toContain('<svg');
  });
});

describe('SessionTimeline', () => {
  const rows = normalizeRequestPage(
    rawPage([rawEntry(1), rawEntry(2), rawPartial(3), rawMiss(4), rawEntry(5)])
  ).entries;
  let markup = '';
  beforeAll(() => {
    markup = renderToStaticMarkup(createElement(SessionTimeline, { rows, truncated: false }));
  });

  test('draws one bar per request in order', () => {
    expect(markup.match(/data-bar="/g)).toHaveLength(5);
    const order = [...markup.matchAll(/data-bar="(\d+)"/g)].map((match) => match[1]);
    expect(order).toEqual(['1', '2', '3', '4', '5']);
  });

  test('marks the partial and the miss with their cause and leaves hits unmarked', () => {
    expect(markup.match(/data-bar="\d+" data-flag="miss"/g)).toHaveLength(1);
    expect(markup.match(/data-bar="\d+" data-flag="partial"/g)).toHaveLength(1);
    expect(markup.match(/data-bar="\d+"(?! data-flag)/g)).toHaveLength(3);
    expect(markup).toContain('Idle past the cache TTL');
    expect(markup).toContain('Message changed');
  });

  test('stacks cache read, cache write and uncached input', () => {
    expect(markup).toContain('data-part="read"');
    expect(markup).toContain('data-part="write"');
    expect(markup).toContain('data-part="uncached"');
    expect(markup).toContain('Cache read');
    expect(markup).toContain('Uncached input');
  });

  test('says when the window is truncated and when it is empty', () => {
    const truncated = renderToStaticMarkup(
      createElement(SessionTimeline, { rows, truncated: true })
    );
    expect(truncated).toContain('Showing the newest 5 requests');
    const empty = renderToStaticMarkup(createElement(SessionTimeline, { rows: [], truncated: false }));
    expect(empty).toContain('no requests in the history');
  });
});
