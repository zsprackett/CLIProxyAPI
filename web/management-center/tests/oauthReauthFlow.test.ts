import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import { createInstance } from 'i18next';
import en from '@/i18n/locales/en.json';
import { OAuthFlowDialog } from '@/features/oauth/components/OAuthFlowDialog';
import { resolveFlowView, targetedCompletionWarning } from '@/features/oauth/flowView';
import type { ProviderFlowState } from '@/features/oauth/hooks/useOAuthFlows';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });

const renderDialog = (state: ProviderFlowState) =>
  renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(OAuthFlowDialog, {
        open: true,
        providerId: 'anthropic',
        heading: { title: 'Sign in again: user@example.com', caption: 'Claude', icon: '' },
        state,
        text: (suffix: string) => i18n.t(`auth_login.anthropic_${suffix}`),
        supportsCallback: true,
        mode: 'reauth',
        onStart: () => {},
        onCancel: () => {},
        onCallbackChange: () => {},
        onCallbackSubmit: () => {},
        onClose: () => {},
      })
    )
  );

describe('targeted OAuth completion', () => {
  test('an ok status with an adoption or verification error is not a clean success', () => {
    expect(targetedCompletionWarning({ status: 'ok', auth_id: 'a', saved: true })).toBeUndefined();
    expect(
      targetedCompletionWarning({ status: 'ok', saved: true, adoption_error: 'not adopted' })
    ).toBe('not adopted');
    expect(
      targetedCompletionWarning({ status: 'ok', adopted: true, verification_error: 'HTTP 401' })
    ).toBe('HTTP 401');
    expect(
      targetedCompletionWarning({
        status: 'ok',
        adoption_error: 'not adopted',
        verification_error: 'HTTP 401',
      })
    ).toBe('not adopted; HTTP 401');
  });

  test('the dialog reports a warning success with the backend message', () => {
    const state: ProviderFlowState = { status: 'success', warning: 'HTTP 401' };
    expect(resolveFlowView(state)).toBe('success');
    const markup = renderDialog(state);
    expect(markup).toContain(en.auth_reauth.success_warning_title);
    expect(markup).toContain('HTTP 401');
    expect(markup).toContain('role="alert"');
    expect(markup).not.toContain(en.auth_reauth.success_title);
  });

  test('a clean targeted success names the update in place and has no add-account action', () => {
    const markup = renderDialog({ status: 'success' });
    expect(markup).toContain(en.auth_reauth.success_title);
    expect(markup).toContain(en.auth_reauth.success_hint);
    expect(markup).not.toContain(en.auth_login.login_another_account);
    expect(markup).not.toContain(en.auth_login.view_auth_files);
    expect(markup).not.toContain('role="alert"');
  });

  test('a mismatched account error is shown as the backend reports it', () => {
    const markup = renderDialog({
      status: 'error',
      error: 'Authenticated account does not match target credential',
    });
    expect(markup).toContain('Authenticated account does not match target credential');
  });
});
