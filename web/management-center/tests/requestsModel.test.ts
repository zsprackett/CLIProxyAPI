import { describe, expect, test } from 'bun:test';
import {
  MAX_ROWS,
  appendOlder,
  buildClientLabels,
  causeKey,
  collectOptions,
  emptyFeed,
  feedFromPage,
  mergePoll,
  naReasonKey,
  requestFilterFromControls,
  segmentLabel,
  sessionTimeline,
  verdictKey,
  DEFAULT_CONTROLS,
  formatLatency,
  formatSpan,
  shortId,
} from '@/features/requests/requestsModel';
import { apiKeyNameFingerprint } from '@/features/config/apiKeyNames';
import { clientKeyHash } from '@/features/quota/forecast/forecastModel';
import { normalizeRequestPage } from '@/services/api/requestLog';
import {
  rawEntry,
  rawFailed,
  rawMiss,
  rawPage,
  rawPartial,
  rawToolsMiss,
} from './fixtures/requestLog';

const page = (seqs: number[], overrides: Record<string, unknown> = {}) =>
  normalizeRequestPage(
    rawPage(
      seqs.map((seq) => rawEntry(seq)),
      overrides
    )
  );

describe('request feed', () => {
  test('feedFromPage keeps newest-first order and the cursor', () => {
    const feed = feedFromPage(page([1, 3, 2], { cursor: 3, oldest: 1, more: false }));
    expect(feed.rows.map((row) => row.seq)).toEqual([3, 2, 1]);
    expect(feed).toMatchObject({ epoch: 'k3j9x2', cursor: 3, oldest: 1, more: false });
  });

  test('mergePoll prepends new rows, skips duplicates and advances the cursor', () => {
    const feed = feedFromPage(page([3, 2, 1], { cursor: 3 }));
    const result = mergePoll(feed, page([5, 4, 3], { cursor: 5 }));
    expect(result.reset).toBe(false);
    expect(result.feed.rows.map((row) => row.seq)).toEqual([5, 4, 3, 2, 1]);
    expect(result.feed.cursor).toBe(5);
  });

  test('mergePoll keeps the cursor when nothing is new', () => {
    const feed = feedFromPage(page([3], { cursor: 3 }));
    const result = mergePoll(feed, page([], { cursor: 3 }));
    expect(result.feed).toEqual(feed);
  });

  test('mergePoll asks for a reload when the epoch changes', () => {
    const feed = feedFromPage(page([3, 2], { cursor: 3 }));
    const result = mergePoll(feed, page([1], { epoch: 'other', cursor: 1 }));
    expect(result.reset).toBe(true);
    expect(result.feed).toEqual(feed);
  });

  test('mergePoll bounds memory by dropping the oldest rows and marks more', () => {
    const seqs = Array.from({ length: MAX_ROWS }, (_, i) => MAX_ROWS - i);
    const feed = feedFromPage(page(seqs, { cursor: MAX_ROWS, more: false }));
    const result = mergePoll(feed, page([MAX_ROWS + 2, MAX_ROWS + 1], { cursor: MAX_ROWS + 2 }));
    expect(result.feed.rows).toHaveLength(MAX_ROWS);
    expect(result.feed.rows[0].seq).toBe(MAX_ROWS + 2);
    expect(result.feed.rows.at(-1)?.seq).toBe(3);
    expect(result.feed.more).toBe(true);
    expect(result.feed.oldest).toBe(3);
  });

  test('appendOlder adds rows below the lowest seq and takes the new more flag', () => {
    const feed = feedFromPage(page([5, 4], { more: true }));
    const next = appendOlder(feed, page([4, 3, 2], { more: false }));
    expect(next.rows.map((row) => row.seq)).toEqual([5, 4, 3, 2]);
    expect(next.more).toBe(false);
    expect(next.cursor).toBe(feed.cursor);
  });

  test('appendOlder stops at the row bound', () => {
    const seqs = Array.from({ length: MAX_ROWS }, (_, i) => MAX_ROWS + 10 - i);
    const feed = feedFromPage(page(seqs, { more: true }));
    const next = appendOlder(feed, page([5, 4], { more: true }));
    expect(next.rows).toHaveLength(MAX_ROWS);
    expect(next.more).toBe(false);
  });

  test('emptyFeed starts with no epoch so the first poll never counts as a reset', () => {
    const result = mergePoll(emptyFeed(), page([2, 1], { cursor: 2 }));
    expect(result.reset).toBe(false);
    expect(result.feed.rows).toHaveLength(2);
  });
});

