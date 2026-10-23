import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { apiKeyNameFingerprint } from '@/features/config/apiKeyNames';
import { AttributionTable } from '@/features/quota/forecast/AttributionTable';
import { resolveClientLabels } from '@/features/quota/forecast/forecastModel';
import {
  QuotaForecastView,
  type QuotaForecastViewProps,
} from '@/features/quota/forecast/QuotaForecastView';
import { normalizeQuotaForecast } from '@/services/api/quotaForecast';
import { NOW_MS, rawClaudeForecast } from './fixtures/quotaForecast';

const forecast = normalizeQuotaForecast(rawClaudeForecast());

const render = (props: Partial<QuotaForecastViewProps>) =>
  renderToStaticMarkup(
    createElement(QuotaForecastView, {
      provider: 'claude',
      data: forecast,
      loading: false,
      error: '',
      nowMs: NOW_MS,
      showIdentities: false,
      resolvedTheme: 'light',
      ...props,
    })
  );

let originalLanguage = 'en';
beforeAll(async () => {
  originalLanguage = i18n.language;
  await i18n.changeLanguage('en');
});
afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});

describe('QuotaForecastView rendering', () => {
  test('renders the pooled headline, runway, pace, chart and heatmap for a provider', () => {
    const markup = render({});
    expect(markup).toContain('of 2 accounts');
    expect(markup).toContain('0.20 accounts/h');
    expect(markup).toContain('Runs out in 3h 0m — 2h 0m before reset (≈0.40 accounts short)');
    expect(markup).toContain('10 pts over pace');
    expect(markup).toContain('Runs out before reset');
    expect(markup).toContain('role="img"');
    expect(markup).toContain('data-severity="projected"');
    expect(markup).toContain('<path');
    expect(markup).toContain('Burn by hour of week');
    expect(markup).toContain('claude-opus');
    expect(markup).toContain('R² 0.82');
  });

  test('announces firing alerts for the provider only', () => {
    const markup = render({});
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('projected to run out in 3h 0m');
    expect(markup).not.toContain('Codex');
  });

  test('offers the pool classes and chart views as pressed toggles', () => {
    const markup = render({});
    expect(markup).toMatch(/aria-pressed="true"[^>]*>5-hour limit</);
    expect(markup).toMatch(/aria-pressed="false"[^>]*>7-day limit</);
    expect(markup).toMatch(/aria-pressed="true"[^>]*>Remaining</);
    expect(markup).toMatch(/aria-pressed="false"[^>]*>By account</);

    const stacked = render({ initialChartView: 'stacked' });
    expect(stacked).toMatch(/aria-pressed="true"[^>]*>By account</);
  });

  test('masks account identities unless they are shown', () => {
    expect(render({})).not.toContain('alice@example.com');
    expect(render({ showIdentities: true })).toContain('alice@example.com.json');
  });

  test('summarizes every pool on the All tab', () => {
    const markup = render({ provider: null });
    expect(markup).toContain('1.2 of 2 accounts left');
    expect(markup).toContain('0.78 accounts spare at next reset (in 3d 0h)');
    expect(markup).toContain('no longer projected to run out');
    expect(markup).not.toContain('role="img"');
  });

  test('explains how to enable the forecast when it is off', () => {
    const disabled = normalizeQuotaForecast({ enabled: false, reason: 'disabled' });
    const markup = render({ data: disabled });
    expect(markup).toContain('Quota forecast is off');
    expect(markup).toContain('oauth.quota-forecast.enabled: true');
  });

  test('hints at background polling when it is off', () => {
    const raw = { ...rawClaudeForecast(), quota_poll_enabled: false };
    expect(render({ data: normalizeQuotaForecast(raw) })).toContain(
      'oauth.quota-poll-interval-seconds'
    );
    expect(render({})).not.toContain('oauth.quota-poll-interval-seconds');
  });

  test('shows collecting data instead of a runway without enough history', () => {
    const raw = rawClaudeForecast();
    raw.pools[0].burn.sufficient = false;
    raw.pools[0].runway.status = 'insufficient_data';
    const markup = render({ data: normalizeQuotaForecast(raw) });
    expect(markup).toContain('Collecting data');
    expect(markup).not.toContain('Runs out in');
  });

  test('reports a failed first load', () => {
    const markup = render({ data: null, error: 'boom' });
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('boom');
    expect(render({ data: null })).toBe('');
  });
});

describe('AttributionTable client rows', () => {
  test('labels client keys by saved name or mask and never prints a raw key', () => {
    const apiBase = 'http://localhost:8317';
    const attribution = forecast.pools[0].attribution!;
    const clientLabels = resolveClientLabels(
      attribution.clients,
      ['sk-team-alpha', 'sk-solo-beta'],
      { [apiKeyNameFingerprint(apiBase, 'sk-team-alpha')]: 'Team Alpha' },
      apiBase
    );
    const markup = renderToStaticMarkup(
      createElement(AttributionTable, { attribution, clientLabels, initialGroup: 'clients' })
    );
    expect(markup).toContain('Team Alpha');
    expect(markup).toContain('sk******ta');
    expect(markup).toContain('zz******yy');
    expect(markup).toContain('No API key');
    expect(markup).not.toContain('sk-team-alpha');
    expect(markup).not.toContain('sk-solo-beta');
    expect(markup).not.toContain('4e9f7b2de42c');
  });
});

describe('AttributionTable without usage history', () => {
  test('says usage history is off instead of "no usage yet"', () => {
    const attribution = {
      ...forecast.pools[0].attribution!,
      method: 'none' as const,
      reason: 'usage_history_disabled',
      models: [],
      clients: [],
      agents: [],
    };
    const markup = renderToStaticMarkup(
      createElement(AttributionTable, { attribution, clientLabels: new Map() })
    );
    expect(markup).toContain('Attribution needs usage history');
    expect(markup).not.toContain('No token usage recorded');
  });

  test('keeps the plain empty message when attribution ran', () => {
    const attribution = {
      ...forecast.pools[0].attribution!,
      reason: null,
      models: [],
      clients: [],
      agents: [],
    };
    const markup = renderToStaticMarkup(
      createElement(AttributionTable, { attribution, clientLabels: new Map() })
    );
    expect(markup).toContain('No token usage recorded');
  });
});
