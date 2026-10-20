/**
 * What a user sees on a Claude card after a "Load quota" click, on the quota
 * page and in the auth-file section, with the backend answering the poll.
 */

import { afterAll, afterEach, beforeEach, describe, expect, mock, test } from 'bun:test';
import { AxiosError, type AxiosAdapter, type AxiosInstance, type AxiosResponse } from 'axios';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { invalidateAuthFileDerivedCaches } from '@/features/authFiles/cacheInvalidation';
import { QUOTA_ADAPTERS } from '@/features/quota/providers';
import type { QuotaSnapshot } from '@/features/quota/snapshot/types';
import { applyPolledSnapshot } from '@/features/quota/snapshot/windows';
import { apiClient } from '@/services/api/client';
import { useNotificationStore } from '@/stores/useNotificationStore';
import { useQuotaStore } from '@/stores/useQuotaStore';
import type { AuthFileItem } from '@/types';
import { getQuotaCacheKey } from '@/utils/quota/identity';

// Bun has no CSS-module loader; the card and the section bind their classes at import time.
const classNames = { default: new Proxy({}, { get: (_target, key) => String(key) }) };
mock.module('@/features/quota/components/QuotaBody.module.scss', () => classNames);
mock.module('@/features/quota/components/QuotaCard.module.scss', () => classNames);
mock.module('@/features/authFiles/components/AuthFileQuota.module.scss', () => classNames);

const { QuotaCard } = await import('@/features/quota/components/QuotaCard');
const { AuthFileQuotaSection } =
  await import('@/features/authFiles/components/AuthFileQuotaSection');
const { useQuotaActions } = await import('@/features/quota/hooks/useQuotaActions');

const claudeGolden: QuotaSnapshot = JSON.parse(
  readFileSync(
    new URL('../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json', import.meta.url),
    'utf8'
  )
);

const claudeFile = (quotaSnapshot?: QuotaSnapshot): AuthFileItem => ({
  name: 'claude-a.json',
  type: 'claude',
  auth_index: 'idx-a',
  ...(quotaSnapshot ? { quota_snapshot: quotaSnapshot } : {}),
});

type Answer = { status: number; data: unknown };

const http = (apiClient as unknown as { instance: AxiosInstance }).instance;
const originalAdapter = http.defaults.adapter;
let requests: Array<[string | undefined, unknown]> = [];
let answer: () => Promise<Answer> = async () => ({ status: 200, data: {} });

/** The backend: records each request and answers with `answer`, as the server would. */
const backend: AxiosAdapter = async (config) => {
  requests.push([config.url, JSON.parse(String(config.data))]);
  const { status, data } = await answer();
  const response = { status, statusText: '', data, headers: {}, config } as AxiosResponse;
  if (status >= 400) {
    throw new AxiosError(
      `Request failed with status code ${status}`,
      'ERR_BAD_REQUEST',
      config,
      null,
      response
    );
  }
  return response;
};

const answerWith = (status: number, data: unknown) => {
  answer = async () => ({ status, data });
};

/** Clicks "Load quota" on the quota-page card and waits for the click to settle. */
const clickLoadQuota = (file: AuthFileItem): Promise<void> => {
  let actions: ReturnType<typeof useQuotaActions> | undefined;
  function Harness() {
    actions = useQuotaActions(false);
    return null;
  }
  renderToStaticMarkup(createElement(Harness));
  return actions!.refreshQuota(file, QUOTA_ADAPTERS.claude);
};

/** The quota page's card: the listing entry with the click's poll result applied. */
const renderQuotaPageCard = (file: AuthFileItem) => {
  const state = useQuotaStore.getState();
  return renderToStaticMarkup(
    createElement(QuotaCard, {
      entry: { file: applyPolledSnapshot(file, state.quotaSnapshots), type: 'claude' },
      quota: state.quotaPolls[getQuotaCacheKey(file)],
      resolvedTheme: 'light',
      canRefresh: true,
      resetting: false,
      onRefresh: () => {},
      onReset: () => {},
    })
  );
};

// renderToStaticMarkup reads zustand's server snapshot, which is the store's initial state.
const serverState = useQuotaStore.getInitialState();

const renderAuthFileSection = (file: AuthFileItem) => {
  const state = useQuotaStore.getState();
  serverState.quotaSnapshots = state.quotaSnapshots;
  serverState.quotaPolls = state.quotaPolls;
  return renderToStaticMarkup(
    createElement(AuthFileQuotaSection, { file, quotaType: 'claude', disableControls: false })
  );
};

beforeEach(async () => {
  await i18n.changeLanguage('en');
  http.defaults.adapter = backend;
  requests = [];
  answerWith(200, { quota_snapshot: claudeGolden });
  useQuotaStore.getState().clearQuotaCache();
  useNotificationStore.setState({ notifications: [] });
});

afterEach(() => {
  serverState.quotaSnapshots = {};
  serverState.quotaPolls = {};
});

afterAll(() => {
  http.defaults.adapter = originalAdapter;
});

describe('a credential re-added at the same path', () => {
  test('shows no poll result of the account it replaced', async () => {
    await clickLoadQuota(claudeFile());
    expect(renderQuotaPageCard(claudeFile())).toContain('<span class="codexPlanValue">Max</span>');
    expect(renderAuthFileSection(claudeFile())).toContain('7-day limit');

    invalidateAuthFileDerivedCaches(() => {}, ['claude-a.json']);

    const card = renderQuotaPageCard(claudeFile());
    expect(card).toContain('idleBody');
    expect(card).not.toContain('Max');
    const section = renderAuthFileSection(claudeFile());
    expect(section).toContain('Click here to refresh quota');
    expect(section).not.toContain('7-day limit');
    expect(section).not.toContain('Max');
  });

  test('a different credential keeps its poll result', async () => {
    await clickLoadQuota(claudeFile());

    invalidateAuthFileDerivedCaches(() => {}, ['claude-other.json']);

    expect(renderQuotaPageCard(claudeFile())).toContain('<span class="codexPlanValue">Max</span>');
  });
});
