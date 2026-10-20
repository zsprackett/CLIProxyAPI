/**
 * A Codex snapshot in the shape of the P2 contract: the classed windows, the
 * display-only code review and additional-limit windows, and every extras section.
 */

import type { QuotaSnapshot } from '@/features/quota/snapshot/types';

export const CODEX_NOW = Date.parse('2026-10-19T18:00:00Z');
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const iso = (ms: number) => new Date(ms).toISOString();

const ok = <T>(value: T) => ({
  status: 'ok' as const,
  value,
  value_at: iso(CODEX_NOW - HOUR),
  checked_at: iso(CODEX_NOW - HOUR),
});

const usage = (
  id: string,
  klass: string,
  usedPercent: number,
  resetInMs: number,
  periodSeconds: number,
  label?: string
) => ({
  id,
  class: klass,
  ...(label ? { label } : {}),
  used_percent: usedPercent,
  limit_reached: false,
  reset_at: iso(CODEX_NOW + resetInMs),
  period_seconds: periodSeconds,
  section: 'codex.usage',
  source: 'poll' as const,
  observed_at: iso(CODEX_NOW - HOUR),
});

export const CODEX_EXPIRING_CREDIT_AT = CODEX_NOW + 30 * 60 * 1000;
export const CODEX_LATER_CREDIT_AT = CODEX_NOW + 11 * DAY;

export const codexSnapshot = (): QuotaSnapshot => ({
  provider: 'codex',
  windows: [
    usage('primary', 'codex:5h', 38, 3 * HOUR, 18000),
    usage('secondary', 'codex:weekly', 60, 4 * DAY, 604800),
    usage('code_review.primary', '', 0, 2 * HOUR, 18000),
    usage(
      'additional.GPT-5.3-Codex-Spark.secondary',
      '',
      10,
      5 * DAY,
      604800,
      'GPT-5.3-Codex-Spark'
    ),
  ],
  extras: {
    kind: 'codex',
    plan: ok('pro'),
    access: ok({ allowed: true, limit_reached: false }),
    credits: ok({ has_credits: true, unlimited: false, balance: '125' }),
    subscription: ok({ active_until: '2026-11-19T00:00:00Z' }),
    reset_credits: ok({
      available: 2,
      applicable_available: 2,
      credits: [
        {
          id: 'credit-later',
          status: 'available',
          granted_at: iso(CODEX_NOW - DAY),
          expires_at: iso(CODEX_LATER_CREDIT_AT),
        },
        {
          id: 'credit-soon',
          status: 'available',
          granted_at: iso(CODEX_NOW - 2 * DAY),
          expires_at: iso(CODEX_EXPIRING_CREDIT_AT),
        },
      ],
    }),
  },
  last_poll: { at: iso(CODEX_NOW - HOUR), trigger: 'on_demand' },
  updated_at: iso(CODEX_NOW - HOUR),
});
