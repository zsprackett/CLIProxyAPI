import type { AuthFileItem } from '@/types';
import { CODEX_REQUEST_HEADERS, resolveCodexChatgptAccountId } from '@/utils/quota';

export const buildCodexRequestHeader = (file: AuthFileItem): Record<string, string> => {
  const accountId = resolveCodexChatgptAccountId(file);
  const requestHeader: Record<string, string> = {
    ...CODEX_REQUEST_HEADERS,
  };
  if (accountId) {
    requestHeader['Chatgpt-Account-Id'] = accountId;
  }
  return requestHeader;
};
