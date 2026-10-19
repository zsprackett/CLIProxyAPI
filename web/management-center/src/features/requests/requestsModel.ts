/**
 * Pure logic of the Requests page: the live feed, filter mapping, labels and
 * the session timeline geometry. No React and no network.
 */

import { apiKeyNameFingerprint } from '@/features/config/apiKeyNames';
import { clientKeyHash } from '@/features/quota/forecast/forecastModel';
import type {
  CacheDiagnosis,
  CacheVerdict,
  RequestEntry,
  RequestFilter,
  RequestPage,
} from '@/services/api/requestLog';

/** Rows kept in memory; a long live tail drops the oldest first. */
export const MAX_ROWS = 2000;
export const PAGE_SIZE = 200;
export const TAIL_INTERVAL_MS = 5000;

export interface RequestFeed {
  epoch: string;
  /** Highest seq seen; the next live poll sends it as `since`. */
  cursor: number;
  /** Lowest seq shown; "load older" sends it as `before`. 0 when empty. */
  oldest: number;
  /** The store holds rows older than the lowest one shown. */
  more: boolean;
  /** Newest first. */
  rows: RequestEntry[];
}

export const emptyFeed = (): RequestFeed => ({
  epoch: '',
  cursor: 0,
  oldest: 0,
  more: false,
  rows: [],
});

const byNewest = (a: RequestEntry, b: RequestEntry) => b.seq - a.seq;

const lowestSeq = (rows: RequestEntry[]) => rows[rows.length - 1]?.seq ?? 0;

export function feedFromPage(page: RequestPage): RequestFeed {
  const rows = [...page.entries].sort(byNewest).slice(0, MAX_ROWS);
  return {
    epoch: page.epoch,
    cursor: Math.max(page.cursor, rows[0]?.seq ?? 0),
    oldest: lowestSeq(rows),
    more: page.more,
    rows,
  };
}

/**
 * Folds a `since` poll into the feed. A changed epoch means the backend
 * restarted its history, so the caller must reload instead of merging.
 */
export function mergePoll(
  feed: RequestFeed,
  page: RequestPage
): { feed: RequestFeed; reset: boolean } {
  if (feed.epoch && page.epoch !== feed.epoch) return { feed, reset: true };
  const fresh = page.entries.filter((entry) => entry.seq > feed.cursor);
  const cursor = Math.max(feed.cursor, page.cursor);
  if (!fresh.length) {
    return { feed: cursor === feed.cursor ? feed : { ...feed, cursor }, reset: false };
  }
  const merged = [...fresh.sort(byNewest), ...feed.rows];
  const rows = merged.slice(0, MAX_ROWS);
  return {
    feed: {
      epoch: page.epoch || feed.epoch,
      cursor: Math.max(cursor, rows[0].seq),
      oldest: lowestSeq(rows),
      more: feed.more || merged.length > rows.length,
      rows,
    },
    reset: false,
  };
}

export function appendOlder(feed: RequestFeed, page: RequestPage): RequestFeed {
  const older = page.entries.filter((entry) => entry.seq < feed.oldest).sort(byNewest);
  const rows = [...feed.rows, ...older].slice(0, MAX_ROWS);
  return {
    ...feed,
    oldest: lowestSeq(rows),
    more: rows.length >= MAX_ROWS ? false : page.more,
    rows,
  };
}

export const canLoadOlder = (feed: RequestFeed): boolean =>
  feed.more && feed.rows.length < MAX_ROWS;

// Filters

export const TIME_RANGES = ['all', '15m', '1h', '6h', '24h', '7d'] as const;
export type TimeRange = (typeof TIME_RANGES)[number];

const RANGE_MS: Record<Exclude<TimeRange, 'all'>, number> = {
  '15m': 15 * 60_000,
  '1h': 3_600_000,
  '6h': 6 * 3_600_000,
  '24h': 24 * 3_600_000,
  '7d': 7 * 24 * 3_600_000,
};

export interface RequestControls {
  session: string;
  key: string;
  auth: string;
  model: string;
  status: string;
  verdicts: CacheVerdict[];
  range: TimeRange;
}

export const DEFAULT_CONTROLS: RequestControls = {
  session: '',
  key: '',
  auth: '',
  model: '',
  status: '',
  verdicts: [],
  range: 'all',
};

export const isFiltered = (controls: RequestControls): boolean =>
  JSON.stringify(controls) !== JSON.stringify(DEFAULT_CONTROLS);

export function requestFilterFromControls(controls: RequestControls, nowMs: number): RequestFilter {
  const filter: RequestFilter = {};
  const session = controls.session.trim();
  if (session) filter.session = session;
  if (controls.key) filter.key = controls.key;
  if (controls.auth) filter.auth = controls.auth;
  if (controls.model) filter.model = controls.model;
  if (controls.status) filter.status = controls.status;
  if (controls.verdicts.length) filter.verdict = controls.verdicts;
  if (controls.range !== 'all') filter.fromMs = nowMs - RANGE_MS[controls.range];
  return filter;
}

export interface KnownOptions {
  models: string[];
  auths: string[];
  keys: string[];
}