describe('labels', () => {
  test('verdict, cause and reason keys sit under request_log', () => {
    expect(verdictKey('partial')).toBe('request_log.verdict_partial');
    expect(causeKey('idle_gap')).toBe('request_log.cause_idle_gap');
    expect(causeKey('something_new')).toBe('request_log.cause_unknown');
    expect(naReasonKey('no_session')).toBe('request_log.na_no_session');
    expect(naReasonKey('')).toBe('request_log.na_unknown');
  });

  test('segmentLabel names the segment and the tools delta', () => {
    const partial = normalizeRequestPage(rawPage([rawPartial(1)])).entries[0].cache;
    const miss = normalizeRequestPage(rawPage([rawMiss(2)])).entries[0].cache;
    const tools = normalizeRequestPage(rawPage([rawToolsMiss(3)])).entries[0].cache;
    expect(segmentLabel(partial)).toEqual({
      key: 'request_log.segment_message',
      values: { index: 14 },
    });
    expect(segmentLabel(miss)).toEqual({
      key: 'request_log.segment_system',
      values: { index: 1 },
    });
    expect(segmentLabel(tools)).toEqual({
      key: 'request_log.segment_tools_delta',
      values: { from: 37, to: 38 },
    });
    const hit = normalizeRequestPage(rawPage([rawEntry(4)])).entries[0].cache;
    expect(segmentLabel(hit)).toBeNull();
  });

  test('buildClientLabels prefers a saved name, then the backend mask, then the hash', () => {
    const apiBase = 'http://x';
    const key = 'sk-secret-key-1234';
    const names = { [apiKeyNameFingerprint(apiBase, key)]: 'Laptop' };
    const hash = clientKeyHash(key);
    const labels = buildClientLabels({
      hashes: [hash, 'bbbbbbbbbbbb', 'cccccccccccc', '-', ''],
      keys: { bbbbbbbbbbbb: 'sk-...9f2c' },
      apiKeys: [key],
      names,
      apiBase,
    });
    expect(labels.get(hash)).toBe('Laptop');
    expect(labels.get('bbbbbbbbbbbb')).toBe('sk-...9f2c');
    expect(labels.get('cccccccccccc')).toBe('#cccccccccccc');
    expect(labels.get('-')).toBe('');
    expect(labels.get('')).toBe('');
  });
});

describe('filters', () => {
  test('controls map to query parameters with a time preset', () => {
    const now = 10_000_000;
    expect(requestFilterFromControls(DEFAULT_CONTROLS, now)).toEqual({});
    expect(
      requestFilterFromControls(
        {
          session: ' s1 ',
          key: 'k',
          auth: 'a',
          model: 'm',
          status: 'failed',
          verdicts: ['miss', 'partial'],
          range: '1h',
        },
        now
      )
    ).toEqual({
      session: 's1',
      key: 'k',
      auth: 'a',
      model: 'm',
      status: 'failed',
      verdict: ['miss', 'partial'],
      fromMs: now - 3_600_000,
    });
  });

  test('collectOptions accumulates distinct values across pages', () => {
    const first = collectOptions(
      { models: [], auths: [], keys: [] },
      normalizeRequestPage(rawPage([rawEntry(1)])).entries
    );
    const second = collectOptions(first, [
      ...normalizeRequestPage(rawPage([rawEntry(2, { model: 'gpt-5', auth_id: '' })])).entries,
    ]);
    expect(second.models).toEqual(['claude-opus-4-5', 'gpt-5']);
    expect(second.auths).toEqual(['claude-max-1.json']);
    expect(second.keys).toEqual(['a1b2c3d4e5f6']);
  });
});

