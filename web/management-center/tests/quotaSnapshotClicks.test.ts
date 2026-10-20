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
const { useAuthFilesData } = await import('@/features/authFiles/hooks/useAuthFilesData');

const claudeGolden: QuotaSnapshot = JSON.parse(
  readFileSync(
    new URL('../../../sdk/cliproxy/auth/testdata/quota_snapshot_claude.json', import.meta.url),
    'utf8'
  )
);

const MINUTE = 60 * 1000;
const iso = (ms: number) => new Date(ms).toISOString();

const claudeFile = (quotaSnapshot?: QuotaSnapshot): AuthFileItem => ({
  name: 'claude-a.json',
  type: 'claude',
  auth_index: 'idx-a',
  ...(quotaSnapshot ? { quota_snapshot: quotaSnapshot } : {}),
});

/** A listing snapshot with the 7-day window at 90% used, so 10% shows. */
const listedReading = (updatedAt: string): QuotaSnapshot => ({
  ...claudeGolden,
  windows: claudeGolden.windows.map((window) =>
    window.id === 'seven_day' ? { ...window, used_percent: 90, observed_at: updatedAt } : window
  ),
  updated_at: updatedAt,
});

type Answer = { status: number; data: unknown };

const http = (apiClient as unknown as { instance: AxiosInstance }).instance;
const originalAdapter = http.defaults.adapter;
let requests: Array<[string | undefined, unknown]> = [];
let answer: () => Promise<Answer> = async () => ({ status: 200, data: {} });

