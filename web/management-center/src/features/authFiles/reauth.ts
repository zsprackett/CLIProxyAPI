import type { AuthFileItem } from '@/types';
import { isRuntimeOnlyAuthFile, normalizeProviderKey } from './constants';

/** A credential that a targeted OAuth login can repair, keyed by the auth-url provider. */
export type ReauthNeed =
  | { level: 'required'; provider: string; authIndex: string }
  | { level: 'suggested'; provider: string; authIndex: string; code: string }
  | null;

/**
 * Built-in providers whose auth-url login accepts an `auth_index` target. Plugin OAuth
 * providers are left out: the list does not tell the panel which plugin entry to start.
 */
const TARGETABLE_PROVIDERS = new Set([
  'claude',
  'codex',
  'antigravity',
  'xai',
  'devin',
  'kimi',
  'kimi-ai',
  'meta',
]);

const SIGN_IN_RENEWAL_STATES = new Set(['sign_in_required', 'expired_sign_in_required']);

const TERMINAL_REFRESH_CODES = new Set([
  'refresh_token_reused',
  'refresh_token_expired',
  'refresh_token_invalidated',
  'token_revoked',
  'invalid_client',
  'unauthorized',
]);

/** auth-url provider key; the panel OAuth page still names Claude `anthropic`. */
const toApiProvider = (key: string) => (key === 'anthropic' ? 'claude' : key);

export function reauthNeed(file: AuthFileItem): ReauthNeed {
  if (file.disabled === true || isRuntimeOnlyAuthFile(file)) return null;
  const authIndex = typeof file.authIndex === 'string' ? file.authIndex.trim() : '';
  if (!authIndex) return null;
  const provider = toApiProvider(normalizeProviderKey(String(file.provider ?? file.type ?? '')));
  if (!TARGETABLE_PROVIDERS.has(provider)) return null;

  const code = file.refreshError?.code ?? '';
  if (
    file.requiresSignIn === true ||
    SIGN_IN_RENEWAL_STATES.has(String(file.renewalState ?? '')) ||
    TERMINAL_REFRESH_CODES.has(code)
  ) {
    return { level: 'required', provider, authIndex };
  }
  // Claude retries invalid_grant instead of asking for a sign-in, but a retry rarely helps.
  if (
    file.renewalState === 'retry_scheduled' &&
    (code === 'invalid_grant' || code.startsWith('refresh_token_'))
  ) {
    return { level: 'suggested', provider, authIndex, code };
  }
  return null;
}

/** The OAuth page card id for an auth-url provider. */
export const reauthPanelProviderId = (provider: string) =>
  provider === 'claude' ? 'anthropic' : provider;
