/**
 * Spending a Codex manual reset credit. It stays an api-call write: the backend
 * poll only reads quota.
 */

import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { apiCallApi, authFilesApi, getApiCallErrorMessage } from '@/services/api';
import { guardConfigConnection } from '@/services/api/configValue';
import { isRecord } from '@/utils/helpers';
import { CODEX_RATE_LIMIT_RESET_CREDITS_CONSUME_URL, createStatusError } from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { buildCodexRequestHeader } from './requestHeader';

const createCodexRedeemRequestId = (): string => {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return globalThis.crypto.randomUUID();
  }

  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (char) => {
    const value = Math.floor(Math.random() * 16);
    const segment = char === 'x' ? value : (value & 0x3) | 0x8;
    return segment.toString(16);
  });
};

/** Redeems one credit and returns the auth index once upstream confirms the reset. */
async function consumeCodexResetCredit(file: AuthFileItem, t: TFunction): Promise<string> {
  const authIndex = normalizeAuthIndex(file.auth_index ?? file.authIndex);
  if (!authIndex) {
    throw new Error(t('codex_quota.missing_auth_index'));
  }

  const result = await apiCallApi.request({
    authIndex,
    method: 'POST',
    url: CODEX_RATE_LIMIT_RESET_CREDITS_CONSUME_URL,
    header: buildCodexRequestHeader(file),
    data: JSON.stringify({ redeem_request_id: createCodexRedeemRequestId() }),
  });

  if (result.statusCode < 200 || result.statusCode >= 300) {
    throw createStatusError(getApiCallErrorMessage(result), result.statusCode);
  }
  const code = isRecord(result.body) ? result.body.code : undefined;
  if (code !== 'reset' && code !== 'already_redeemed') {
    throw new Error(t('codex_quota.reset_not_confirmed'));
  }
  return authIndex;
}

/** Redeems a credit, then clears the proxy's local cooldown for the same credential. */
export async function redeemCodexResetCredit(file: AuthFileItem, t: TFunction): Promise<void> {
  const assertConnection = guardConfigConnection();
  const authIndex = await consumeCodexResetCredit(file, t);
  try {
    // Never clear a different connection's cooldown after awaiting redemption.
    assertConnection();
    const result = await authFilesApi.resetCooldown(authIndex);
    assertConnection();
    if (result.status !== 'ok' || result.auth_index !== authIndex) {
      throw new Error('Invalid cooldown reset response');
    }
  } catch {
    // Redemption already succeeded: direct the operator to the existing clear action.
    throw new Error(t('codex_quota.reset_cooldown_failed'));
  }
}
