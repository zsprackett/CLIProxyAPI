/**
 * The quota section of an auth-file card shows the newer whole reading: the
 * live state in the store or the backend snapshot.
 */

import { afterAll, afterEach, beforeAll, describe, expect, mock, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import { useQuotaStore } from '@/stores/useQuotaStore';
import type { AuthFileItem, ClaudeQuotaState } from '@/types';

// Bun has no CSS-module loader; the section binds its classes at import time.
mock.module('@/features/authFiles/components/AuthFileQuota.module.scss', () => ({
  default: new Proxy({}, { get: (_target, key) => String(key) }),
}));

const { AuthFileQuotaSection } =
  await import('@/features/authFiles/components/AuthFileQuotaSection');

const NOW = Date.now();
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const iso = (ms: number) => new Date(ms).toISOString();

const snapshot: QuotaSnapshot = {
  provider: 'claude',
  windows: [
    {
      id: 'seven_day',
      class: 'claude:7d',
      used_percent: 50,
      limit_reached: false,
      reset_at: iso(NOW + 2 * DAY),
      period_seconds: 604800,
      section: 'claude.usage',
      source: 'poll',
      observed_at: iso(NOW - 3 * HOUR),
    },
  ],
  extras: null,
  last_poll: null,
  updated_at: iso(NOW - HOUR),
};

const file: AuthFileItem = { name: 'claude-a.json', type: 'claude', quota_snapshot: snapshot };

const live = (fetchedAtMs: number) =>
  ({
    status: 'success',
    fetchedAtMs,
    windows: [
      {
        id: 'seven-day',
        label: 'Live 7d',
        labelKey: undefined,
        usedPercent: 10,
        resetLabel: '-',
        resetAtMs: NOW + 4 * DAY,
      },
    ],
  }) as unknown as ClaudeQuotaState;

// renderToStaticMarkup reads zustand's server snapshot, which is the store's initial state.
const serverState = useQuotaStore.getInitialState();

const renderSection = (quota: ClaudeQuotaState) => {
  serverState.claudeQuota = { [file.name]: quota };
  return renderToStaticMarkup(
    createElement(AuthFileQuotaSection, { file, quotaType: 'claude', disableControls: false })
  );
};

let originalLanguage = 'en';
beforeAll(async () => {
  originalLanguage = i18n.language;
  await i18n.changeLanguage('en');
});
afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});
afterEach(() => {
  serverState.claudeQuota = {};
});

describe('auth-file quota section', () => {
  test('a live error still shows the snapshot windows and the error', () => {
    const markup = renderSection({
      status: 'error',
      error: 'upstream timeout',
      windows: [],
    } as unknown as ClaudeQuotaState);
    expect(markup).toContain('7-day limit');
    expect(markup).toContain('50%');
    expect(markup).toContain('upstream timeout');
  });

  test('a snapshot updated after the live fetch replaces the live body', () => {
    const markup = renderSection(live(NOW - 2 * HOUR));
    expect(markup).toContain('7-day limit');
    expect(markup).toContain('50%');
    expect(markup).not.toContain('Live 7d');
  });

  test('a live reading fetched after the snapshot update replaces the snapshot', () => {
    const markup = renderSection(live(NOW - 30 * 60 * 1000));
    expect(markup).toContain('Live 7d');
    expect(markup).toContain('90%');
    expect(markup).not.toContain('7-day limit');
  });
});
