/**
 * Per-request history and prompt-cache diagnosis
 * (GET /observability/requests and /observability/requests/sessions).
 *
 * Instants become epoch milliseconds. Untrusted input degrades to empty
 * values rather than throwing, so a partial payload still renders.
 */

import { apiClient } from './client';

export const CACHE_VERDICTS = ['hit', 'partial', 'miss', 'cold', 'na'] as const;
export type CacheVerdict = (typeof CACHE_VERDICTS)[number];

export interface CacheSegment {
  /** tools, system, settings, message, instructions or input. */
  kind: string;
  index: number;
}

export interface CacheDiagnosis {
  verdict: CacheVerdict;
  /** Why a verdict is `na`: failed, no_usage, no_session, unsupported, below_minimum, concurrent. */
  reason: string;
  cause: string;
  confidence: string;
  causes: string[];
  segment: CacheSegment | null;
  tools: { from: number; to: number } | null;
  expected: number;
  lost: number;
  gapSec: number;
  ttlSec: number;
}

export interface RequestTokens {
  /** Uncached input only. */
  input: number;
  cacheRead: number;
  cacheWrite: number;
  output: number;
  reasoning: number;
  /** input + cacheRead + cacheWrite. */
  prompt: number;
}

export interface RequestEntry {
  seq: number;
  requestId: string;
  traceId: string;
  atMs: number;
  sessionId: string;
  keyHash: string;
  authId: string;
  provider: string;
  model: string;
  agent: string;
  stream: boolean;
  status: number;
  failed: boolean;
  error: string;
  latencyMs: number;
  ttftMs: number;
  tokens: RequestTokens;
  cache: CacheDiagnosis;
}

export interface RequestPage {
  enabled: boolean;
  /** Why history is off: `disabled`, `home_mode` or `unavailable`. */
  reason: string | null;
  /** Changes when history restarts; rows and cursor from an older epoch are stale. */
  epoch: string;
  cursor: number;
  oldest: number;
  more: boolean;
  /** Masked labels by client key hash. */
  keys: Record<string, string>;
  entries: RequestEntry[];
}

export interface SessionTokens {
  input: number;
  cacheRead: number;
  cacheWrite: number;
  output: number;
  reasoning: number;
  prompt: number;
}

export interface SessionMiss {
  seq: number;
  atMs: number;
  cause: string;
  segment: CacheSegment | null;
}

export interface SessionSummary {
  sessionId: string;
  keyHash: string;
  agent: string;
  firstAtMs: number;
  lastAtMs: number;
  requests: number;
  failed: number;
  models: string[];
  authIds: string[];
  verdicts: Record<string, number>;
  tokens: SessionTokens;
  hitRatio: number;
  lostTokens: number;
  causes: Record<string, number>;
  topCause: string;
  lastMiss: SessionMiss | null;
}

export interface SessionPage {
  enabled: boolean;
  reason: string | null;
  epoch: string;
  cursor: number;
  keys: Record<string, string>;
  sessions: SessionSummary[];
}

export interface RequestFilter {
  session?: string;
  key?: string;
  auth?: string;
  model?: string;
  /** `ok`, `failed` or an HTTP status code. */
  status?: string;
  verdict?: readonly CacheVerdict[];
  fromMs?: number;
  toMs?: number;
  /** Only rows with a higher seq than this (live tail). */
  since?: number;
  /** Only rows with a lower seq than this (load older). */
  before?: number;
  limit?: number;
  order?: 'asc' | 'desc';
}

export interface SessionFilter {
  key?: string;
  auth?: string;
  model?: string;
  fromMs?: number;
  toMs?: number;
  limit?: number;
}

const asRecord = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};

const asArray = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);

const num = (value: unknown): number =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;

const str = (value: unknown): string => (typeof value === 'string' ? value : '');

const strings = (value: unknown): string[] =>
  asArray(value).filter((item): item is string => typeof item === 'string');

const bool = (value: unknown): boolean => value === true;

/** RFC 3339 to ms; garbage and Go's zero time become 0. */
const instant = (value: unknown): number => {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(value)) return 0;
  const ms = Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? ms : 0;
};

const counts = (value: unknown): Record<string, number> =>
  Object.fromEntries(
    Object.entries(asRecord(value)).flatMap(([key, count]) =>
      typeof count === 'number' && Number.isFinite(count) ? [[key, count]] : []
    )
  );

const keyLabels = (value: unknown): Record<string, string> =>
  Object.fromEntries(
    Object.entries(asRecord(value)).filter(
      (entry): entry is [string, string] => typeof entry[1] === 'string'
    )
  );

const readSegment = (raw: unknown): CacheSegment | null => {
  const segment = asRecord(raw);
  const kind = str(segment.kind);
  return kind ? { kind, index: num(segment.index) } : null;
};

const readReason = (raw: Record<string, unknown>): string | null =>
  raw.enabled === true ? null : str(raw.reason) || 'disabled';

