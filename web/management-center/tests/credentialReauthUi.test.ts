/**
 * Static markup for the "Sign in again" entry points on the auth-file card banner and the
 * quota card strip. The dialog interaction itself is outside this harness.
 */

import { describe, expect, mock, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import en from '@/i18n/locales/en.json';
import type { AuthFileItem } from '@/types';

// Bun has no CSS-module loader; the quota sections bind their classes at import time.
const classNames = { default: new Proxy({}, { get: (_target, key) => String(key) }) };
mock.module('@/features/authFiles/components/AuthFileQuota.module.scss', () => classNames);
mock.module('@/features/authFiles/components/AuthFileCard.module.scss', () => classNames);
mock.module('@/features/quota/components/QuotaBody.module.scss', () => classNames);
mock.module('@/features/quota/components/QuotaCard.module.scss', () => classNames);

const { AuthFileCard } = await import('@/features/authFiles/components/AuthFileCard');
const { QuotaCard } = await import('@/features/quota/components/QuotaCard');

const claudeRetrying: AuthFileItem = {
  name: 'claude-user@example.com.json',
  type: 'claude',
  provider: 'claude',
  email: 'user@example.com',
  authIndex: '7',
  statusMessage: 'invalid grant (retrying)',
  requiresSignIn: false,
  renewalState: 'retry_scheduled',
  refreshError: { code: 'invalid_grant', message: 'invalid grant' },
};
const codexRequired: AuthFileItem = {
  name: 'codex-user@example.com.json',
  type: 'codex',
  provider: 'codex',
  email: 'user@example.com',
  authIndex: '3',
  requiresSignIn: true,
  renewalState: 'sign_in_required',
  refreshError: { code: 'refresh_token_reused' },
};
const healthy: AuthFileItem = {
  name: 'claude-ok@example.com.json',
  type: 'claude',
  provider: 'claude',
  email: 'ok@example.com',
  authIndex: '8',
  requiresSignIn: false,
  renewalState: 'renewal_verified',
};

const renderAuthFileCard = (file: AuthFileItem, withReauth = true) =>
  renderToStaticMarkup(
    createElement(AuthFileCard, {
      file,
      compact: true,
      selected: false,
      resolvedTheme: 'light',
      disableControls: false,
      deleting: null,
      statusUpdating: {},
      manualRefreshing: {},
      cooldownResetting: {},
      quotaFilterType: null,
      statusBarCache: new Map(),
      onShowModels: () => {},
      onDownload: () => {},
      onManualRefresh: () => {},
      onCooldownReset: () => {},
      onOpenPrefixProxyEditor: () => {},
      onDelete: () => {},
      onToggleStatus: () => {},
      onToggleSelect: () => {},
      ...(withReauth ? { onReauth: () => {} } : {}),
    })
  );

const renderQuotaCard = (file: AuthFileItem) =>
  renderToStaticMarkup(
    createElement(QuotaCard, {
      entry: { file, type: file.type as 'claude' | 'codex' },
      resolvedTheme: 'light',
      canRefresh: true,
      resetting: false,
      onRefresh: () => {},
      onReset: () => {},
      onReauth: () => {},
    })
  );

const banner = (markup: string) =>
  markup.split('<div class="warning"')[1]?.split('<div class="health"')[0] ?? '';

describe('auth-file card sign-in-again entry', () => {
  test('the Claude invalid_grant retry banner carries a secondary button', () => {
    const markup = banner(renderAuthFileCard(claudeRetrying));
    expect(markup).toContain('invalid grant (retrying)');
    expect(markup).toContain(`>${en.auth_reauth.button}<`);
    expect(markup).toContain('data-reauth="suggested"');
    expect(markup).toContain('btn-secondary');
    expect(markup).toContain('aria-label="Sign in again to user@example.com"');
    expect(markup).toContain('aria-haspopup="dialog"');
  });

  test('a credential that requires a sign-in gets a primary button, even without a message', () => {
    const markup = banner(renderAuthFileCard(codexRequired));
    expect(markup).toContain(en.auth_reauth.required_hint);
    expect(markup).toContain('data-reauth="required"');
    expect(markup).toContain('btn-primary');
  });

  test('a healthy, disabled or hostless card shows no button', () => {
    expect(renderAuthFileCard(healthy)).not.toContain(en.auth_reauth.button);
    expect(renderAuthFileCard({ ...codexRequired, disabled: true })).not.toContain(
      en.auth_reauth.button
    );
    const withoutHost = renderAuthFileCard(claudeRetrying, false);
    expect(withoutHost).toContain('invalid grant (retrying)');
    expect(withoutHost).not.toContain(en.auth_reauth.button);
  });
});

describe('quota card sign-in-again strip', () => {
  test('shows the strip for suggested and required credentials', () => {
    const suggested = renderQuotaCard(claudeRetrying);
    expect(suggested).toContain('class="reauthStrip" data-level="suggested"');
    expect(suggested).toContain(en.auth_reauth.suggested_hint);
    expect(suggested).toContain('btn-secondary');

    const required = renderQuotaCard(codexRequired);
    expect(required).toContain('class="reauthStrip" data-level="required"');
    expect(required).toContain(en.auth_reauth.required_hint);
    expect(required).toContain('btn-primary');
  });

  test('a healthy credential has no strip', () => {
    const markup = renderQuotaCard(healthy);
    expect(markup).not.toContain('reauthStrip');
    expect(markup).not.toContain(en.auth_reauth.button);
  });
});