/** The backend: records each request and answers with `answer`, as the server would. */
const backend: AxiosAdapter = async (config) => {
  requests.push([config.url, config.data === undefined ? null : JSON.parse(String(config.data))]);
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

/** The auth-file page loading its listing, as its refresh and its interval do. */
const loadAuthFilesListing = (): Promise<void> => {
  let load: ReturnType<typeof useAuthFilesData>['loadFiles'] | undefined;
  function Harness() {
    load = useAuthFilesData().loadFiles;
    return null;
  }
  renderToStaticMarkup(createElement(Harness));
  return load!({ background: true });
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

const toasts = () =>
  useNotificationStore.getState().notifications.map((note) => [note.type, note.message]);

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

describe('a failed click on the quota page', () => {
  const failClick = async () => {
    answerWith(500, { error: 'upstream timeout' });
    await clickLoadQuota(claudeFile());
  };

  test('gives way to a listing snapshot newer than the click', async () => {
    await failClick();
    const listed = claudeFile(listedReading(iso(Date.now() + MINUTE)));
    expect(renderQuotaPageCard(listed)).toContain('Failed to load quota: upstream timeout');

    useQuotaStore.getState().reconcileQuotaSnapshots([listed]);

    const card = renderQuotaPageCard(listed);
    expect(card).not.toContain('role="alert"');
    expect(card).toContain('<span class="windowPercent">10%</span>');
  });

  test('stays over a listing snapshot older than the click', async () => {
    await failClick();
    const listed = claudeFile(listedReading(iso(Date.now() - MINUTE)));

    useQuotaStore.getState().reconcileQuotaSnapshots([listed]);

    const card = renderQuotaPageCard(listed);
    expect(card).toContain('role="alert"');
    expect(card).toContain('Failed to load quota: upstream timeout');
  });

  test('stays while the listing has no snapshot', async () => {
    await failClick();

    useQuotaStore.getState().reconcileQuotaSnapshots([claudeFile()]);

    expect(renderQuotaPageCard(claudeFile())).toContain('Failed to load quota: upstream timeout');
  });
});

describe('the answer to a click on the quota page', () => {
  const usageError = 'fetch /api/oauth/usage failed with status 429';

  /** The backend's 200 when the usage request failed: the old windows stay, marked by the error. */
  const failedUsagePoll = (): QuotaSnapshot => {
    const at = iso(Date.now());
    return {
      ...claudeGolden,
      extras: {
        kind: 'claude',
        plan: { status: 'ok', value: 'plan_max', value_at: at, checked_at: at },
        extra_usage: {
          status: 'failed',
          value: null,
          value_at: null,
          checked_at: at,
          error: usageError,
        },
      },
      last_poll: { at, trigger: 'on_demand', error: usageError },
      updated_at: at,
    };
  };

  test('a clean poll shows the windows and a success toast', async () => {
    await clickLoadQuota(claudeFile());

    const card = renderQuotaPageCard(claudeFile());
    expect(card).toContain('7-day limit');
    expect(card).not.toContain('Stale since');
    expect(card).not.toContain('Last quota poll failed');
    expect(toasts()).toEqual([['success', 'Quota refreshed for "claude-a.json"']]);
  });

  test('a 200 with a failed usage request shows stale windows, the failure, and no success toast', async () => {
    answerWith(200, { quota_snapshot: failedUsagePoll() });

    await clickLoadQuota(claudeFile());

    const card = renderQuotaPageCard(claudeFile());
    expect(card).toContain('7-day limit');
    expect(card).toContain('Stale since');
    expect(card).toContain(`Last quota poll failed: ${usageError}`);
    const section = renderAuthFileSection(claudeFile());
    expect(section).toContain('Stale since');
    expect(section).toContain(`Last quota poll failed: ${usageError}`);
    expect(toasts()).toEqual([
      ['error', `Failed to refresh quota for "claude-a.json": ${usageError}`],
    ]);
  });

  test('a 404 asks the user to update CPA', async () => {
    answerWith(404, { error: 'auth not found' });

    await clickLoadQuota(claudeFile());

    expect(renderQuotaPageCard(claudeFile())).toContain(
      'Failed to load quota: Please update the CPA version or check for updates'
    );
  });

  test('a 422 shows the backend reason', async () => {
    answerWith(422, { error: 'credential has no quota to poll' });

    await clickLoadQuota(claudeFile());

    expect(renderQuotaPageCard(claudeFile())).toContain(
      'Failed to load quota: credential has no quota to poll'
    );
  });

  test('a second click while the first is in flight sends no second request', async () => {
    let arrived: () => void = () => {};
    const requested = new Promise<void>((resolve) => (arrived = resolve));
    let release: () => void = () => {};
    answer = () =>
      new Promise<Answer>((resolve) => {
        release = () => resolve({ status: 200, data: { quota_snapshot: claudeGolden } });
        arrived();
      });

    const first = clickLoadQuota(claudeFile());
    const second = clickLoadQuota(claudeFile());
    await requested;
    release();
    await Promise.all([first, second]);

    expect(requests).toEqual([['/credentials/quota/poll', { auth_index: 'idx-a' }]]);
    expect(renderQuotaPageCard(claudeFile())).toContain('<span class="codexPlanValue">Max</span>');
  });
});

describe('a failed click on an auth-file card', () => {
  const failClick = async () => {
    answerWith(500, { error: 'upstream timeout' });
    await clickLoadQuota(claudeFile());
  };

  test('gives way to the next listing, which carries a newer snapshot', async () => {
    await failClick();
    expect(renderAuthFileSection(claudeFile())).toContain('Failed to load quota: upstream timeout');

    answerWith(200, { files: [claudeFile(listedReading(iso(Date.now() + MINUTE)))] });
    await loadAuthFilesListing();

    const section = renderAuthFileSection(claudeFile(listedReading(iso(Date.now() + MINUTE))));
    expect(section).not.toContain('quotaError');
    expect(section).not.toContain('Failed to load quota');
    expect(section).toContain('<span class="windowPercent">10%</span>');
  });

  test('stays over a listing whose snapshot is older than the click', async () => {
    await failClick();

    answerWith(200, { files: [claudeFile(listedReading(iso(Date.now() - MINUTE)))] });
    await loadAuthFilesListing();

    expect(renderAuthFileSection(claudeFile(listedReading(iso(Date.now() - MINUTE))))).toContain(
      'Failed to load quota: upstream timeout'
    );
  });
});
