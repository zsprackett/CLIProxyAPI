/**
 * DEV-ONLY preview: plan tiers (standard / premium=gold / flagship=Hetian jade with gold bezel).
 *
 * Registered in App.tsx only when `import.meta.env.DEV` is true, outside the
 * auth guard, so it can be opened without a backend or real accounts:
 *   http://localhost:5173/#/__preview/quota-tiers
 * Renders the real QuotaCard with mocked quota states; Claude and Codex cards
 * read a mocked backend quota snapshot. Copy is intentionally
 * not localized — this page never ships.
 */

import { useState } from 'react';
import { useThemeStore } from '@/stores';
import type {
  AntigravityQuotaState,
  AuthFileItem,
  KimiQuotaState,
  Theme,
  XaiBillingSummary,
  XaiQuotaState,
} from '@/types';
import { HOUR_MS, DAY_MS } from '@/utils/time/durations';
import { QuotaCard } from '../components/QuotaCard';
import type { QuotaFileEntry } from '../logic';
import type { QuotaCardState } from '../providers';
import type { QuotaExtras, QuotaSnapshot, QuotaWindow, Section } from '../snapshot/types';
import pageStyles from '../QuotaPage.module.scss';
import styles from './QuotaTierPreview.module.scss';

interface MockAccount {
  entry: QuotaFileEntry;
  quota: QuotaCardState;
}

const THEMES: { value: Theme; label: string }[] = [
  { value: 'light', label: '羊毛纸' },
  { value: 'white', label: '纯白' },
  { value: 'dark', label: '暗色' },
];

const CARD_STAGGER_MS = 50;
const SIMULATED_REFRESH_MS = 1200;

const file = (name: string, type: string, extra: Partial<AuthFileItem> = {}): AuthFileItem => ({
  name,
  type,
  provider: type,
  ...extra,
});

const nowIso = () => new Date().toISOString();

const okSection = <T,>(value: T): Section<T> => ({
  status: 'ok',
  value,
  value_at: nowIso(),
  checked_at: nowIso(),
});

const absentSection = <T,>(): Section<T> => ({
  status: 'absent',
  value: null,
  value_at: null,
  checked_at: null,
});

const snapshotWindow = (
  id: string,
  windowClass: string,
  usedPercent: number,
  resetInMs: number,
  periodHours: number
): QuotaWindow => ({
  id,
  class: windowClass,
  used_percent: usedPercent,
  limit_reached: false,
  reset_at: new Date(Date.now() + resetInMs).toISOString(),
  period_seconds: periodHours * 3600,
  section: 'poll',
  source: 'poll',
  observed_at: nowIso(),
});

const snapshot = (
  provider: 'claude' | 'codex',
  windows: QuotaWindow[],
  extras: QuotaExtras
): QuotaSnapshot => ({
  provider,
  windows,
  extras,
  last_poll: { at: nowIso(), trigger: 'background' },
  updated_at: nowIso(),
});

/** A snapshot card: the data is on the file, the card state only tracks a click. */
const IDLE_POLL = { status: 'idle' } as QuotaCardState;

const codexAccount = (
  name: string,
  planType: string,
  used: [number, number],
  soon = false
): MockAccount => ({
  entry: {
    type: 'codex',
    file: file(name, 'codex', {
      quota_snapshot: snapshot(
        'codex',
        [
          snapshotWindow(
            'primary_window',
            'codex:5h',
            used[0],
            soon ? 0.4 * HOUR_MS : 3 * HOUR_MS,
            5
          ),
          snapshotWindow('secondary_window', 'codex:weekly', used[1], 4 * DAY_MS, 168),
        ],
        {
          kind: 'codex',
          plan: okSection(planType),
          access: okSection({ allowed: true, limit_reached: false }),
          credits: absentSection(),
          subscription: okSection({
            active_until: new Date(Date.now() + 26 * DAY_MS).toISOString(),
          }),
          reset_credits: absentSection(),
        }
      ),
    }),
  },
  quota: IDLE_POLL,
});

const xaiBilling = (
  planLabel: string,
  planTier: XaiBillingSummary['planTier'],
  usedPercent: number,
  limitCents = 150_000
): XaiBillingSummary => ({
  mode: 'billing',
  periodType: 'monthly',
  usagePercent: null,
  productUsage: [],
  monthlyLimitCents: limitCents,
  usedCents: limitCents * (usedPercent / 100),
  includedUsedCents: limitCents * (usedPercent / 100),
  onDemandCapCents: null,
  onDemandUsedCents: null,
  onDemandUsedPercent: null,
  usedPercent,
  planLabel,
  planTier,
  billingPeriodEnd: new Date(Date.now() + 6 * DAY_MS).toISOString(),
});

const antigravityQuota = (plan: string, remaining: [number, number]): AntigravityQuotaState => ({
  status: 'success',
  subscription: { plan, tierName: null, tierId: null },
  groups: [
    {
      id: 'gemini',
      label: 'Gemini 3 Pro',
      buckets: [
        {
          id: 'gemini',
          label: 'Gemini 3 Pro',
          remainingFraction: remaining[0],
          resetTime: new Date(Date.now() + 6 * HOUR_MS).toISOString(),
        },
      ],
    },
    {
      id: 'claude',
      label: 'Claude Sonnet',
      buckets: [
        {
          id: 'claude',
          label: 'Claude Sonnet',
          remainingFraction: remaining[1],
          resetTime: new Date(Date.now() + 6 * HOUR_MS).toISOString(),
        },
      ],
    },
  ],
});

