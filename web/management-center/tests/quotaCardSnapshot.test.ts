/**
 * A Claude or Codex card on the quota page shows the credential's snapshot
 * without a click, and offers "Load quota" only when there is none.
 */

import { describe, expect, mock, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import type { AuthFileItem } from '@/types';
import { codexSnapshot } from './fixtures/quotaSnapshotCodex';

// Bun has no CSS-module loader; the card binds its classes at import time.
const classNames = { default: new Proxy({}, { get: (_target, key) => String(key) }) };
mock.module('@/features/quota/components/QuotaBody.module.scss', () => classNames);
mock.module('@/features/quota/components/QuotaCard.module.scss', () => classNames);

const { QuotaCard } = await import('@/features/quota/components/QuotaCard');

const claudeGolden: QuotaSnapshot = JSON.parse(
  readFileSync(
    new URL('../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json', import.meta.url),
    'utf8'
  )
);

const renderCard = (file: AuthFileItem, quota?: { status: 'loading' | 'error'; error?: string }) =>
  renderToStaticMarkup(
    createElement(QuotaCard, {
      entry: { file, type: file.type as 'claude' | 'codex' },
      quota,
      resolvedTheme: 'light',
      canRefresh: true,
      resetting: false,
      onRefresh: () => {},
      onReset: () => {},
    })
  );

describe('quota card for a snapshot provider', () => {
  test('a Claude card with a snapshot shows its plan and windows without a click', () => {
    const markup = renderCard({
      name: 'claude-a.json',
      type: 'claude',
      auth_index: 'idx-a',
      quota_snapshot: claudeGolden,
    });
    expect(markup).toContain('<span class="codexPlanValue">Max</span>');
    expect(markup).toContain('7-day limit');
    expect(markup).toContain('Refresh');
    expect(markup).not.toContain('idleBody');
  });

  test('a card without a snapshot offers Load quota', () => {
    const markup = renderCard({ name: 'claude-b.json', type: 'claude', auth_index: 'idx-b' });
    expect(markup).toContain('idleBody');
    expect(markup).not.toContain('7-day limit');
  });

  test('a failed click shows the error', () => {
    const markup = renderCard(
      { name: 'claude-a.json', type: 'claude', auth_index: 'idx-a', quota_snapshot: claudeGolden },
      { status: 'error', error: 'HTTP 409' }
    );
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('HTTP 409');
  });

  test('a Codex card with an available manual reset offers the reset', () => {
    const markup = renderCard({
      name: 'codex-a.json',
      type: 'codex',
      auth_index: 'idx-c',
      quota_snapshot: codexSnapshot(),
    });
    expect(markup).toContain('Reset quota');
    expect(markup).toContain('Pro 20x');
  });
});