function readCache(raw: unknown): CacheDiagnosis {
  const cache = asRecord(raw);
  const tools = asRecord(cache.tools);
  const verdict = (CACHE_VERDICTS as readonly string[]).includes(cache.verdict as string)
    ? (cache.verdict as CacheVerdict)
    : 'na';
  return {
    verdict,
    reason: str(cache.reason),
    cause: str(cache.cause),
    confidence: str(cache.confidence),
    causes: strings(cache.causes),
    segment: readSegment(cache.segment),
    tools:
      typeof tools.from === 'number' && typeof tools.to === 'number'
        ? { from: tools.from, to: tools.to }
        : null,
    expected: num(cache.expected),
    lost: num(cache.lost),
    gapSec: num(cache.gap_s),
    ttlSec: num(cache.ttl_s),
  };
}

function readTokens(raw: unknown): RequestTokens {
  const tokens = asRecord(raw);
  return {
    input: num(tokens.input),
    cacheRead: num(tokens.cache_read),
    cacheWrite: num(tokens.cache_write),
    output: num(tokens.output),
    reasoning: num(tokens.reasoning),
    prompt: num(tokens.prompt),
  };
}

function readEntry(raw: unknown): RequestEntry | null {
  const entry = asRecord(raw);
  const seq = num(entry.seq);
  if (!seq) return null;
  return {
    seq,
    requestId: str(entry.request_id),
    traceId: str(entry.trace_id),
    atMs: instant(entry.at),
    sessionId: str(entry.session_id),
    keyHash: str(entry.key_hash),
    authId: str(entry.auth_id),
    provider: str(entry.provider),
    model: str(entry.model),
    agent: str(entry.agent),
    stream: bool(entry.stream),
    status: num(entry.status),
    failed: bool(entry.failed),
    error: str(entry.error),
    latencyMs: num(entry.latency_ms),
    ttftMs: num(entry.ttft_ms),
    tokens: readTokens(entry.tokens),
    cache: readCache(entry.cache),
  };
}

export function normalizeRequestPage(raw: unknown): RequestPage {
  const page = asRecord(raw);
  return {
    enabled: page.enabled === true,
    reason: readReason(page),
    epoch: str(page.epoch),
    cursor: num(page.cursor),
    oldest: num(page.oldest),
    more: bool(page.more),
    keys: keyLabels(page.keys),
    entries: asArray(page.entries).flatMap((item) => {
      const entry = readEntry(item);
      return entry ? [entry] : [];
    }),
  };
}

function readSession(raw: unknown): SessionSummary | null {
  const session = asRecord(raw);
  const sessionId = str(session.session_id);
  if (!sessionId) return null;
  const miss = asRecord(session.last_miss);
  const tokens = readTokens(session.tokens);
  return {
    sessionId,
    keyHash: str(session.key_hash),
    agent: str(session.agent),
    firstAtMs: instant(session.first_at),
    lastAtMs: instant(session.last_at),
    requests: num(session.requests),
    failed: num(session.failed),
    models: strings(session.models),
    authIds: strings(session.auth_ids),
    verdicts: counts(session.verdicts),
    tokens,
    hitRatio: num(session.hit_ratio),
    lostTokens: num(session.lost_tokens),
    causes: counts(session.causes),
    topCause: str(session.top_cause),
    lastMiss: num(miss.seq)
      ? {
          seq: num(miss.seq),
          atMs: instant(miss.at),
          cause: str(miss.cause),
          segment: readSegment(miss.segment),
        }
      : null,
  };
}

export function normalizeSessionPage(raw: unknown): SessionPage {
  const page = asRecord(raw);
  return {
    enabled: page.enabled === true,
    reason: readReason(page),
    epoch: str(page.epoch),
    cursor: num(page.cursor),
    keys: keyLabels(page.keys),
    sessions: asArray(page.sessions).flatMap((item) => {
      const session = readSession(item);
      return session ? [session] : [];
    }),
  };
}

type Query = Record<string, string | number>;

const setText = (query: Query, name: string, value: string | undefined) => {
  if (value) query[name] = value;
};

const setNumber = (query: Query, name: string, value: number | undefined) => {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) query[name] = value;
};

export const requestLogApi = {
  async list(filter: RequestFilter = {}): Promise<RequestPage> {
    const query: Query = {};
    setText(query, 'session', filter.session);
    setText(query, 'key', filter.key);
    setText(query, 'auth', filter.auth);
    setText(query, 'model', filter.model);
    setText(query, 'status', filter.status);
    if (filter.verdict?.length) query.verdict = filter.verdict.join(',');
    setNumber(query, 'from', filter.fromMs);
    setNumber(query, 'to', filter.toMs);
    setNumber(query, 'since', filter.since);
    setNumber(query, 'before', filter.before);
    setNumber(query, 'limit', filter.limit);
    setText(query, 'order', filter.order);
    const raw = await apiClient.get<unknown>('/observability/requests', { params: query });
    return normalizeRequestPage(raw);
  },

  async sessions(filter: SessionFilter = {}): Promise<SessionPage> {
    const query: Query = {};
    setText(query, 'key', filter.key);
    setText(query, 'auth', filter.auth);
    setText(query, 'model', filter.model);
    setNumber(query, 'from', filter.fromMs);
    setNumber(query, 'to', filter.toMs);
    setNumber(query, 'limit', filter.limit);
    const raw = await apiClient.get<unknown>('/observability/requests/sessions', {
      params: query,
    });
    return normalizeSessionPage(raw);
  },
};
