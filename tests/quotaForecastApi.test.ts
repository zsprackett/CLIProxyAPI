import { describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import { normalizeQuotaForecast, quotaForecastApi } from '@/services/api/quotaForecast';
import { NOW_MS, rawClaudeForecast } from './fixtures/quotaForecast';

const H_MS = 3_600_000;

describe('normalizeQuotaForecast', () => {
  test('converts snake_case, RFC 3339 instants and chart unix seconds', () => {
    const forecast = normalizeQuotaForecast(rawClaudeForecast());
    expect(forecast.enabled).toBe(true);
    expect(forecast.reason).toBeNull();
    expect(forecast.recordingSinceMs).toBe(NOW_MS - 48 * H_MS);
    expect(forecast.quotaPollEnabled).toBe(true);
    expect(forecast.lookbackHours).toBe(24);
    expect(forecast.pools.map((pool) => pool.class)).toEqual(['claude:5h', 'claude:7d']);

    const [pool, weekly] = forecast.pools;
    expect(pool.accountsWithData).toBe(2);
    expect(pool.nextResetAtMs).toBe(NOW_MS + 5 * H_MS);
    expect(pool.burn).toEqual({
      accountsPerHour: 0.2,
      low: 0.1,
      high: 0.3,
      coverageSeconds: 21600,
      sufficient: true,
    });
    expect(pool.runway.status).toBe('short');
    expect(pool.runway.exhaustsAtMs).toBe(NOW_MS + 3 * H_MS);
    expect(pool.runway.spareAtNextResetAccounts).toBeNull();
    expect(pool.pace).toEqual({ usedPercent: 40, expectedPercent: 30, deltaPp: 10 });
    expect(pool.accounts[1]).toMatchObject({
      authId: 'a',
      resetAtMs: null,
      elapsedFraction: null,
      projectedUsedAtReset: null,
    });

    expect(pool.chart?.nowMs).toBe(NOW_MS);
    expect(pool.chart?.history.times).toEqual([NOW_MS - 6 * H_MS, NOW_MS - 3 * H_MS, NOW_MS]);
    expect(pool.chart?.history.total).toEqual([null, 1.6, 1.2]);
    expect(pool.chart?.projection.times[0]).toBe(pool.chart?.nowMs);
    expect(pool.chart?.resets).toEqual([{ atMs: NOW_MS + 5 * H_MS, authId: 'b' }]);
    expect(pool.chart?.danger).toEqual([
      { fromMs: NOW_MS + 3 * H_MS, toMs: NOW_MS + 5 * H_MS, severity: 'projected' },
    ]);
    expect(pool.hourlyBurn[0]).toEqual({ hourStartMs: NOW_MS - H_MS, accounts: 0.2, coverage: 1 });
    expect(pool.attribution?.method).toBe('nnls');
    expect(pool.attribution?.clients[0]).toMatchObject({ keyMasked: 'sk**********ha', share: 0.6 });

    expect(weekly.pace).toBeNull();
    expect(weekly.chart).toBeNull();
    expect(weekly.accounts).toEqual([]);
    expect(weekly.attribution).toBeNull();
    expect(weekly.runway.spareAtNextResetAccounts).toBe(0.78);

    expect(forecast.alerts.map((alert) => alert.state)).toEqual(['firing', 'resolved']);
    expect(forecast.alerts[0].exhaustsAtMs).toBe(NOW_MS + 3 * H_MS);
  });

  test('tolerates missing, null and garbage input', () => {
    for (const raw of [null, undefined, 'nope', 42, [], {}]) {
      const forecast = normalizeQuotaForecast(raw);
      expect(forecast.enabled).toBe(false);
      expect(forecast.pools).toEqual([]);
      expect(forecast.alerts).toEqual([]);
    }

    const forecast = normalizeQuotaForecast({
      enabled: false,
      reason: 'disabled',
      generated_at: '0001-01-01T00:00:00Z',
      pools: [
        null,
        { class: '' },
        {
          class: 'codex:weekly',
          remaining_accounts: 'lots',
          runway: { status: 'mystery', exhausts_at: 'yesterday' },
          burn: { sufficient: 'yes' },
          chart: { start: 10, now: 20, end: 5 },
          hourly_burn: [[1, 2], 'x', [3600, 0.5, 0], [7200, 0.4, 2]],
        },
      ],
      alerts: [{ class: 'codex:weekly', state: 'exploding' }, { state: 'firing' }],
    });
    expect(forecast.reason).toBe('disabled');
    expect(forecast.generatedAtMs).toBeNull();
    expect(forecast.pools).toHaveLength(1);
    const [pool] = forecast.pools;
    expect(pool.provider).toBe('codex');
    expect(pool.remainingAccounts).toBe(0);
    expect(pool.runway.status).toBe('insufficient_data');
    expect(pool.runway.exhaustsAtMs).toBeNull();
    expect(pool.burn.sufficient).toBe(false);
    expect(pool.chart).toBeNull();
    expect(pool.hourlyBurn).toEqual([{ hourStartMs: 7_200_000, accounts: 0.4, coverage: 1 }]);
    expect(forecast.alerts).toHaveLength(1);
    expect(forecast.alerts[0].state).toBe('resolved');
  });

  test('keeps chart series aligned when entries are invalid', () => {
    const raw = rawClaudeForecast();
    const chart = raw.pools[0].chart as Record<string, unknown>;
    chart.history = {
      times: [100, 'bad', 300],
      total: [1, 2, 3],
      by_account: [{ auth_id: 'a', values: [10, 20] }, { values: [1] }],
    };
    chart.projection = {
      times: [300, 400, 500],
      central: [1, Number.NaN, 0.5],
      optimistic: [1, 1, 1],
      pessimistic: [1, 1],
    };
    chart.danger = [{ from: 500, to: 400, severity: 'projected' }];
    const normalized = normalizeQuotaForecast(raw).pools[0].chart;
    expect(normalized?.history.times).toEqual([100_000, 300_000]);
    expect(normalized?.history.total).toEqual([1, 3]);
    expect(normalized?.history.byAccount).toEqual([{ authId: 'a', values: [10, null] }]);
    expect(normalized?.projection.times).toEqual([300_000]);
    expect(normalized?.danger).toEqual([]);
  });
});

describe('quotaForecastApi', () => {
  test('requests the forecast endpoint with provider and lookback', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(rawClaudeForecast());
    try {
      const forecast = await quotaForecastApi.get({ provider: 'claude', lookbackHours: 12 });
      expect(get).toHaveBeenCalledWith('/observability/quota/forecast', {
        params: { provider: 'claude', lookback_hours: 12 },
      });
      expect(forecast.pools).toHaveLength(2);

      await quotaForecastApi.get({ provider: null });
      expect(get).toHaveBeenLastCalledWith('/observability/quota/forecast', { params: {} });
    } finally {
      get.mockRestore();
    }
  });
});
