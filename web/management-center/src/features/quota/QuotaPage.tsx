/**
 * 额度查询页：提供商 tabs + 统一卡网格。
 *
 * 保留的行为契约（重设计不改）：
 * - 现有提供商保持点击加载；Devin 首次可见时主动查询一次，不轮询；
 * - cacheGeneration 会话隔离 + request-id 去重（见 useQuotaBatchLoader）；
 * - 文件列表变化后按 provider 剪枝额度缓存（已删文件不残留）；
 * - useHeaderRefresh 单槽位：本页唯一注册者，全局刷新 = 重取文件列表。
 * - While open, the file list is re-read in the background every
 *   QUOTA_FILES_REFRESH_MS so backend quota snapshots stay current. Background
 *   reads never show the loading state and never trigger provider quota fetches.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { authFilesApi, QUOTA_FORECAST_PROVIDERS, type QuotaForecastProvider } from '@/services/api';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { IconSearch, IconX } from '@/components/ui/icons';
import { Select } from '@/components/ui/Select';
import { Skeleton } from '@/components/ui/Skeleton';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useInterval } from '@/hooks/useInterval';
import { useNow } from '@/hooks/useNow';
import { useRevealGroup } from '@/hooks/motion';
import { useAuthStore, useConfigStore, useQuotaStore, useThemeStore } from '@/stores';
import type { AuthFileItem, ResolvedTheme } from '@/types';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { ProviderTabs } from '@/features/authFiles/components/ProviderTabs';
import { QuotaHeader } from './components/QuotaHeader';
import { QuotaCard } from './components/QuotaCard';
import { QuotaTimeline } from './components/QuotaTimeline';
import { QuotaLedger, type LedgerItem } from './components/QuotaLedger';
import { QuotaSummaryStrip } from './components/QuotaSummaryStrip';
import { QuotaForecast } from './forecast/QuotaForecast';
import {
  CARD_ENTRANCE_BUDGET_MS,
  QUOTA_FILES_REFRESH_MS,
  QUOTA_PAGE_SIZE,
  QUOTA_SORT_MODES,
  QUOTA_TAB_ORDER,
  QUOTA_VIEW_MODES,
  type QuotaSortMode,
  type QuotaTabId,
  type QuotaViewMode,
} from './constants';
import {
  isCredentialCoolingDown,
  maskIdentity,
  predictSoonestResetPick,
  resolveLedgerRow,
  summarizeProvider,
  type LedgerCredential,
} from './ledgerModel';
import {
  buildTabCounts,
  canRefreshQuotaAfterList,
  classifyQuotaFiles,
  countQuotaCredentials,
  filterEntriesByTab,
  filterEntriesBySearch,
  paginate,
  sortQuotaEntries,
  type QuotaFileEntry,
} from './logic';
import { nextRecoveryMs } from './resetSchedule';
import { applyPolledSnapshot, readQuotaSnapshot } from './snapshot/windows';
import { QUOTA_ADAPTERS, getQuotaSetter, type QuotaCardState } from './providers';
import type { QuotaProviderType } from './providers/types';
import { useDevinQuotaAutoLoad } from './providers/devin/useDevinQuotaAutoLoad';
import { useQuotaActions } from './hooks/useQuotaActions';
import { useQuotaBatchLoader } from './hooks/useQuotaBatchLoader';
import { readQuotaUiState, writeQuotaUiState } from './uiState';
import styles from './QuotaPage.module.scss';

const TAB_IDS: string[] = ['all', ...QUOTA_TAB_ORDER];
const SKELETON_CARD_COUNT = 6;

export function QuotaPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const resolvedTheme: ResolvedTheme = useThemeStore((state) => state.resolvedTheme);

  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<QuotaTabId>(() => readQuotaUiState()?.tab ?? 'all');
  const [sortMode, setSortMode] = useState<QuotaSortMode>(
    () => readQuotaUiState()?.sortMode ?? 'default'
  );
  const [viewMode, setViewMode] = useState<QuotaViewMode>(
    () => readQuotaUiState()?.viewMode ?? 'ledger'
  );
  const [showIdentities, setShowIdentities] = useState(
    () => readQuotaUiState()?.showIdentities ?? false
  );
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const searchInputRef = useRef<HTMLInputElement>(null);
  // 页头 + tabs 的入场级联（标题 → meta → 动作 → tabs，级差 70ms）
  const revealRef = useRevealGroup<HTMLDivElement>();

  const disableControls = connectionStatus !== 'connected';

  /* ---------- 文件列表 ---------- */

  const sessionGeneration = useQuotaStore((state) => state.cacheGeneration);
  const [filesGeneration, setFilesGeneration] = useState<number | null>(null);
  const listRequestRef = useRef(0);
  // A foreground load owns the loading state; a background read never preempts it.
  const foregroundLoadRef = useRef(false);
  const loadFiles = useCallback(
    async (options?: { background?: boolean }) => {
      const background = options?.background === true;
      if (background && (foregroundLoadRef.current || connectionStatus !== 'connected')) return;
      const requestId = ++listRequestRef.current;
      if (connectionStatus !== 'connected') {
        setFiles([]);
        setFilesGeneration(null);
        setLoading(false);
        return;
      }
      const isCurrent = () =>
        requestId === listRequestRef.current &&
        sessionGeneration === useQuotaStore.getState().cacheGeneration;
      if (!background) {
        foregroundLoadRef.current = true;
        setLoading(true);
        setError('');
      }
      try {
        const data = await authFilesApi.list();
        if (!isCurrent()) return;
        setFiles(data?.files || []);
        setFilesGeneration(sessionGeneration);
        if (background) setError('');
      } catch (err: unknown) {
        // A failed background read keeps the current list; the next tick retries.
        if (!isCurrent() || background) return;
        const message = err instanceof Error ? err.message : t('notification.refresh_failed');
        setError(message);
      } finally {
        if (!background && isCurrent()) {
          foregroundLoadRef.current = false;
          setLoading(false);
        }
      }
    },
    [connectionStatus, sessionGeneration, t]
  );

  useHeaderRefresh(loadFiles);

  useInterval(
    () => {
      if (document.visibilityState === 'hidden') return;
      void loadFiles({ background: true });
    },
    connectionStatus === 'connected' ? QUOTA_FILES_REFRESH_MS : null
  );

  useEffect(() => {
    void loadFiles();
    return () => {
      listRequestRef.current += 1;
    };
  }, [loadFiles]);

  /* ---------- 额度缓存 ----------
   * 排在归类/排序之前：「最快恢复优先」要读它算排序键。 */

  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const quotaPolls = useQuotaStore((state) => state.quotaPolls);
  const quotaSnapshots = useQuotaStore((state) => state.quotaSnapshots);
  const devinQuota = useQuotaStore((state) => state.devinQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const metaQuota = useQuotaStore((state) => state.metaQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);

  const quotaByType = useMemo<Record<QuotaProviderType, Record<string, QuotaCardState>>>(
    () =>
      ({
        antigravity: antigravityQuota,
        claude: quotaPolls,
        codex: quotaPolls,
        devin: devinQuota,
        kimi: kimiQuota,
        meta: metaQuota,
        xai: xaiQuota,
      }) as unknown as Record<QuotaProviderType, Record<string, QuotaCardState>>,
    [antigravityQuota, quotaPolls, devinQuota, kimiQuota, metaQuota, xaiQuota]
  );

  const getQuota = useCallback(
    (entry: QuotaFileEntry): QuotaCardState | undefined =>
      quotaByType[entry.type][getQuotaCacheKey(entry.file)],
    [quotaByType]
  );

  /* ---------- 归类 / 过滤 / 排序 / 分页 ---------- */

  // 只在「最快恢复优先」下订阅分钟时钟。默认序下不门控的话，pageItems 每分钟
  // 换一次身份，会反复空转下面那个「刷新全部」的 loading 下降沿 effect。
  const tick = useNow(sortMode !== 'default');
  const sortNow = sortMode === 'default' ? 0 : tick;

  // A click's poll result stands in for the listing snapshot until the listing catches up.
  const entries = useMemo(
    () => classifyQuotaFiles(files.map((file) => applyPolledSnapshot(file, quotaSnapshots))),
    [files, quotaSnapshots]
  );
  const tabCounts = useMemo(() => buildTabCounts(entries), [entries]);
  const filteredEntries = useMemo(
    () => filterEntriesBySearch(filterEntriesByTab(entries, tab), search),
    [entries, tab, search]
  );
  const handleSearchChange = useCallback((value: string) => {
    setSearch(value);
    setPage(1);
  }, []);

  const resolveNextRecovery = useCallback(
    (entry: QuotaFileEntry) =>
      nextRecoveryMs(
        entry.type,
        getQuota(entry),
        sortNow,
        readQuotaSnapshot(entry.type, entry.file.quota_snapshot)
      ),
    [getQuota, sortNow]
  );
  // 排序在分页之前：否则「最快恢复」只在当前页内成立。
  const sortedEntries = useMemo(
    () => sortQuotaEntries(filteredEntries, sortMode, resolveNextRecovery),
    [filteredEntries, sortMode, resolveNextRecovery]
  );

  const { pageItems, currentPage, totalPages } = useMemo(
    () => paginate(sortedEntries, page, QUOTA_PAGE_SIZE),
    [sortedEntries, page]
  );

  const handleTabChange = useCallback((next: string) => {
    setTab(next as QuotaTabId);
    setPage(1);
    writeQuotaUiState({ tab: next as QuotaTabId });
  }, []);

  const handleSortModeChange = useCallback((next: string) => {
    setSortMode(next as QuotaSortMode);
    setPage(1);
    writeQuotaUiState({ sortMode: next as QuotaSortMode });
  }, []);

  const sortOptions = useMemo(
    () =>
      QUOTA_SORT_MODES.map((mode) => ({ value: mode, label: t(`quota_management.sort_${mode}`) })),
    [t]
  );

  const handleViewModeChange = useCallback((next: string) => {
    setViewMode(next as QuotaViewMode);
    writeQuotaUiState({ viewMode: next as QuotaViewMode });
  }, []);

  const viewOptions = useMemo(
    () =>
      QUOTA_VIEW_MODES.map((mode) => ({ value: mode, label: t(`quota_management.view_${mode}`) })),
    [t]
  );

  const toggleIdentities = useCallback(() => {
    setShowIdentities((prev) => {
      writeQuotaUiState({ showIdentities: !prev });
      return !prev;
    });
  }, []);

  // Pooled forecasts exist for these providers only; the All tab shows every pool's summary.
  const forecastProvider: QuotaForecastProvider | null | undefined =
    tab === 'all'
      ? null
      : (QUOTA_FORECAST_PROVIDERS as readonly string[]).includes(tab)
        ? (tab as QuotaForecastProvider)
        : undefined;

  /* ---------- Ledger rows and provider totals ---------- */

  const now = useNow();
  const routingStrategy = useConfigStore((state) => state.config?.routingStrategy);
  useEffect(() => {
    if (connectionStatus !== 'connected') return;
    void useConfigStore
      .getState()
      .fetchConfig()
      .catch(() => {
        // The next-pick hint is optional; the page works without config.
      });
  }, [connectionStatus]);

  const credentials = useMemo(
    () =>
      new Map(
        entries.map((entry): [QuotaFileEntry, LedgerCredential] => [
          entry,
          {
            key: getQuotaCacheKey(entry.file),
            name: entry.file.name,
            provider: entry.type,
            disabled: Boolean(entry.file.disabled),
            coolingDown: isCredentialCoolingDown(entry.file, now),
            row: resolveLedgerRow(entry.type, entry.file, getQuota(entry), now),
          },
        ])
      ),
    [entries, getQuota, now]
  );

  const summaries = useMemo(() => {
    const byProvider = new Map<QuotaProviderType, LedgerCredential[]>();
    for (const entry of filteredEntries) {
      const credential = credentials.get(entry);
      if (!credential) continue;
      const group = byProvider.get(entry.type);
      if (group) group.push(credential);
      else byProvider.set(entry.type, [credential]);
    }
    return QUOTA_TAB_ORDER.filter((type) => byProvider.has(type)).map((type) =>
      summarizeProvider(type, byProvider.get(type) ?? [], now)
    );
  }, [credentials, filteredEntries, now]);

  // The router picks within a provider across every credential, not just the filtered ones.
  const nextPickKeys = useMemo(() => {
    const keys = new Set<string>();
    if (routingStrategy?.trim().toLowerCase() !== 'soonest-reset') return keys;
    for (const type of ['claude', 'codex'] as const) {
      const pool = entries
        .filter((entry) => entry.type === type)
        .map((entry) => credentials.get(entry))
        .filter((credential): credential is LedgerCredential => credential !== undefined);
      const key = predictSoonestResetPick(pool, now);
      if (key) keys.add(key);
    }
    return keys;
  }, [credentials, entries, now, routingStrategy]);

  const ledgerItems = useMemo(
    () =>
      pageItems
        .map((entry): LedgerItem | null => {
          const credential = credentials.get(entry);
          return credential ? { entry, credential, quota: getQuota(entry) } : null;
        })
        .filter((item): item is LedgerItem => item !== null),
    [credentials, getQuota, pageItems]
  );

  // Existing providers display filenames; Devin's card and timeline share an
  // identity-aware display label. Masking applies to whichever label is shown.
  const displayNameForView = useCallback(
    (name: string) => (showIdentities ? name : maskIdentity(name)),
    [showIdentities]
  );

  const { loadedCount, attentionCount } = useMemo(
    () =>
      countQuotaCredentials(
        entries,
        (entry) => quotaByType[entry.type][getQuotaCacheKey(entry.file)]?.status
      ),
    [entries, quotaByType]
  );

  // Pruning: once the file list settles, each quota cache keeps only the
  // credentials that still exist. Claude and Codex share one card-state map.
  useEffect(() => {
    if (loading || error || filesGeneration !== sessionGeneration) return;
    const survivorsBySetter = new Map<string, Set<string>>(
      QUOTA_TAB_ORDER.map((type) => [QUOTA_ADAPTERS[type].storeSetter, new Set<string>()])
    );
    entries.forEach((entry) =>
      survivorsBySetter
        .get(QUOTA_ADAPTERS[entry.type].storeSetter)
        ?.add(getQuotaCacheKey(entry.file))
    );

    survivorsBySetter.forEach((survivors, storeSetter) => {
      const type = QUOTA_TAB_ORDER.find((tab) => QUOTA_ADAPTERS[tab].storeSetter === storeSetter);
      if (!type) return;
      const setQuota = getQuotaSetter(QUOTA_ADAPTERS[type]);
      setQuota((prev) => {
        const staleKeys = Object.keys(prev).filter((name) => !survivors.has(name));
        if (staleKeys.length === 0) return prev;
        const next = { ...prev };
        staleKeys.forEach((name) => delete next[name]);
        return next;
      });
    });
    useQuotaStore.getState().reconcileQuotaSnapshots(files);
  }, [entries, error, files, filesGeneration, loading, sessionGeneration]);

  /* ---------- 加载与操作 ---------- */

  const { batchLoading, loadQuota } = useQuotaBatchLoader();
  const { resettingQuotaName, refreshQuota, resetQuota } = useQuotaActions(disableControls);

  const pendingRefreshRef = useRef<number | null>(null);
  const prevLoadingRef = useRef(loading);

  // 刷新全部：先重取文件列表，待其落定（loading 下降沿）再批量拉当前页额度
  const handleRefreshAll = useCallback(() => {
    if (disableControls) return;
    pendingRefreshRef.current = sessionGeneration;
    void loadFiles();
  }, [disableControls, loadFiles, sessionGeneration]);

  useEffect(() => {
    const wasLoading = prevLoadingRef.current;
    prevLoadingRef.current = loading;

    const requestedSession = pendingRefreshRef.current;
    if (requestedSession === null) return;
    if (requestedSession !== sessionGeneration) {
      pendingRefreshRef.current = null;
      return;
    }
    if (loading || !wasLoading) return;

    pendingRefreshRef.current = null;
    if (
      canRefreshQuotaAfterList(
        requestedSession,
        sessionGeneration,
        filesGeneration,
        Boolean(error),
        disableControls
      )
    ) {
      void loadQuota(pageItems);
    }
  }, [disableControls, error, filesGeneration, loading, loadQuota, pageItems, sessionGeneration]);

  useDevinQuotaAutoLoad(
    pageItems,
    disableControls ||
      loading ||
      batchLoading ||
      Boolean(error) ||
      filesGeneration !== sessionGeneration,
    loadQuota
  );

  const canUseActions = !disableControls && !loading && filesGeneration === sessionGeneration;

  /* ---------- 首屏卡片一次性级联入场 ----------
   * 首批数据渲染后立即翻转 cardsAnimated；已挂载的卡片在挂载时捕获过自己的
   * 延迟（QuotaCard 内 useState 初始化），后续切 tab/翻页/刷新新挂载的卡片
   * 拿到 null —— 不重播。 */

  const [cardsAnimated, setCardsAnimated] = useState(false);
  const enableCardEntrance = !cardsAnimated && !loading && pageItems.length > 0;
  useEffect(() => {
    if (enableCardEntrance) {
      setCardsAnimated(true);
    }
  }, [enableCardEntrance]);
  const cardEntranceDelay = (index: number): number | null => {
    if (!enableCardEntrance) return null;
    if (pageItems.length <= 1) return 0;
    return Math.round((index / (pageItems.length - 1)) * CARD_ENTRANCE_BUDGET_MS);
  };

  /* ---------- 渲染 ---------- */

  const isEmpty = !loading && filteredEntries.length === 0;

  return (
    <div className={styles.page} ref={revealRef}>
      <QuotaHeader
        totalCount={entries.length}
        loadedCount={loadedCount}
        attentionCount={attentionCount}
        refreshing={loading || batchLoading}
        disableControls={disableControls}
        onRefreshAll={handleRefreshAll}
      />

      <section className={styles.workbench}>
        {/* 提供商导航与搜索工具栏分层，避免不同控件争夺视觉焦点。 */}
        <div className={styles.tabsRow} data-reveal>
          <ProviderTabs
            types={TAB_IDS}
            counts={tabCounts}
            active={tab}
            resolvedTheme={resolvedTheme}
            onChange={handleTabChange}
          />
        </div>

        <div className={styles.toolbar}>
          <div className={styles.search}>
            <IconSearch size={16} className={styles.searchIcon} aria-hidden="true" />
            <input
              ref={searchInputRef}
              className={styles.searchInput}
              type="search"
              value={search}
              onChange={(event) => handleSearchChange(event.target.value)}
              placeholder={t('quota_management.search_placeholder')}
              aria-label={t('quota_management.search_label')}
            />
            {search && (
              <button
                type="button"
                className={styles.clearSearch}
                aria-label={t('quota_management.search_clear')}
                title={t('quota_management.search_clear')}
                onClick={() => {
                  handleSearchChange('');
                  searchInputRef.current?.focus();
                }}
              >
                <IconX size={14} aria-hidden="true" />
              </button>
            )}
          </div>
          <div className={styles.sort}>
            <Button
              variant="ghost"
              size="sm"
              className={styles.identityToggle}
              aria-pressed={showIdentities}
              onClick={toggleIdentities}
            >
              {showIdentities
                ? t('quota_management.hide_identities')
                : t('quota_management.show_identities')}
            </Button>
            <Select
              value={viewMode}
              options={viewOptions}
              onChange={handleViewModeChange}
              ariaLabel={t('quota_management.view_label')}
              size="sm"
            />
            <Select
              value={sortMode}
              options={sortOptions}
              onChange={handleSortModeChange}
              ariaLabel={t('quota_management.sort_label')}
              size="sm"
            />
          </div>
        </div>

        {error && (
          <div className={styles.errorBanner} role="alert">
            {error}
          </div>
        )}

        {!loading && viewMode === 'ledger' && (
          <QuotaSummaryStrip summaries={summaries} resolvedTheme={resolvedTheme} nowMs={now} />
        )}

        {!loading && forecastProvider !== undefined && (tabCounts[tab] ?? 0) > 0 && (
          <QuotaForecast
            provider={forecastProvider}
            nowMs={now}
            showIdentities={showIdentities}
            resolvedTheme={resolvedTheme}
          />
        )}

        {loading ? (
          <div className={styles.grid} aria-hidden="true">
            {Array.from({ length: SKELETON_CARD_COUNT }, (_, index) => (
              <Skeleton key={index} height={168} rounded={14} />
            ))}
          </div>
        ) : isEmpty ? (
          <EmptyState
            title={
              search.trim()
                ? t('quota_management.search_empty_title')
                : tab === 'all'
                  ? t('quota_management.empty_title')
                  : t(`${QUOTA_ADAPTERS[tab].i18nPrefix}.empty_title`)
            }
            description={
              search.trim()
                ? t('quota_management.search_empty_desc')
                : tab === 'all'
                  ? t('quota_management.empty_desc')
                  : t(`${QUOTA_ADAPTERS[tab].i18nPrefix}.empty_desc`)
            }
            action={
              search.trim() ? (
                <Button variant="secondary" size="sm" onClick={() => handleSearchChange('')}>
                  {t('quota_management.search_clear')}
                </Button>
              ) : tab === 'all' ? undefined : (
                <Button variant="secondary" size="sm" onClick={() => handleTabChange('all')}>
                  {t('auth_files.filter_all')}
                </Button>
              )
            }
          />
        ) : viewMode === 'ledger' ? (
          <QuotaLedger
            items={ledgerItems}
            resolvedTheme={resolvedTheme}
            nowMs={now}
            showIdentities={showIdentities}
            canRefresh={(entry) => canUseActions && !entry.file.disabled}
            nextPickKeys={nextPickKeys}
            onRefresh={(entry) => void refreshQuota(entry.file, QUOTA_ADAPTERS[entry.type])}
          />
        ) : (
          <div className={styles.grid}>
            {pageItems.map((entry, index) => (
              <QuotaCard
                key={`${entry.type}:${getQuotaCacheKey(entry.file)}`}
                entry={entry}
                quota={getQuota(entry)}
                resolvedTheme={resolvedTheme}
                showIdentity={showIdentities}
                canRefresh={canUseActions && !entry.file.disabled}
                resetting={resettingQuotaName === getQuotaCacheKey(entry.file)}
                entranceDelayMs={cardEntranceDelay(index)}
                onRefresh={() => void refreshQuota(entry.file, QUOTA_ADAPTERS[entry.type])}
                onReset={() => resetQuota(entry.file, QUOTA_ADAPTERS[entry.type])}
              />
            ))}
          </div>
        )}

        {!loading && filteredEntries.length > QUOTA_PAGE_SIZE && (
          <div className={styles.pagination}>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setPage(Math.max(1, currentPage - 1))}
              disabled={currentPage <= 1}
            >
              {t('auth_files.pagination_prev')}
            </Button>
            <div className={styles.pageInfo}>
              {t('auth_files.pagination_info', {
                current: currentPage,
                total: totalPages,
                count: filteredEntries.length,
              })}
            </div>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setPage(Math.min(totalPages, currentPage + 1))}
              disabled={currentPage >= totalPages}
            >
              {t('auth_files.pagination_next')}
            </Button>
          </div>
        )}

        {/* 时间线只比较当前页凭证，避免大量凭证一次性生成无界泳道。 */}
        <QuotaTimeline
          entries={pageItems}
          quotaFor={getQuota}
          displayNameFor={displayNameForView}
          resolvedTheme={resolvedTheme}
        />
      </section>
    </div>
  );
}
