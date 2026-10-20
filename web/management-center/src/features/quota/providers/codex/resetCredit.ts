/**
 * Spending a Codex manual reset credit. It stays an api-call write: the backend
 * poll only reads quota.
 */

import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { apiCallApi, getApiCallErrorMessage } from '@/services/api';
import {
  CODEX_RATE_LIMIT_RESET_CREDITS_CONSUME_URL,
  CODEX_REQUEST_HEADERS,
  createStatusError,
  resolveCodexChatgptAccountId,
} from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/authIndex';

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

export async function consumeCodexResetCredit(file: AuthFileItem, t: TFunction): Promise<void> {
  const authIndex = normalizeAuthIndex(file.auth_index ?? file.authIndex);
  if (!authIndex) {
    throw new Error(t('codex_quota.missing_auth_index'));
  }

  const header: Record<string, string> = { ...CODEX_REQUEST_HEADERS };
  const accountId = resolveCodexChatgptAccountId(file);
  if (accountId) {
    header['Chatgpt-Account-Id'] = accountId;
  }

  const result = await apiCallApi.request({
    authIndex,
    method: 'POST',
    url: CODEX_RATE_LIMIT_RESET_CREDITS_CONSUME_URL,
    header,
    data: JSON.stringify({ redeem_request_id: createCodexRedeemRequestId() }),
  });

  if (result.statusCode < 200 || result.statusCode >= 300) {
    throw createStatusError(getApiCallErrorMessage(result), result.statusCode);
  }
}
