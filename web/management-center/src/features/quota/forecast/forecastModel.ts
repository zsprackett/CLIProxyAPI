/**
 * Pure helpers for the quota trend & forecast section.
 *
 * Clock-free: `nowMs` and the time-zone offset are always injected so every
 * descriptor and every chart coordinate is deterministic under test.
 */

import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex } from '@noble/hashes/utils.js';
import { apiKeyNameFingerprint } from '@/features/config/apiKeyNames';
import type {
  ForecastAttributionRow,
  ForecastChart,
  ForecastHourlyBurn,
  ForecastPool,
} from '@/services/api/quotaForecast';
import { maskApiKey } from '@/utils/format';
import type { RemainingTone } from '../components/ledgerTone';

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;

/* ---------- Labels ---------- */

/** Pool classes reuse the window labels the quota cards already show. */
const CLASS_LABEL_KEYS: Record<string, string> = {
  'claude:5h': 'claude_quota.five_hour',
  'claude:7d': 'claude_quota.seven_day',
  'claude:7d_oi': 'claude_quota.seven_day_fable',
  'codex:5h': 'codex_quota.primary_window',
  'codex:weekly': 'codex_quota.secondary_window',
  'codex:monthly': 'codex_quota.team_secondary_window',
  'xai:weekly': 'xai_quota.weekly_limit',
  'xai:monthly': 'xai_quota.monthly_credits',
  'devin:daily': 'quota_management.ledger_window_daily',
  'devin:weekly': 'quota_management.ledger_window_weekly',
};

