import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import en from '@/i18n/locales/en.json';
import { apiKeyNameFingerprint } from '@/features/config/apiKeyNames';
import {
  buildForecastGeometry,
  buildHeatmap,
  burnDisplay,
  chartPointAt,
  clientKeyHash,
  describePace,
  describeRunway,
  forecastClassLabelKey,
  formatRunway,
  pickDefaultClass,
  resolveClientLabels,
} from '@/features/quota/forecast/forecastModel';
import { normalizeQuotaForecast, type ForecastPool } from '@/services/api/quotaForecast';
import { maskApiKey } from '@/utils/format';
import { ALPHA_KEY_HASH, BETA_KEY_HASH, NOW_MS, rawClaudeForecast } from './fixtures/quotaForecast';

const H_MS = 3_600_000;
const forecast = normalizeQuotaForecast(rawClaudeForecast());
const [fiveHour, weekly] = forecast.pools;
const chart = fiveHour.chart!;
const withRunway = (pool: ForecastPool, runway: Partial<ForecastPool['runway']>): ForecastPool => ({
  ...pool,
  runway: { ...pool.runway, ...runway },
});

let originalLanguage = 'en';
beforeAll(async () => {
  originalLanguage = i18n.language;
  await i18n.changeLanguage('en');
});
afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});

describe('class labels and default pool', () => {
  test('every backend class maps to an existing translation', () => {
    const classes = [
      'claude:5h',
      'claude:7d',
      'claude:7d_oi',
      'codex:5h',
      'codex:weekly',
      'codex:monthly',
      'xai:weekly',
      'xai:monthly',
      'devin:daily',
      'devin:weekly',
    ];
    for (const poolClass of classes) {
      const key = forecastClassLabelKey(poolClass);
      expect(key).not.toBeNull();
      const [section, leaf] = key!.split('.');
      expect((en as Record<string, Record<string, string>>)[section][leaf]).toBeTruthy();
    }
    expect(forecastClassLabelKey('kimi:weekly')).toBeNull();
  });

  test('opens the pool that runs out first, else the longest window', () => {
    expect(pickDefaultClass(forecast.pools)).toBe('claude:5h');
    const calm = [withRunway(fiveHour, { exhaustsAtMs: null }), weekly];
    expect(pickDefaultClass(calm)).toBe('claude:7d');
    const both = [
      withRunway(fiveHour, { exhaustsAtMs: NOW_MS + 9 * H_MS }),
      withRunway(weekly, { exhaustsAtMs: NOW_MS + 2 * H_MS }),
    ];
    expect(pickDefaultClass(both)).toBe('claude:7d');
    expect(pickDefaultClass([])).toBeNull();
  });
});

describe('descriptors', () => {
  test('describes a short runway with time to empty and the gap before reset', () => {
    const headline = describeRunway(fiveHour, NOW_MS);
    expect(headline).toEqual({
      kind: 'short',
      exhaustsInMs: 3 * H_MS,
      shortMs: 2 * H_MS,
      shortAccounts: 0.4,
    });
    expect(formatRunway(headline, i18n.t)).toBe(
      'Runs out in 3h 0m — 2h 0m before reset (≈0.40 accounts short)'
    );
  });

  test('describes spare, tight, ok and insufficient runways', () => {
    const spare = describeRunway(weekly, NOW_MS);
    expect(spare).toEqual({ kind: 'spare', spareAccounts: 0.78, resetInMs: 72 * H_MS });
    expect(formatRunway(spare, i18n.t)).toBe('0.78 accounts spare at next reset (in 3d 0h)');

    expect(describeRunway(withRunway(weekly, { status: 'tight' }), NOW_MS).kind).toBe('tight');
    expect(
      describeRunway(withRunway(weekly, { spareAtNextResetAccounts: null }), NOW_MS).kind
    ).toBe('ok');
    const unsure = { ...fiveHour, burn: { ...fiveHour.burn, sufficient: false } };
    expect(describeRunway(unsure, NOW_MS)).toEqual({ kind: 'insufficient' });
  });

  test('treats less than two points either way as on pace', () => {
    expect(describePace(1.9)).toEqual({ kind: 'on', points: 2 });
    expect(describePace(-1.4).kind).toBe('on');
    expect(describePace(2)).toEqual({ kind: 'over', points: 2 });
    expect(describePace(-7.6)).toEqual({ kind: 'under', points: 8 });
  });

  test('shows burn per hour for short windows and per day beyond a day', () => {
    expect(burnDisplay(fiveHour)).toEqual({ unit: 'hour', rate: 0.2, low: 0.1, high: 0.3 });
    const daily = burnDisplay(weekly);
    expect(daily.unit).toBe('day');
    expect(daily.rate).toBeCloseTo(0.24);
    expect(daily.high).toBeCloseTo(0.48);
  });
});

