export const T0_MS = Date.parse('2026-10-18T17:00:00Z');

export const rawEntry = (seq: number, overrides: Record<string, unknown> = {}) => ({
  seq,
  request_id: `req-${seq}`,
  trace_id: `trace-${seq}`,
  at: new Date(T0_MS + seq * 1000).toISOString(),
  session_id: 'sess-1',
  key_hash: 'a1b2c3d4e5f6',
  auth_id: 'claude-max-1.json',
  provider: 'claude',
  model: 'claude-opus-4-5',
  agent: 'claude-cli',
  stream: true,
  status: 200,
  failed: false,
  latency_ms: 18320,
  ttft_ms: 1410,
  tokens: {
    input: 12,
    cache_read: 81022,
    cache_write: 1460,
    output: 902,
    reasoning: 0,
    prompt: 82494,
  },
  cache: { verdict: 'hit', expected: 81000, gap_s: 41, ttl_s: 3600 },
  ...overrides,
});

export const rawMiss = (seq: number) =>
  rawEntry(seq, {
    tokens: { input: 82000, cache_read: 0, cache_write: 400, output: 10, reasoning: 0, prompt: 82400 },
    cache: {
      verdict: 'miss',
      cause: 'idle_gap',
      confidence: 'high',
      causes: ['idle_gap', 'system_changed'],
      segment: { kind: 'system', index: 1 },
      expected: 81000,
      lost: 81000,
      gap_s: 4210,
      ttl_s: 3600,
    },
  });

export const rawPartial = (seq: number) =>
  rawEntry(seq, {
    cache: {
      verdict: 'partial',
      cause: 'message_changed',
      confidence: 'high',
      causes: ['message_changed'],
      segment: { kind: 'message', index: 14 },
      expected: 81000,
      lost: 52310,
      gap_s: 30,
      ttl_s: 3600,
    },
  });

export const rawToolsMiss = (seq: number) =>
  rawEntry(seq, {
    cache: {
      verdict: 'miss',
      cause: 'tools_changed',
      segment: { kind: 'tools', index: 0 },
      tools: { from: 37, to: 38 },
      expected: 81000,
      lost: 81000,
    },
  });

export const rawCold = (seq: number) => rawEntry(seq, { cache: { verdict: 'cold' } });

export const rawNa = (seq: number) =>
  rawEntry(seq, { cache: { verdict: 'na', reason: 'no_session' }, session_id: '' });

export const rawFailed = (seq: number) =>
  rawEntry(seq, {
    status: 429,
    failed: true,
    error: 'rate limited',
    tokens: {},
    cache: { verdict: 'na', reason: 'failed' },
  });

export const rawPage = (entries: unknown[], overrides: Record<string, unknown> = {}) => ({
  enabled: true,
  epoch: 'k3j9x2',
  cursor: 18234,
  oldest: 1001,
  more: true,
  keys: { a1b2c3d4e5f6: 'sk-...9f2c' },
  entries,
  ...overrides,
});

export const rawSession = (id: string, overrides: Record<string, unknown> = {}) => ({
  session_id: id,
  key_hash: 'a1b2c3d4e5f6',
  agent: 'claude-cli',
  first_at: '2026-10-18T15:00:02Z',
  last_at: '2026-10-18T17:02:11Z',
  requests: 214,
  failed: 3,
  models: ['claude-opus-4-5', 'claude-haiku-4-5'],
  auth_ids: ['claude-max-1.json'],
  verdicts: { hit: 190, partial: 6, miss: 4, cold: 1, na: 13 },
  tokens: {
    input: 4210,
    cache_read: 15100000,
    cache_write: 410000,
    output: 92000,
    reasoning: 0,
    prompt: 15514210,
  },
  hit_ratio: 0.973,
  lost_tokens: 312000,
  causes: { idle_gap: 3, message_changed: 5, tools_changed: 2 },
  top_cause: 'message_changed',
  last_miss: {
    seq: 18101,
    at: '2026-10-18T16:00:00Z',
    cause: 'message_changed',
    segment: { kind: 'message', index: 0 },
  },
  ...overrides,
});

export const rawSessionPage = (sessions: unknown[], overrides: Record<string, unknown> = {}) => ({
  enabled: true,
  epoch: 'k3j9x2',
  cursor: 18234,
  keys: { a1b2c3d4e5f6: 'sk-...9f2c' },
  sessions,
  ...overrides,
});
