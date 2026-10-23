/**
 * Pooled quota trend & forecast (GET /observability/quota/forecast).
 *
 * Instants become epoch milliseconds: RFC 3339 strings at the top level and
 * unix seconds inside `chart` and `hourly_burn`. Untrusted input degrades to
 * empty values rather than throwing, so a partial payload still renders.
 */

import { apiClient } from './client';

export const QUOTA_FORECAST_PROVIDERS = ['claude', 'codex', 'xai', 'devin'] as const;
export type QuotaForecastProvider = (typeof QUOTA_FORECAST_PROVIDERS)[number];

export type ForecastRunwayStatus = 'ok' | 'tight' | 'short' | 'insufficient_data';
export type ForecastAttributionMethod = 'nnls' | 'proportional' | 'none';

export interface ForecastBurn {
  accountsPerHour: number;
  low: number;
  high: number;
  coverageSeconds: number;
  sufficient: boolean;
}

export interface ForecastRunway {
  status: ForecastRunwayStatus;
  exhaustsAtMs: number | null;
  recoversAtMs: number | null;
  shortSeconds: number;
  shortAccounts: number;
  spareAtNextResetAccounts: number | null;
  troughAccounts: number;
  troughAtMs: number | null;
}

export interface ForecastPace {
  usedPercent: number;
  expectedPercent: number;
  /** Positive = burning faster than an even pace. */
  deltaPp: number;
}

export interface ForecastAccount {
  authId: string;
  name: string;
  usedPercent: number;
  remainingPercent: number;
  resetAtMs: number | null;
  observedAtMs: number | null;
  elapsedFraction: number | null;
  paceDeltaPp: number | null;
  burnPercentPerHour: number;
  /** Above 100 means the credential runs out before it resets. */
  projectedUsedAtReset: number | null;
}

export interface ForecastChart {
  startMs: number;
  nowMs: number;
  endMs: number;
  history: {
    times: number[];
    /** Pool total in accounts. */
    total: (number | null)[];
    /** Remaining percentage (0-100) per credential, aligned with `times`. */
    byAccount: { authId: string; values: (number | null)[] }[];
  };
  projection: { times: number[]; central: number[]; optimistic: number[]; pessimistic: number[] };
  resets: { atMs: number; authId: string }[];
  danger: { fromMs: number; toMs: number; severity: 'projected' | 'possible' }[];
}

export interface ForecastHourlyBurn {
  hourStartMs: number;
  accounts: number;
  coverage: number;
}

export interface ForecastAttributionRow {
  model: string;
  keyHash: string;
  keyMasked: string;
  agent: string;
  tokens: number;
  requests: number;
  burnAccounts: number;
  /** Percent of one account burned per million uncached tokens. */
  percentPerMTok: number;
  share: number;
}

export interface ForecastAttribution {
  method: ForecastAttributionMethod;
  /** Why attribution is empty, e.g. `usage_history_disabled`. Null when it ran. */
  reason: string | null;
  r2: number | null;
  rows: number;
  models: ForecastAttributionRow[];
  clients: ForecastAttributionRow[];
  agents: ForecastAttributionRow[];
}

export interface ForecastPool {
  class: string;
  provider: string;
  window: string;
  windowSeconds: number;
  modelScoped: boolean;
  accountsTotal: number;
  accountsWithData: number;
  remainingAccounts: number;
  nextResetAtMs: number | null;
  burn: ForecastBurn;
  runway: ForecastRunway;
  pace: ForecastPace | null;
  accounts: ForecastAccount[];
  chart: ForecastChart | null;
  hourlyBurn: ForecastHourlyBurn[];
  attribution: ForecastAttribution | null;
}

export interface ForecastAlert {
  class: string;
  provider: string;
  state: 'firing' | 'resolved';
  firedAtMs: number | null;
  exhaustsAtMs: number | null;
  recoversAtMs: number | null;
  shortAccounts: number;
  remainingAccounts: number;
}

export interface QuotaForecast {
  enabled: boolean;
  /** Why the forecast is off: `disabled`, `home_mode` or `unavailable`. */
  reason: string | null;
  generatedAtMs: number | null;
  recordingSinceMs: number | null;
  quotaPollEnabled: boolean;
  routingStrategy: string;
  drainModel: string;
  lookbackHours: number;
  retentionDays: number;
  pools: ForecastPool[];
  alerts: ForecastAlert[];
}

const asRecord = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};

const asArray = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);

const num = (value: unknown, fallback = 0): number =>
  typeof value === 'number' && Number.isFinite(value) ? value : fallback;

const numOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const str = (value: unknown): string => (typeof value === 'string' ? value : '');

/** RFC 3339 → ms; Go's zero time and garbage become null. */
const instant = (value: unknown): number | null => {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(value)) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? ms : null;
};

const unixMs = (value: unknown): number | null => {
  const seconds = numOrNull(value);
  return seconds !== null && seconds > 0 ? seconds * 1000 : null;
};

const pick = <T extends string>(value: unknown, allowed: readonly T[], fallback: T): T =>
  allowed.includes(value as T) ? (value as T) : fallback;

function readChart(raw: unknown): ForecastChart | null {
  const chart = asRecord(raw);
  const startMs = unixMs(chart.start);
  const nowMs = unixMs(chart.now);
  const endMs = unixMs(chart.end);
  if (startMs === null || nowMs === null || endMs === null || endMs <= startMs) return null;

  // History keeps an index only when its time is valid, so every series stays aligned.
  const history = asRecord(chart.history);
  const rawTimes = asArray(history.times);
  const keep = rawTimes.map((value) => unixMs(value) !== null);
  const aligned = (values: unknown): (number | null)[] => {
    const list = asArray(values);
    return rawTimes.flatMap((_, index) => (keep[index] ? [numOrNull(list[index])] : []));
  };

  const projection = asRecord(chart.projection);
  const projectionColumns = [
    asArray(projection.times),
    asArray(projection.central),
    asArray(projection.optimistic),
    asArray(projection.pessimistic),
  ];
  const projected = projectionColumns[0].flatMap((rawTime, index) => {
    const time = unixMs(rawTime);
    const values = projectionColumns.slice(1).map((column) => numOrNull(column[index]));
    return time !== null && values.every((value) => value !== null)
      ? [{ time, values: values as number[] }]
      : [];
  });

  return {
    startMs,
    nowMs,
    endMs,
    history: {
      times: rawTimes.flatMap((value) => {
        const ms = unixMs(value);
        return ms === null ? [] : [ms];
      }),
      total: aligned(history.total),
      byAccount: asArray(history.by_account)
        .map(asRecord)
        .filter((account) => str(account.auth_id))
        .map((account) => ({ authId: str(account.auth_id), values: aligned(account.values) })),
    },
    projection: {
      times: projected.map((point) => point.time),
      central: projected.map((point) => point.values[0]),
      optimistic: projected.map((point) => point.values[1]),
      pessimistic: projected.map((point) => point.values[2]),
    },
    resets: asArray(chart.resets).flatMap((value) => {
      const reset = asRecord(value);
      const atMs = unixMs(reset.at);
      return atMs === null ? [] : [{ atMs, authId: str(reset.auth_id) }];
    }),
    danger: asArray(chart.danger).flatMap((value) => {
      const danger = asRecord(value);
      const fromMs = unixMs(danger.from);
      const toMs = unixMs(danger.to);
      if (fromMs === null || toMs === null || toMs <= fromMs) return [];
      const severity = pick(danger.severity, ['projected', 'possible'] as const, 'possible');
      return [{ fromMs, toMs, severity }];
    }),
  };
}

function readAttributionRow(raw: unknown): ForecastAttributionRow {
  const row = asRecord(raw);
  return {
    model: str(row.model),
    keyHash: str(row.key_hash),
    keyMasked: str(row.key_masked),
    agent: str(row.agent),
    tokens: num(row.tokens),
    requests: num(row.requests),
    burnAccounts: num(row.burn_accounts),
    percentPerMTok: num(row.percent_per_mtok),
    share: Math.min(1, Math.max(0, num(row.share))),
  };
}

function readAttribution(raw: unknown): ForecastAttribution | null {
  if (raw === null || typeof raw !== 'object') return null;
  const attribution = asRecord(raw);
  return {
    method: pick(attribution.method, ['nnls', 'proportional', 'none'] as const, 'none'),
    reason: str(attribution.reason) || null,
    r2: numOrNull(attribution.r2),
    rows: num(attribution.rows),
    models: asArray(attribution.models).map(readAttributionRow),
    clients: asArray(attribution.clients).map(readAttributionRow),
    agents: asArray(attribution.agents).map(readAttributionRow),
  };
}