describe('buildForecastGeometry', () => {
  const box = { width: 100, height: 100 };
  const geometry = buildForecastGeometry(chart, 'remaining', box, {
    capacity: 2,
    tzOffsetMinutes: () => 0,
  });

  test('scales to the pool capacity with even y ticks', () => {
    expect(geometry.yMax).toBe(2);
    expect(geometry.yTicks.map((tick) => tick.value)).toEqual([0, 0.5, 1, 1.5, 2]);
    expect(geometry.nowX).toBe(50);
  });

  test('draws history as a step line that skips unknown totals', () => {
    expect(geometry.historyPath).toBe('M25 20H50V40');
  });

  test('draws the projection with a vertical step-up at the reset', () => {
    expect(geometry.projectionPath).toBe('M50 40L75 80L91.67 80V50L100 50');
    expect(geometry.bandPath).toBe('M50 40L75 60L100 30L100 70L75 100L50 40Z');
    expect(geometry.resetXs).toEqual([91.67]);
  });

  test('marks danger intervals and hour-aligned x ticks', () => {
    expect(geometry.dangerRects).toEqual([{ x: 75, width: 16.67, severity: 'projected' }]);
    expect(geometry.xTicks).toHaveLength(7);
    expect(geometry.xTicks[0]).toEqual({ ms: NOW_MS - 6 * H_MS, x: 0 });
    expect(geometry.stacked).toEqual([]);
  });

  test('stacks accounts in a stable auth_id order', () => {
    const stacked = buildForecastGeometry(chart, 'stacked', box, { capacity: 2 });
    expect(stacked.stacked.map((area) => area.authId)).toEqual(['a', 'b']);
    // 'a' fills 0..0.8 accounts at the middle sample, 'b' stacks to 1.6.
    expect(stacked.stacked[0].path).toContain('H25V60');
    expect(stacked.stacked[1].path).toContain('H25V20');
    expect(stacked.stacked.every((area) => area.path.endsWith('Z'))).toBe(true);
  });

  test('reads the nearest recorded or projected sample', () => {
    expect(chartPointAt(chart, NOW_MS - 3.4 * H_MS)).toEqual({
      ms: NOW_MS - 3 * H_MS,
      actual: 1.6,
      projected: null,
    });
    expect(chartPointAt(chart, NOW_MS + 2.6 * H_MS)).toEqual({
      ms: NOW_MS + 3 * H_MS,
      actual: null,
      projected: { central: 0.4, optimistic: 0.8, pessimistic: 0 },
    });
  });
});

describe('buildHeatmap', () => {
  test('bins by local weekday and hour and averages per covered hour', () => {
    const plusOneHour = () => 60;
    const heatmap = buildHeatmap(
      [
        { hourStartMs: NOW_MS - H_MS, accounts: 0.2, coverage: 1 },
        { hourStartMs: NOW_MS - 2 * H_MS, accounts: 0.1, coverage: 0.5 },
        { hourStartMs: NOW_MS - 2 * H_MS - 7 * 24 * H_MS, accounts: 0.3, coverage: 1 },
        // Sunday 23:30 UTC is Monday 00:30 an hour east.
        { hourStartMs: NOW_MS - 12.5 * H_MS, accounts: 0.05, coverage: 1 },
      ],
      plusOneHour
    );
    expect(heatmap.cells).toHaveLength(7);
    expect(heatmap.cells[1]).toHaveLength(24);
    expect(heatmap.cells[1][12]).toEqual({ mean: 0.2, samples: 1 });
    expect(heatmap.cells[1][11].samples).toBe(2);
    expect(heatmap.cells[1][11].mean).toBeCloseTo(0.4 / 1.5);
    expect(heatmap.cells[1][0]).toEqual({ mean: 0.05, samples: 1 });
    expect(heatmap.cells[0][23]).toEqual({ mean: null, samples: 0 });
    expect(heatmap.max).toBeCloseTo(0.4 / 1.5);
  });
});

describe('resolveClientLabels', () => {
  const apiBase = 'http://localhost:8317';

  test('hashes keys like the backend: sha256 hex prefix of the trimmed key', () => {
    expect(clientKeyHash('sk-team-alpha')).toBe(ALPHA_KEY_HASH);
    expect(clientKeyHash('  sk-solo-beta \n')).toBe(BETA_KEY_HASH);
    expect(clientKeyHash('   ')).toBe('-');
  });

  test('prefers saved names, then masked configured keys, then the backend mask', () => {
    const names = { [apiKeyNameFingerprint(apiBase, 'sk-team-alpha')]: 'Team Alpha' };
    const labels = resolveClientLabels(
      fiveHour.attribution!.clients,
      ['sk-team-alpha', 'sk-solo-beta'],
      names,
      apiBase
    );
    expect(labels.get(ALPHA_KEY_HASH)).toEqual({ kind: 'name', text: 'Team Alpha' });
    expect(labels.get(BETA_KEY_HASH)).toEqual({
      kind: 'masked',
      text: maskApiKey('sk-solo-beta'),
    });
    expect(labels.get('abcdef123456')).toEqual({ kind: 'unknown', text: 'zz******yy' });
    expect(labels.get('-')).toEqual({ kind: 'none' });
    for (const label of labels.values()) {
      expect(JSON.stringify(label)).not.toContain('sk-solo-beta');
    }
  });
});
