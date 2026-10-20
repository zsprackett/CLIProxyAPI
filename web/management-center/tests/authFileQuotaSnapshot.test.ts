/**
 * The quota section of an auth-file card shows the credential's snapshot: the
 * click's poll result until the listing carries a newer one.
 */

import { afterEach, describe, expect, mock, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import { useQuotaStore } from '@/stores/useQuotaStore';
import type { AuthFileItem } from '@/types';
import { codexSnapshot } from './fixtures/quotaSnapshotCodex';

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

const claudeReading = (usedPercent: number, updatedAtMs: number): QuotaSnapshot => ({
  provider: 'claude',
  windows: [
    {
      id: 'seven_day',
      class: 'claude:7d',
      used_percent: usedPercent,
      limit_reached: false,
      reset_at: iso(NOW + 2 * DAY),
      period_seconds: 604800,
      section: 'claude.usage',
      source: 'poll',
      observed_at: iso(updatedAtMs),
    },
  ],
  extras: {
    kind: 'claude',
    plan: { status: 'ok', value: 'plan_max', value_at: null, checked_at: null },
    extra_usage: { status: 'absent', value: null, value_at: null, checked_at: null },
  },
  last_poll: null,
  updated_at: iso(updatedAtMs),
});

const claudeFile = (listed: QuotaSnapshot): AuthFileItem => ({
  name: 'claude-a.json',
  type: 'claude',
  auth_index: 'idx-a',
  quota_snapshot: listed,
});

// renderToStaticMarkup reads zustand's server snapshot, which is the store's initial state.
const serverState = useQuotaStore.getInitialState();

const renderSection = (file: AuthFileItem) =>
  renderToStaticMarkup(
    createElement(AuthFileQuotaSection, {
      file,
      quotaType: file.type as 'claude' | 'codex',
      disableControls: false,
    })
  );

afterEach(() => {
  serverState.quotaPolls = {};
  serverState.quotaSnapshots = {};
});

describe('auth-file quota section', () => {
  test('a failed click still shows the snapshot windows and the error', () => {
    serverState.quotaPolls = { 'claude-a.json': { status: 'error', error: 'upstream timeout' } };
    const markup = renderSection(claudeFile(claudeReading(50, NOW - HOUR)));
    expect(markup).toContain('7-day limit');
    expect(markup).toContain('50%');
    expect(markup).toContain('Max');
    expect(markup).toContain('upstream timeout');
  });

  test('a poll result newer than the listing snapshot replaces it', () => {
    serverState.quotaSnapshots = {
      'idx-a': { name: 'claude-a.json', snapshot: claudeReading(10, NOW - 5 * 60 * 1000) },
    };
    const markup = renderSection(claudeFile(claudeReading(50, NOW - HOUR)));
    expect(markup).toContain('90%');
    expect(markup).not.toContain('50%');
  });

  test('a listing snapshot newer than the poll result replaces it', () => {
    serverState.quotaSnapshots = {
      'idx-a': { name: 'claude-a.json', snapshot: claudeReading(10, NOW - 2 * HOUR) },
    };
    const markup = renderSection(claudeFile(claudeReading(50, NOW - HOUR)));
    expect(markup).toContain('50%');
    expect(markup).not.toContain('90%');
  });

  test('a Codex snapshot with manual resets offers the reset button', () => {
    const file: AuthFileItem = {
      name: 'codex-a.json',
      type: 'codex',
      auth_index: 'idx-c',
      quota_snapshot: codexSnapshot(),
    };
    expect(renderSection(file)).toContain('aria-label="Reset quota"');

    const noCredits = codexSnapshot();
    if (noCredits.extras?.kind === 'codex') noCredits.extras.reset_credits.value = null;
    expect(renderSection({ ...file, quota_snapshot: noCredits })).not.toContain(
      'aria-label="Reset quota"'
    );
  });
});
