import { describe, expect, test } from 'bun:test';
import { reauthNeed, reauthPanelProviderId } from '@/features/authFiles/reauth';
import { normalizeAuthFilesResponse } from '@/services/api/authFiles';
import type { AuthFileItem } from '@/types';

// Shape of the v8 credential that motivated the button: Claude retries invalid_grant
// instead of marking the credential as needing a sign-in.
const claudeRetrying: AuthFileItem = {
  name: 'claude-user@example.com.json',
  type: 'claude',
  provider: 'claude',
  email: 'user@example.com',
  authIndex: '7',
  statusMessage: 'invalid grant (retrying)',
  requiresSignIn: false,
  renewalState: 'retry_scheduled',
  refreshError: { code: 'invalid_grant', message: 'invalid grant', httpStatus: 400 },
};

describe('reauthNeed', () => {
  test('suggests a sign-in for a Claude credential that retries invalid_grant', () => {
    expect(reauthNeed(claudeRetrying)).toEqual({
      level: 'suggested',
      provider: 'claude',
      authIndex: '7',
      code: 'invalid_grant',
    });
  });

  test('requires a sign-in when the backend says so', () => {
    expect(
      reauthNeed({ name: 'codex.json', type: 'codex', authIndex: '3', requiresSignIn: true })
    ).toEqual({ level: 'required', provider: 'codex', authIndex: '3' });
    expect(
      reauthNeed({
        name: 'codex.json',
        type: 'codex',
        authIndex: '3',
        renewalState: 'expired_sign_in_required',
      })
    ).toEqual({ level: 'required', provider: 'codex', authIndex: '3' });
  });

  test.each([
    'refresh_token_reused',
    'refresh_token_expired',
    'refresh_token_invalidated',
    'token_revoked',
    'invalid_client',
    'unauthorized',
  ])('requires a sign-in for the terminal refresh code %s', (code) => {
    expect(
      reauthNeed({
        name: 'anthropic.json',
        type: 'anthropic',
        authIndex: '9',
        refreshError: { code },
      })
    ).toEqual({ level: 'required', provider: 'claude', authIndex: '9' });
  });

  test('returns null for disabled, unindexed, unsupported, runtime-only and healthy files', () => {
    expect(reauthNeed({ ...claudeRetrying, disabled: true })).toBeNull();
    expect(reauthNeed({ ...claudeRetrying, authIndex: null })).toBeNull();
    expect(reauthNeed({ ...claudeRetrying, authIndex: undefined })).toBeNull();
    expect(reauthNeed({ ...claudeRetrying, type: 'gemini', provider: 'gemini' })).toBeNull();
    expect(reauthNeed({ ...claudeRetrying, type: 'vertex', provider: 'vertex' })).toBeNull();
    expect(reauthNeed({ ...claudeRetrying, runtimeOnly: true })).toBeNull();
    expect(
      reauthNeed({
        name: 'claude.json',
        type: 'claude',
        authIndex: '1',
        requiresSignIn: false,
        renewalState: 'renewal_verified',
      })
    ).toBeNull();
    // A transient failure that is retried is not a sign-in signal.
    expect(
      reauthNeed({
        ...claudeRetrying,
        refreshError: { code: 'refresh_failed' },
      })
    ).toBeNull();
  });

  test('never reads the status message text', () => {
    expect(
      reauthNeed({
        name: 'claude.json',
        type: 'claude',
        authIndex: '1',
        statusMessage: 'sign in required: invalid_grant refresh_token_reused',
      })
    ).toBeNull();
  });

  test('maps the API provider to the OAuth page card id', () => {
    expect(reauthPanelProviderId('claude')).toBe('anthropic');
    expect(reauthPanelProviderId('codex')).toBe('codex');
    expect(reauthPanelProviderId('kimi-ai')).toBe('kimi-ai');
  });
});

describe('credential renewal fields', () => {
  test('normalizes the typed v8 renewal fields', () => {
    const { files } = normalizeAuthFilesResponse({
      files: [
        {
          name: 'claude.json',
          type: 'claude',
          auth_index: 7,
          requires_sign_in: false,
          renewal_state: 'retry_scheduled',
          refresh_error: { code: 'invalid_grant', message: 'invalid grant', http_status: 400 },
        },
      ],
    });
    expect(files[0].requiresSignIn).toBe(false);
    expect(files[0].renewalState).toBe('retry_scheduled');
    expect(files[0].refreshError).toEqual({
      code: 'invalid_grant',
      message: 'invalid grant',
      httpStatus: 400,
    });
    expect(reauthNeed(files[0])?.level).toBe('suggested');
  });

  test('drops malformed renewal fields', () => {
    const { files } = normalizeAuthFilesResponse({
      files: [{ name: 'a.json', type: 'claude', refresh_error: 'boom', renewal_state: 5 }],
    });
    expect(files[0].refreshError).toBeUndefined();
    expect(files[0].renewalState).toBeUndefined();
    expect(files[0].requiresSignIn).toBeUndefined();
  });
});