const claudeAccount = (name: string, planType: string, used: [number, number]): MockAccount => ({
  entry: {
    type: 'claude',
    file: file(name, 'claude', {
      quota_snapshot: snapshot(
        'claude',
        [
          snapshotWindow('five_hour', 'claude:5h', used[0], 2 * HOUR_MS, 5),
          snapshotWindow('seven_day', 'claude:7d', used[1], 3 * DAY_MS, 168),
        ],
        { kind: 'claude', plan: okSection(planType), extra_usage: absentSection() }
      ),
    }),
  },
  quota: IDLE_POLL,
});

const buildAccounts = (): MockAccount[] => [
  ...['Allegro', 'Allegretto', 'Moderato', 'Andante'].map((planName): MockAccount => ({
    entry: { type: 'kimi', file: file(`kimi-${planName.toLowerCase()}.json`, 'kimi') },
    quota: {
      status: 'success',
      planName,
      rows: [
        {
          id: 'five-hour',
          labelKey: 'kimi_quota.limit_window',
          labelParams: { duration: '5h' },
          used: 25,
          limit: 100,
          resetAtMs: Date.now() + 2 * HOUR_MS,
          periodHours: 5,
        },
      ],
    } as KimiQuotaState,
  })),
  codexAccount('codex-pro500.json', 'promax', [6, 21]),
  claudeAccount('claude-max20.json', 'plan_max20', [36, 72]),
  {
    entry: { type: 'xai', file: file('grok-heavy.json', 'xai') },
    quota: {
      status: 'success',
      billing: xaiBilling('SuperGrok Heavy', 'elite', 43),
    } as XaiQuotaState,
  },
  {
    entry: { type: 'antigravity', file: file('antigravity-ultra.json', 'antigravity') },
    quota: antigravityQuota('ultra', [0.91, 0.27]),
  },
  codexAccount('codex-pro200.json', 'pro', [18, 54]),
  {
    entry: { type: 'xai', file: file('grok-supergrok.json', 'xai') },
    quota: {
      status: 'success',
      billing: xaiBilling('SuperGrok', 'premium', 61, 15_000),
    } as XaiQuotaState,
  },
  codexAccount('codex-pro100.json', 'prolite', [82, 48], true),
  claudeAccount('claude-max5.json', 'plan_max5', [12, 40]),
  {
    entry: { type: 'antigravity', file: file('antigravity-ultra-lite.json', 'antigravity') },
    quota: antigravityQuota('ultra-lite', [0.64, 0.12]),
  },
  codexAccount('codex-plus.json', 'plus', [0, 29]),
  codexAccount('codex-business.json', 'self_serve_business_prolite', [40, 33]),
  {
    entry: { type: 'codex', file: file('codex-team.json', 'codex') },
    quota: { status: 'error', error: 'HTTP 401' } as QuotaCardState,
  },
  {
    // Idle card: no plan styling until quota data arrives.
    entry: {
      type: 'codex',
      file: file('codex-pro500-idle.json', 'codex', { plan_type: 'promax' }),
    },
    quota: IDLE_POLL,
  },
];

export function QuotaTierPreview() {
  const theme = useThemeStore((state) => state.theme);
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const setTheme = useThemeStore((state) => state.setTheme);
  const [accounts] = useState(buildAccounts);
  const [refreshing, setRefreshing] = useState(false);
  const [replayKey, setReplayKey] = useState(0);

  const simulateRefresh = () => {
    setRefreshing(true);
    window.setTimeout(() => setRefreshing(false), SIMULATED_REFRESH_MS);
  };

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.titleBlock}>
          <h1 className={styles.title}>额度卡片档位</h1>
          <p className={styles.subtitle}>
            开发环境预览，模拟数据。套餐档位：标准 → 金卡 → 和田玉金镶玉。
          </p>
        </div>
        <div className={styles.controls}>
          <div className={styles.segmented} role="group" aria-label="主题">
            {THEMES.map((option) => (
              <button
                key={option.value}
                type="button"
                className={styles.segment}
                aria-pressed={theme === option.value}
                onClick={() => setTheme(option.value)}
              >
                {option.label}
              </button>
            ))}
          </div>
          <button
            type="button"
            className={styles.replay}
            onClick={simulateRefresh}
            disabled={refreshing}
          >
            模拟刷新（档位应保持）
          </button>
          <button
            type="button"
            className={styles.replay}
            onClick={() => setReplayKey((key) => key + 1)}
          >
            重播入场
          </button>
        </div>
      </header>

      <section className={styles.section}>
        <div key={replayKey} className={pageStyles.grid}>
          {accounts.map((account, index) => (
            <QuotaCard
              key={account.entry.file.name}
              entry={account.entry}
              quota={
                refreshing && account.quota.status !== 'error'
                  ? { status: 'loading' }
                  : account.quota
              }
              resolvedTheme={resolvedTheme}
              canRefresh
              resetting={false}
              entranceDelayMs={index * CARD_STAGGER_MS}
              onRefresh={simulateRefresh}
              onReset={() => undefined}
            />
          ))}
        </div>
      </section>
    </div>
  );
}
