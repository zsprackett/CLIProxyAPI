import type { OAuthStatusResponse } from '@/services/api/oauth';
import type { ProviderFlowState } from './hooks/useOAuthFlows';

export type FlowView = 'idle' | 'flow' | 'success' | 'failed';

/**
 * 对话框视图：有授权链接（或正在生成）→ 步骤视图；链接失效后的失败 → 失败视图。
 * 轮询网络失败时链接仍在，留在步骤视图里：手动回调仍可提交并恢复轮询。
 */
export const resolveFlowView = (state: ProviderFlowState): FlowView => {
  if (state.status === 'success') return 'success';
  if (state.url || state.status === 'waiting') return 'flow';
  if (state.status === 'error') return 'failed';
  return 'idle';
};

/** 授权链接拆成 host + 余下部分：host 高亮，方便用户核对要去的域名。 */
export const splitAuthUrl = (url: string): { host: string; rest: string } => {
  try {
    const { host } = new URL(url);
    if (!host) return { host: '', rest: url };
    const hostEnd = url.indexOf(host) + host.length;
    return { host, rest: url.slice(hostEnd) };
  } catch {
    return { host: '', rest: url };
  }
};

/**
 * A targeted login can save the credential but still fail adoption or verification.
 * The status is `ok` then, so the message is the only signal that it is not clean.
 */
export const targetedCompletionWarning = (res: OAuthStatusResponse): string | undefined => {
  const messages = [res.adoption_error, res.verification_error]
    .map((message) => message?.trim())
    .filter(Boolean);
  return messages.length > 0 ? messages.join('; ') : undefined;
};
