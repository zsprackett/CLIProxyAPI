import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { QuotaHeader } from '@/features/quota/components/QuotaHeader';
import { classifyQuotaFiles, countQuotaCredentials } from '@/features/quota/logic';
import i18n from '@/i18n';
import type { AuthFileItem } from '@/types';

const snapshotFile = (name: string, windows: unknown[]): AuthFileItem =>
  ({
    name,
    provider: 'claude',
    quota_snapshot: { provider: 'claude', windows, extras: null },
  }) as unknown as AuthFileItem;

const WINDOW = { id: 'five_hour', used_percent: 10 };

const renderHeader = (files: AuthFileItem[]) => {
  const entries = classifyQuotaFiles(files);
  const counts = countQuotaCredentials(entries, () => undefined);
  return renderToStaticMarkup(
    createElement(QuotaHeader, {
      totalCount: entries.length,
      ...counts,
      refreshing: false,
      disableControls: false,
      onRefreshAll: () => {},
    })
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

describe('quota page header counts', () => {
  test('snapshot-only credentials count as loaded', () => {
    const markup = renderHeader([
      snapshotFile('a.json', [WINDOW]),
      snapshotFile('b.json', [WINDOW]),
      snapshotFile('c.json', [WINDOW]),
    ]);
    expect(markup).toContain('3 credentials');
    expect(markup).toContain('3 loaded');
  });

  test('a credential with an empty snapshot and no live reading is not loaded', () => {
    const markup = renderHeader([snapshotFile('a.json', [WINDOW]), snapshotFile('b.json', [])]);
    expect(markup).toContain('2 credentials');
    expect(markup).toContain('1 loaded');
  });
});