/** i18n key for a pool class, or null for a class this build does not know. */
export function forecastClassLabelKey(poolClass: string): string | null {
  return CLASS_LABEL_KEYS[poolClass] ?? null;
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

export function forecastClassLabel(t: Translate, poolClass: string): string {
  const key = forecastClassLabelKey(poolClass);
  return key ? t(key) : poolClass;
}

/** The pool to open first: the one running out soonest, else the longest window. */
export function pickDefaultClass(pools: ForecastPool[]): string | null {
  let pick: ForecastPool | null = null;
  for (const pool of pools) {
    const exhausts = pool.runway.exhaustsAtMs;
    if (exhausts === null) continue;
    if (!pick || exhausts < (pick.runway.exhaustsAtMs ?? Infinity)) pick = pool;
  }
  if (pick) return pick.class;
  for (const pool of pools) {
    if (!pick || pool.windowSeconds > pick.windowSeconds) pick = pool;
  }
  return pick?.class ?? null;
}

/* ---------- Numbers and durations ---------- */

/** Accounts read to two decimals below one, otherwise one. */
export function formatAccounts(value: number): string {
  return Math.abs(value) < 1 ? value.toFixed(2) : value.toFixed(1);
}

/** Compact duration (`2d 4h`, `3h 20m`, `45m`), truncated like the quota countdowns. */
export function formatForecastDuration(ms: number, t: Translate): string {
  const total = Math.max(0, ms);
  const days = Math.floor(total / DAY_MS);
  const hours = Math.floor((total % DAY_MS) / HOUR_MS);
  const minutes = Math.floor((total % HOUR_MS) / MINUTE_MS);
  if (days > 0) return t('quota_management.forecast_duration_days', { days, hours });
  if (hours > 0) return t('quota_management.forecast_duration_hours', { hours, minutes });
  return t('quota_management.forecast_duration_minutes', { minutes: Math.max(1, minutes) });
}

/* ---------- Descriptors ---------- */

export type RunwayHeadline =
  | { kind: 'insufficient' }
  | { kind: 'short'; exhaustsInMs: number; shortMs: number; shortAccounts: number }
  | { kind: 'tight'; troughAccounts: number; resetInMs: number | null }
  | { kind: 'spare'; spareAccounts: number; resetInMs: number | null }
  | { kind: 'ok'; resetInMs: number | null };

export function describeRunway(pool: ForecastPool, nowMs: number): RunwayHeadline {
  const { runway } = pool;
  if (!pool.burn.sufficient || runway.status === 'insufficient_data') {
    return { kind: 'insufficient' };
  }
  const resetInMs = pool.nextResetAtMs === null ? null : Math.max(0, pool.nextResetAtMs - nowMs);
  if (runway.status === 'short') {
    return {
      kind: 'short',
      exhaustsInMs: Math.max(0, (runway.exhaustsAtMs ?? nowMs) - nowMs),
      shortMs: Math.max(0, runway.shortSeconds * 1000),
      shortAccounts: runway.shortAccounts,
    };
  }
  if (runway.status === 'tight') {
    return { kind: 'tight', troughAccounts: runway.troughAccounts, resetInMs };
  }
  if (runway.spareAtNextResetAccounts !== null) {
    return { kind: 'spare', spareAccounts: runway.spareAtNextResetAccounts, resetInMs };
  }
  return { kind: 'ok', resetInMs };
}

/** One-line runway sentence shared by the headline, the All tab rows and alerts. */
export function formatRunway(headline: RunwayHeadline, t: Translate): string {
  const reset = (resetInMs: number | null) =>
    resetInMs === null ? '' : formatForecastDuration(resetInMs, t);
  switch (headline.kind) {
    case 'insufficient':
      return t('quota_management.forecast_insufficient');
    case 'short':
      return t('quota_management.forecast_runway_short', {
        exhausts: formatForecastDuration(headline.exhaustsInMs, t),
        short: formatForecastDuration(headline.shortMs, t),
        accounts: formatAccounts(headline.shortAccounts),
      });
    case 'tight':
      return t('quota_management.forecast_runway_tight', {
        accounts: formatAccounts(headline.troughAccounts),
      });
    case 'spare':
      return headline.resetInMs === null
        ? t('quota_management.forecast_runway_spare_unknown', {
            accounts: formatAccounts(headline.spareAccounts),
          })
        : t('quota_management.forecast_runway_spare', {
            accounts: formatAccounts(headline.spareAccounts),
            duration: reset(headline.resetInMs),
          });
    case 'ok':
      return headline.resetInMs === null
        ? t('quota_management.forecast_runway_ok_unknown')
        : t('quota_management.forecast_runway_ok', { duration: reset(headline.resetInMs) });
  }
}

/** Status tone, using the same three colors as the quota meters. */
export function runwayTone(headline: RunwayHeadline): RemainingTone {
  if (headline.kind === 'short') return 'toneLow';
  if (headline.kind === 'tight') return 'toneMedium';
  if (headline.kind === 'insufficient') return 'toneUnknown';
  return 'toneHigh';
}

export type PaceKind = 'on' | 'over' | 'under';

/** Within two points of an even pace reads as on pace. */
export function describePace(deltaPp: number): { kind: PaceKind; points: number } {
  const points = Math.round(Math.abs(deltaPp));
  if (Math.abs(deltaPp) < 2) return { kind: 'on', points };
  return { kind: deltaPp > 0 ? 'over' : 'under', points };
}

/** Short windows read per hour; anything longer than a day reads per day. */
export function burnDisplay(pool: ForecastPool): {
  unit: 'hour' | 'day';
  rate: number;
  low: number;
  high: number;
} {
  const factor = pool.windowSeconds > 0 && pool.windowSeconds <= 86_400 ? 1 : 24;
  return {
    unit: factor === 1 ? 'hour' : 'day',
    rate: pool.burn.accountsPerHour * factor,
    low: pool.burn.low * factor,
    high: pool.burn.high * factor,
  };
}

/* ---------- Chart geometry ---------- */

export type ForecastChartView = 'remaining' | 'stacked';

export interface ForecastGeometry {
  width: number;
  height: number;
  yMax: number;
  xOf: (ms: number) => number;
  historyPath: string;
  projectionPath: string;
  bandPath: string;
  stacked: { authId: string; path: string }[];
  dangerRects: { x: number; width: number; severity: 'projected' | 'possible' }[];
  resetXs: number[];
  nowX: number;
  yTicks: { value: number; y: number }[];
  xTicks: { ms: number; x: number }[];
}

export type TzOffsetMinutes = (ms: number) => number;

export const localTzOffsetMinutes: TzOffsetMinutes = (ms) => -new Date(ms).getTimezoneOffset();

const round = (value: number) => Math.round(value * 100) / 100;

function niceStep(max: number, intervals: number): number {
  const raw = max / intervals;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  for (const multiple of [1, 2, 2.5, 5, 10]) {
    if (multiple * magnitude >= raw) return multiple * magnitude;
  }
  return 10 * magnitude;
}

/** Remaining-percentage series per credential in a stable (auth_id) order. */
function sortedAccounts(chart: ForecastChart) {
  return [...chart.history.byAccount].sort((a, b) => a.authId.localeCompare(b.authId));
}

export function buildForecastGeometry(
  chart: ForecastChart,
  view: ForecastChartView,
  box: { width: number; height: number },
  options: { capacity?: number; tzOffsetMinutes?: TzOffsetMinutes } = {}
): ForecastGeometry {
  const { width, height } = box;
  const { startMs, endMs, history, projection } = chart;
  const span = Math.max(1, endMs - startMs);
  const xOf = (ms: number) =>
    round(((Math.min(endMs, Math.max(startMs, ms)) - startMs) / span) * width);

  const peak = Math.max(
    1,
    options.capacity ?? 0,
    ...history.total.map((value) => value ?? 0),
    ...projection.optimistic,
    ...projection.pessimistic
  );
  const step = niceStep(peak, 4);
  const yMax = Math.ceil(peak / step - 1e-9) * step;
  const yOf = (value: number) => round(height - (Math.max(0, value) / yMax) * height);

  const xs = history.times.map(xOf);

  // History: step line, broken where the pool total is unknown.
  let historyPath = '';
  let drawing = false;
  history.total.forEach((value, index) => {
    if (value === null || xs[index] === undefined) {
      drawing = false;
      return;
    }
    historyPath += drawing ? `H${xs[index]}V${yOf(value)}` : `M${xs[index]} ${yOf(value)}`;
    drawing = true;
  });

  // Projection: straight declines, vertical step-ups where a credential resets.
  const px = projection.times.map(xOf);
  let projectionPath = '';
  projection.central.forEach((value, index) => {
    const y = yOf(value);
    if (index === 0) {
      projectionPath = `M${px[0]} ${y}`;
      return;
    }
    const previous = projection.central[index - 1];
    if (value > previous + 1e-9) {
      const reset = chart.resets.find(
        (item) => item.atMs > projection.times[index - 1] && item.atMs <= projection.times[index]
      );
      const jumpX = reset ? xOf(reset.atMs) : px[index];
      projectionPath += `L${jumpX} ${yOf(previous)}V${y}`;
      if (jumpX !== px[index]) projectionPath += `L${px[index]} ${y}`;
    } else {
      projectionPath += `L${px[index]} ${y}`;
    }
  });

  const bandPath =
    projection.times.length > 1
      ? `M${projection.optimistic.map((value, index) => `${px[index]} ${yOf(value)}`).join('L')}` +
        `L${projection.pessimistic
          .map((value, index) => `${px[index]} ${yOf(value)}`)
          .reverse()
          .join('L')}Z`
      : '';

  // Stacked areas: cumulative remaining accounts, lower edge traced backwards.
  const stacked: ForecastGeometry['stacked'] = [];
  if (view === 'stacked' && xs.length > 0) {
    let lower = xs.map(() => 0);
    for (const account of sortedAccounts(chart)) {
      const upper = lower.map((base, index) => base + (account.values[index] ?? 0) / 100);
      let path = `M${xs[0]} ${yOf(upper[0])}`;
      for (let index = 1; index < xs.length; index += 1) {
        path += `H${xs[index]}V${yOf(upper[index])}`;
      }
      path += `V${yOf(lower[xs.length - 1])}`;
      for (let index = xs.length - 1; index >= 1; index -= 1) {
        path += `V${yOf(lower[index - 1])}H${xs[index - 1]}`;
      }
      stacked.push({ authId: account.authId, path: `${path}Z` });
      lower = upper;
    }
  }

  const dangerRects = chart.danger.flatMap((danger) => {
    const x = xOf(danger.fromMs);
    const rectWidth = round(xOf(danger.toMs) - x);
    return rectWidth > 0 ? [{ x, width: rectWidth, severity: danger.severity }] : [];
  });

  const resetXs = Array.from(
    new Set(
      chart.resets
        .filter((reset) => reset.atMs >= startMs && reset.atMs <= endMs)
        .map((reset) => xOf(reset.atMs))
    )
  ).sort((a, b) => a - b);

  const yTicks: ForecastGeometry['yTicks'] = [];
  for (let value = 0; value <= yMax + 1e-9; value += step) {
    yTicks.push({ value: round(value), y: yOf(value) });
  }

  // X ticks land on local hour (or midnight) boundaries, at most six of them.
  const tzOffset = options.tzOffsetMinutes ?? localTzOffsetMinutes;
  const stepHours = [1, 2, 3, 6, 12, 24, 48, 72].find((hours) => span / (hours * HOUR_MS) <= 6);
  const tickMs = (stepHours ?? 168) * HOUR_MS;
  const offsetMs = tzOffset(startMs) * MINUTE_MS;
  const xTicks: ForecastGeometry['xTicks'] = [];
  for (
    let ms = Math.ceil((startMs + offsetMs) / tickMs) * tickMs - offsetMs;
    ms <= endMs;
    ms += tickMs
  ) {
    xTicks.push({ ms, x: xOf(ms) });
  }

  return {
    width,
    height,
    yMax,
    xOf,
    historyPath,
    projectionPath,
    bandPath,
    stacked,
    dangerRects,
    resetXs,
    nowX: xOf(chart.nowMs),
    yTicks,
    xTicks,
  };
}

export interface ChartPoint {
  ms: number;
  /** Recorded pool total, or null when the point lies in the projection. */
  actual: number | null;
  projected: { central: number; optimistic: number; pessimistic: number } | null;
}

/** Nearest recorded or projected sample to an instant, for the hover readout. */
export function chartPointAt(chart: ForecastChart, ms: number): ChartPoint | null {
  const nearest = (times: number[]) => {
    let best = -1;
    times.forEach((time, index) => {
      if (best < 0 || Math.abs(time - ms) < Math.abs(times[best] - ms)) best = index;
    });
    return best;
  };
  if (ms <= chart.nowMs || chart.projection.times.length === 0) {
    const index = nearest(chart.history.times);
    if (index < 0) return null;
    return { ms: chart.history.times[index], actual: chart.history.total[index], projected: null };
  }
  const index = nearest(chart.projection.times);
  return {
    ms: chart.projection.times[index],
    actual: null,
    projected: {
      central: chart.projection.central[index],
      optimistic: chart.projection.optimistic[index],
      pessimistic: chart.projection.pessimistic[index],
    },
  };
}

/* ---------- Heatmap ---------- */

export interface HeatmapCell {
  /** Mean accounts burned per covered hour, or null without samples. */
  mean: number | null;
  samples: number;
}

export interface Heatmap {
  /** Indexed [weekday 0 = Sunday][local hour]. */
  cells: HeatmapCell[][];
  max: number;
}

export function buildHeatmap(
  hourlyBurn: ForecastHourlyBurn[],
  tzOffsetMinutes: TzOffsetMinutes = localTzOffsetMinutes
): Heatmap {
  const sums = Array.from({ length: 7 }, () =>
    Array.from({ length: 24 }, () => ({ burn: 0, coverage: 0, samples: 0 }))
  );
  for (const sample of hourlyBurn) {
    const local = new Date(sample.hourStartMs + tzOffsetMinutes(sample.hourStartMs) * MINUTE_MS);
    const bin = sums[local.getUTCDay()][local.getUTCHours()];
    bin.burn += sample.accounts;
    bin.coverage += sample.coverage;
    bin.samples += 1;
  }
  let max = 0;
  const cells = sums.map((row) =>
    row.map(({ burn, coverage, samples }) => {
      const mean = coverage > 0 ? burn / coverage : null;
      if (mean !== null) max = Math.max(max, mean);
      return { mean, samples };
    })
  );
  return { cells, max };
}

/* ---------- Attribution labels ---------- */

/** The backend's client identifier: first 12 hex chars of SHA-256 of the trimmed key. */
export function clientKeyHash(apiKey: string): string {
  const key = apiKey.trim();
  if (!key) return '-';
  return bytesToHex(sha256(new TextEncoder().encode(key))).slice(0, 12);
}

export type ClientLabel =
  { kind: 'name' | 'masked'; text: string } | { kind: 'none' } | { kind: 'unknown'; text: string };

/**
 * Names attribution clients without ever showing a raw key: a saved key name
 * first, then the locally masked configured key, then the backend's mask.
 */
export function resolveClientLabels(
  clients: ForecastAttributionRow[],
  apiKeys: readonly string[],
  names: Record<string, string>,
  apiBase: string
): Map<string, ClientLabel> {
  const configured = new Map<string, ClientLabel>();
  for (const key of apiKeys) {
    const hash = clientKeyHash(key);
    if (hash === '-' || configured.has(hash)) continue;
    const name = names[apiKeyNameFingerprint(apiBase, key)]?.trim();
    configured.set(
      hash,
      name ? { kind: 'name', text: name } : { kind: 'masked', text: maskApiKey(key) }
    );
  }
  const labels = new Map<string, ClientLabel>();
  for (const row of clients) {
    if (row.keyHash === '-' || !row.keyHash) {
      labels.set(row.keyHash || '-', { kind: 'none' });
      continue;
    }
    labels.set(
      row.keyHash,
      configured.get(row.keyHash) ?? { kind: 'unknown', text: row.keyMasked || `#${row.keyHash}` }
    );
  }
  return labels;
}