describe('sessionTimeline', () => {
  test('bar heights scale to the largest prompt and split read, write and uncached', () => {
    const entries = normalizeRequestPage(
      rawPage([
        rawEntry(1, {
          tokens: { input: 0, cache_read: 20000, cache_write: 20000, output: 1, prompt: 40000 },
        }),
        rawMiss(2),
        rawFailed(3),
      ])
    ).entries;
    const bars = sessionTimeline(entries);
    expect(bars).toHaveLength(3);
    const maxPrompt = 82400;
    expect(bars[1].heightPct).toBe(100);
    expect(bars[1].readPct).toBe(0);
    expect(bars[1].uncachedPct).toBeCloseTo((82000 / 82400) * 100, 5);
    expect(bars[1].writePct).toBeCloseTo((400 / 82400) * 100, 5);
    expect(bars[0].heightPct).toBeCloseTo((40000 / maxPrompt) * 100, 5);
    expect(bars[0].readPct + bars[0].writePct + bars[0].uncachedPct).toBeCloseTo(100, 5);
    expect(bars[0].readPct).toBeCloseTo(50, 5);
    expect(bars[2]).toMatchObject({ heightPct: 0, failed: true });
  });

  test('misses and partials carry their primary cause, hits do not', () => {
    const entries = normalizeRequestPage(
      rawPage([rawEntry(1), rawPartial(2), rawMiss(3)])
    ).entries;
    const bars = sessionTimeline(entries);
    expect(bars.map((bar) => bar.cause)).toEqual(['', 'message_changed', 'idle_gap']);
    expect(bars.map((bar) => bar.verdict)).toEqual(['hit', 'partial', 'miss']);
  });

  test('keeps the order it is given and enforces a minimum visible height', () => {
    const entries = normalizeRequestPage(
      rawPage([
        rawEntry(1),
        rawEntry(2, { tokens: { input: 1, cache_read: 0, cache_write: 0, prompt: 1 } }),
      ])
    ).entries;
    const bars = sessionTimeline(entries);
    expect(bars.map((bar) => bar.seq)).toEqual([1, 2]);
    expect(bars[1].heightPct).toBeGreaterThanOrEqual(2);
  });
});

describe('formatting', () => {
  test('formatLatency uses ms below a second and seconds above', () => {
    expect(formatLatency(0)).toBe('-');
    expect(formatLatency(820)).toBe('820 ms');
    expect(formatLatency(1410)).toBe('1.4 s');
    expect(formatLatency(18320)).toBe('18 s');
  });

  test('formatSpan picks seconds, minutes or hours', () => {
    expect(formatSpan(0)).toBe('0 s');
    expect(formatSpan(41)).toBe('41 s');
    expect(formatSpan(300)).toBe('5 m');
    expect(formatSpan(4210)).toBe('1.2 h');
    expect(formatSpan(3600)).toBe('1 h');
  });

  test('shortId keeps the first eight characters of the conversation id', () => {
    expect(shortId('6c1d2e3f-aaaa-bbbb')).toBe('6c1d2e3f');
    expect(shortId('')).toBe('');
  });

  test('shortId drops the provider prefix and keeps a short subagent id', () => {
    expect(shortId('claude:6cf9a5f1-7238-431b-9c4d-3f5e48a7e6c0')).toBe('6cf9a5f1');
    expect(shortId('claude:6cf9a5f1-7238-431b-9c4d-3f5e48a7e6c0:agent:a97ace3c6eaef4e66')).toBe(
      '6cf9a5f1 › a97ace'
    );
  });
});
