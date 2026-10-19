/**
 * Subscription plan shown as a badge in the auth file card header.
 * React-free — consumed directly by tests/authFilePlan.test.ts.
 *
 * The plan comes from the auth-file list itself, so it is visible without a
 * live quota request: the backend records Claude's plan (`plan_type`) from the
 * OAuth profile at login and token refresh, and Codex carries it in the
 * `id_token` claims.
 */

import type { AuthFileItem } from '@/types';
import { normalizePlanType, resolveAuthProvider, resolveCodexPlanType } from '@/utils/quota';

export interface AuthFilePlan {
  /** Translation key for the plan name. */
  labelKey: string;
  /** Shown when the key has no translation (an unrecognized Codex plan). */
  fallback: string;
}

const CLAUDE_PLANS = new Set(['free', 'pro', 'max', 'team']);

export function resolveAuthFilePlan(file: AuthFileItem): AuthFilePlan | null {
  const provider = resolveAuthProvider(file);
  if (provider === 'claude') {
    const plan = normalizePlanType(file['plan_type']);
    if (!plan || !CLAUDE_PLANS.has(plan)) return null;
    return { labelKey: `claude_quota.plan_${plan}`, fallback: plan };
  }
  if (provider === 'codex') {
    const plan = resolveCodexPlanType(file);
    if (!plan) return null;
    return { labelKey: `codex_quota.plan_${plan}`, fallback: plan };
  }
  return null;
}