function readPool(raw: unknown): ForecastPool | null {
  const pool = asRecord(raw);
  const poolClass = str(pool.class);
  if (!poolClass) return null;
  const burn = asRecord(pool.burn);
  const runway = asRecord(pool.runway);
  const pace = pool.pace === null || typeof pool.pace !== 'object' ? null : asRecord(pool.pace);
  return {
    class: poolClass,
    provider: str(pool.provider) || poolClass.split(':')[0],
    window: str(pool.window),
    windowSeconds: num(pool.window_seconds),
    modelScoped: pool.model_scoped === true,
    accountsTotal: num(pool.accounts_total),
    accountsWithData: num(pool.accounts_with_data),
    remainingAccounts: num(pool.remaining_accounts),
    nextResetAtMs: instant(pool.next_reset_at),
    burn: {
      accountsPerHour: num(burn.accounts_per_hour),
      low: num(burn.low),
      high: num(burn.high),
      coverageSeconds: num(burn.coverage_seconds),
      sufficient: burn.sufficient === true,
    },
    runway: {
      status: pick(
        runway.status,
        ['ok', 'tight', 'short', 'insufficient_data'] as const,
        'insufficient_data'
      ),
      exhaustsAtMs: instant(runway.exhausts_at),
      recoversAtMs: instant(runway.recovers_at),
      shortSeconds: num(runway.short_seconds),
      shortAccounts: num(runway.short_accounts),
      spareAtNextResetAccounts: numOrNull(runway.spare_at_next_reset_accounts),
      troughAccounts: num(runway.trough_accounts),
      troughAtMs: instant(runway.trough_at),
    },
    pace: pace && {
      usedPercent: num(pace.used_percent),
      expectedPercent: num(pace.expected_percent),
      deltaPp: num(pace.delta_pp),
    },
    accounts: asArray(pool.accounts)
      .map(asRecord)
      .filter((account) => str(account.auth_id))
      .map((account) => ({
        authId: str(account.auth_id),
        name: str(account.name) || str(account.auth_id),
        usedPercent: num(account.used_percent),
        remainingPercent: num(account.remaining_percent),
        resetAtMs: instant(account.reset_at),
        observedAtMs: instant(account.observed_at),
        elapsedFraction: numOrNull(account.elapsed_fraction),
        paceDeltaPp: numOrNull(account.pace_delta_pp),
        burnPercentPerHour: num(account.burn_percent_per_hour),
        projectedUsedAtReset: numOrNull(account.projected_used_at_reset),
      })),
    chart: readChart(pool.chart),
    hourlyBurn: asArray(pool.hourly_burn).flatMap((value) => {
      const sample = asArray(value);
      const hourStartMs = unixMs(sample[0]);
      const accounts = numOrNull(sample[1]);
      const coverage = numOrNull(sample[2]);
      return hourStartMs === null || accounts === null || coverage === null || coverage <= 0
        ? []
        : [{ hourStartMs, accounts, coverage: Math.min(1, coverage) }];
    }),
    attribution: readAttribution(pool.attribution),
  };
}

export function normalizeQuotaForecast(raw: unknown): QuotaForecast {
  const data = asRecord(raw);
  return {
    enabled: data.enabled === true,
    reason: str(data.reason) || null,
    generatedAtMs: instant(data.generated_at),
    recordingSinceMs: instant(data.recording_since),
    quotaPollEnabled: data.quota_poll_enabled === true,
    routingStrategy: str(data.routing_strategy),
    drainModel: str(data.drain_model),
    lookbackHours: num(data.lookback_hours),
    retentionDays: num(data.retention_days),
    pools: asArray(data.pools)
      .map(readPool)
      .filter((pool): pool is ForecastPool => pool !== null),
    alerts: asArray(data.alerts).flatMap((value) => {
      const alert = asRecord(value);
      const alertClass = str(alert.class);
      if (!alertClass) return [];
      return [
        {
          class: alertClass,
          provider: str(alert.provider) || alertClass.split(':')[0],
          state: pick(alert.state, ['firing', 'resolved'] as const, 'resolved'),
          firedAtMs: instant(alert.fired_at),
          exhaustsAtMs: instant(alert.exhausts_at),
          recoversAtMs: instant(alert.recovers_at),
          shortAccounts: num(alert.short_accounts),
          remainingAccounts: num(alert.remaining_accounts),
        },
      ];
    }),
  };
}

export const quotaForecastApi = {
  async get(
    params: { provider?: QuotaForecastProvider | null; lookbackHours?: number } = {}
  ): Promise<QuotaForecast> {
    const query: Record<string, string | number> = {};
    if (params.provider) query.provider = params.provider;
    if (params.lookbackHours) query.lookback_hours = params.lookbackHours;
    const raw = await apiClient.get<unknown>('/observability/quota/forecast', { params: query });
    return normalizeQuotaForecast(raw);
  },
};
