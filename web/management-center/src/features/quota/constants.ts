import type { QuotaProviderType } from './providers/types';

/** tab 顺序 = 旧页五分区的纵向顺序，'全部' tab 下卡片也按此分组排列。 */
export const QUOTA_TAB_ORDER: readonly QuotaProviderType[] = [
  'claude',
  'antigravity',
  'codex',
  'xai',
  'kimi',
  'devin',
  'meta',
];

export type QuotaTabId = 'all' | QuotaProviderType;

/** 页级分页固定 20/页，同时把「刷新全部」的上游并发限制在 20。 */
export const QUOTA_PAGE_SIZE = 20;

/** 卡片排序：默认 = provider 分组序；soonest = 最快恢复优先。 */
export const QUOTA_SORT_MODES = ['default', 'soonest'] as const;

export type QuotaSortMode = (typeof QUOTA_SORT_MODES)[number];

/** Page layout: dense per-credential rows (default) or the card grid. */
export const QUOTA_VIEW_MODES = ['ledger', 'cards'] as const;

export type QuotaViewMode = (typeof QUOTA_VIEW_MODES)[number];

/** 与 useRevealGroup 的 GROUP_MAX_TOTAL 一致：卡片级联总预算 360ms。 */
export const CARD_ENTRANCE_BUDGET_MS = 360;

/**
 * How often the open quota page silently re-reads the credential list, so
 * backend quota snapshots (traffic or the background quota poll) show up
 * without a manual refresh. Local management call only; no provider requests.
 */
export const QUOTA_FILES_REFRESH_MS = 60_000;
