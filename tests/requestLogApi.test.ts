import { describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import {
  normalizeRequestPage,
  normalizeSessionPage,
  requestLogApi,
} from '@/services/api/requestLog';
import {
  T0_MS,
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

describe('normalizeRequestPage', () => {
  test('maps snake_case, RFC 3339 instants and every verdict shape', () => {
    const page = normalizeRequestPage(
      rawPage([
        rawEntry(5),
        rawPartial(4),
        rawMiss(3),
        rawToolsMiss(2),
        rawCold(1),
        rawNa(6),
        rawFailed(7),
      ])
    );
    expect(page).toMatchObject({
      enabled: true,
      reason: null,
      epoch: 'k3j9x2',
      cursor: 18234,
      oldest: 1001,
      more: true,
      keys: { a1b2c3d4e5f6: 'sk-...9f2c' },
    });
    const [hit, partial, miss, tools, cold, na, failed] = page.entries;
    expect(hit).toMatchObject({
      seq: 5,
      requestId: 'req-5',
      traceId: 'trace-5',
      atMs: T0_MS + 5000,
      sessionId: 'sess-1',
      keyHash: 'a1b2c3d4e5f6',
      authId: 'claude-max-1.json',
      stream: true,
      status: 200,
      failed: false,
      latencyMs: 18320,
      ttftMs: 1410,
    });
    expect(hit.tokens).toEqual({
      input: 12,
      cacheRead: 81022,
      cacheWrite: 1460,
      output: 902,
      reasoning: 0,
      prompt: 82494,
    });
    expect(hit.cache).toMatchObject({ verdict: 'hit', expected: 81000, gapSec: 41, ttlSec: 3600 });
    expect(hit.cache.segment).toBeNull();
    expect(partial.cache).toMatchObject({
      verdict: 'partial',
      cause: 'message_changed',
      confidence: 'high',
      lost: 52310,
      segment: { kind: 'message', index: 14 },
    });
    expect(miss.cache.causes).toEqual(['idle_gap', 'system_changed']);
    expect(miss.cache.segment).toEqual({ kind: 'system', index: 1 });
    expect(tools.cache.tools).toEqual({ from: 37, to: 38 });
    expect(cold.cache.verdict).toBe('cold');
    expect(na.cache).toMatchObject({ verdict: 'na', reason: 'no_session' });
    expect(failed).toMatchObject({ failed: true, status: 429, error: 'rate limited' });
    expect(failed.tokens.prompt).toBe(0);
  });

  test('degrades missing and wrongly typed fields instead of throwing', () => {
    const page = normalizeRequestPage({
      enabled: true,
      cursor: 'x',
      keys: { ok: 'a', bad: 3 },
      entries: [
        null,
        'row',
        { seq: 'nope' },
        { seq: 9, at: 'garbage', tokens: 'many', cache: { verdict: 'weird', segment: 4 } },
        { seq: 10, status: '200', latency_ms: -5, cache: { causes: ['a', 3] } },
      ],
    });
    expect(page.cursor).toBe(0);
    expect(page.epoch).toBe('');
    expect(page.keys).toEqual({ ok: 'a' });
    expect(page.entries.map((entry) => entry.seq)).toEqual([9, 10]);
    expect(page.entries[0]).toMatchObject({ atMs: 0, status: 0, model: '' });
    expect(page.entries[0].cache.verdict).toBe('na');
    expect(page.entries[0].cache.segment).toBeNull();
    expect(page.entries[1].latencyMs).toBe(0);
    expect(page.entries[1].cache.causes).toEqual(['a']);
  });

  test('survives non-object payloads', () => {
    for (const raw of [null, undefined, 4, 'x', []]) {
      const page = normalizeRequestPage(raw);
      expect(page.entries).toEqual([]);
      expect(page.enabled).toBe(false);
    }
  });

  test('keeps the reason of a disabled payload', () => {
    const page = normalizeRequestPage({ enabled: false, reason: 'home_mode', entries: null });
    expect(page).toMatchObject({ enabled: false, reason: 'home_mode', entries: [] });
  });
});

describe('normalizeSessionPage', () => {
  test('maps session aggregates', () => {
    const page = normalizeSessionPage(rawSessionPage([rawSession('s1')]));
    expect(page.enabled).toBe(true);
    expect(page.sessions).toHaveLength(1);
    expect(page.sessions[0]).toMatchObject({
      sessionId: 's1',
      keyHash: 'a1b2c3d4e5f6',
      agent: 'claude-cli',
      requests: 214,
      failed: 3,
      models: ['claude-opus-4-5', 'claude-haiku-4-5'],
      authIds: ['claude-max-1.json'],
      hitRatio: 0.973,
      lostTokens: 312000,
      topCause: 'message_changed',
      firstAtMs: Date.parse('2026-10-18T15:00:02Z'),
      lastAtMs: Date.parse('2026-10-18T17:02:11Z'),
    });
    expect(page.sessions[0].verdicts).toEqual({ hit: 190, partial: 6, miss: 4, cold: 1, na: 13 });
    expect(page.sessions[0].tokens.cacheRead).toBe(15100000);
    expect(page.sessions[0].causes.idle_gap).toBe(3);
    expect(page.sessions[0].lastMiss).toMatchObject({
      seq: 18101,
      cause: 'message_changed',
      segment: { kind: 'message', index: 0 },
    });
  });

  test('tolerates garbage and disabled payloads', () => {
    const page = normalizeSessionPage({
      enabled: true,
      sessions: [{ session_id: '' }, { session_id: 's', verdicts: 'x', hit_ratio: 'a' }, 3],
    });
    expect(page.sessions.map((session) => session.sessionId)).toEqual(['s']);
    expect(page.sessions[0].hitRatio).toBe(0);
    expect(page.sessions[0].verdicts).toEqual({});
    expect(normalizeSessionPage(null).enabled).toBe(false);
    expect(normalizeSessionPage({ enabled: false, reason: 'disabled' }).reason).toBe('disabled');
  });
});

describe('requestLogApi', () => {
  test('sends only set filters as query parameters', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(rawPage([rawEntry(1)]));
    try {
      const page = await requestLogApi.list({
        session: 's1',
        verdict: ['miss', 'partial'],
        since: 42,
        limit: 200,
        order: 'asc',
        model: '',
      });
      expect(page.entries).toHaveLength(1);
      expect(get).toHaveBeenCalledWith('/observability/requests', {
        params: { session: 's1', verdict: 'miss,partial', since: 42, limit: 200, order: 'asc' },
      });
    } finally {
      get.mockRestore();
    }
  });

  test('queries the sessions endpoint', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(rawSessionPage([rawSession('s1')]));
    try {
      const page = await requestLogApi.sessions({ key: 'abc', limit: 50 });
      expect(page.sessions).toHaveLength(1);
      expect(get).toHaveBeenCalledWith('/observability/requests/sessions', {
        params: { key: 'abc', limit: 50 },
      });
    } finally {
      get.mockRestore();
    }
  });
});