/** Dropdown values accumulate, so narrowing a filter does not hide the others. */
export function collectOptions(known: KnownOptions, entries: RequestEntry[]): KnownOptions {
  const add = (list: string[], value: string) =>
    value && value !== '-' && !list.includes(value) ? [...list, value] : list;
  let { models, auths, keys } = known;
  for (const entry of entries) {
    models = add(models, entry.model);
    auths = add(auths, entry.authId);
    keys = add(keys, entry.keyHash);
  }
  return { models, auths, keys };
}

// Labels

const KNOWN_CAUSES = new Set([
  'credential_changed',
  'model_changed',
  'tools_changed',
  'system_changed',
  'settings_changed',
  'message_changed',
  'instructions_changed',
  'history_truncated',
  'idle_gap',
  'cache_key_changed',
  'lookback_exceeded',
  'no_breakpoint',
  'volatile_changed',
  'unexplained',
]);

const KNOWN_NA_REASONS = new Set([
  'failed',
  'no_usage',
  'no_session',
  'unsupported',
  'below_minimum',
  'concurrent',
]);

const KNOWN_SEGMENTS = new Set(['tools', 'system', 'settings', 'message', 'instructions', 'input']);

export const verdictKey = (verdict: CacheVerdict) => `request_log.verdict_${verdict}`;

export const causeKey = (cause: string) =>
  `request_log.cause_${KNOWN_CAUSES.has(cause) ? cause : 'unknown'}`;

export const naReasonKey = (reason: string) =>
  `request_log.na_${KNOWN_NA_REASONS.has(reason) ? reason : 'unknown'}`;

export interface TranslationRef {
  key: string;
  values: Record<string, number | string>;
}

/** Where the cache prefix diverged: "message 14", "system block 1", "tools 37 to 38". */
export function segmentLabel(cache: CacheDiagnosis): TranslationRef | null {
  if (cache.tools) {
    return {
      key: 'request_log.segment_tools_delta',
      values: { from: cache.tools.from, to: cache.tools.to },
    };
  }
  const segment = cache.segment;
  if (!segment) return null;
  const kind = KNOWN_SEGMENTS.has(segment.kind) ? segment.kind : 'other';
  return { key: `request_log.segment_${kind}`, values: { index: segment.index } };
}

/** Saved key name first, then the backend's mask, then the hash. Empty means no client key. */
export function buildClientLabels(input: {
  hashes: readonly string[];
  keys: Record<string, string>;
  apiKeys: readonly string[] | undefined;
  names: Record<string, string>;
  apiBase: string;
}): Map<string, string> {
  const saved = new Map<string, string>();
  for (const key of input.apiKeys ?? []) {
    const hash = clientKeyHash(key);
    const name = input.names[apiKeyNameFingerprint(input.apiBase, key)]?.trim();
    if (hash !== '-' && name && !saved.has(hash)) saved.set(hash, name);
  }
  const labels = new Map<string, string>();
  for (const hash of input.hashes) {
    if (!hash || hash === '-') labels.set(hash, '');
    else labels.set(hash, saved.get(hash) ?? input.keys[hash] ?? `#${hash}`);
  }
  return labels;
}

// Session timeline

export interface TimelineBar {
  seq: number;
  atMs: number;
  /** Share of the tallest prompt in the window, with a visible minimum. */
  heightPct: number;
  /** Shares of this bar's own prompt. */
  readPct: number;
  writePct: number;
  uncachedPct: number;
  verdict: CacheVerdict;
  /** Primary cause of a miss or partial; empty otherwise. */
  cause: string;
  failed: boolean;
}

const MIN_BAR_PCT = 2;

export function sessionTimeline(entries: readonly RequestEntry[]): TimelineBar[] {
  const prompts = entries.map(
    (entry) =>
      entry.tokens.prompt || entry.tokens.input + entry.tokens.cacheRead + entry.tokens.cacheWrite
  );
  const tallest = Math.max(0, ...prompts);
  return entries.map((entry, index) => {
    const prompt = prompts[index];
    const share = (value: number) => (prompt > 0 ? (value / prompt) * 100 : 0);
    const flagged = entry.cache.verdict === 'miss' || entry.cache.verdict === 'partial';
    return {
      seq: entry.seq,
      atMs: entry.atMs,
      heightPct: prompt > 0 && tallest > 0 ? Math.max(MIN_BAR_PCT, (prompt / tallest) * 100) : 0,
      readPct: share(entry.tokens.cacheRead),
      writePct: share(entry.tokens.cacheWrite),
      uncachedPct: share(entry.tokens.input),
      verdict: entry.cache.verdict,
      cause: flagged ? entry.cache.cause : '',
      failed: entry.failed,
    };
  });
}

// Formatting

/**
 * Short session label: the backend prefixes ids with the provider
 * ("claude:<uuid>") and appends ":agent:<id>" for subagents.
 */
export const shortId = (id: string): string => {
  const [main, agent] = id.replace(/^[a-z]+:(?=.)/i, '').split(':agent:');
  const head = main.slice(0, 8);
  return agent ? `${head} › ${agent.slice(0, 6)}` : head;
};

export function formatLatency(ms: number): string {
  if (!ms) return '-';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const seconds = ms / 1000;
  return `${Number(seconds.toFixed(seconds < 10 ? 1 : 0))} s`;
}

export function formatSpan(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)} s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} m`;
  return `${Number((seconds / 3600).toFixed(1))} h`;
}
