/**
 * Loads the usage report for a query and keeps it current while the page is
 * visible and the range includes today. Same stale guards as useQuotaForecast:
 * a foreground load owns the loading state, background reads never preempt it,
 * and responses from an older request, query or connection are dropped.
 *
 * The server flushes pending rollups before answering, so a refresh shows
 * requests made seconds ago; 60s polling is enough.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { useInterval } from '@/hooks/useInterval';
import { apiClient, usageHistoryApi } from '@/services/api';
import type { UsagePrices, UsageQuery, UsageReport } from '@/services/api/usageHistory';
import { useAuthStore } from '@/stores';

export const USAGE_REFRESH_MS = 60_000;

export interface UsageReportState {
  report: UsageReport | null;
  loading: boolean;
  error: string;
  reload: () => void;
}

/**
 * `query` must be referentially stable per distinct query (UsagePage memoizes it
 * on a serialized key). `live` = range covers today; false disables polling.
 *
 * pseudocode: as useQuotaForecast.load(background), with
 *   const next = await usageHistoryApi.get(query);
 *   isCurrent() also compares the serialized query captured at call time.
 *   A query change keeps the previous report on screen (dimmed via `loading`)
 *   instead of blanking, so switching group-by does not flash; a connection
 *   change blanks it.
 */
export function useUsageReport(query: UsageQuery, live: boolean): UsageReportState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [report, setReport] = useState<UsageReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const requestRef = useRef(0);
  const foregroundRef = useRef(false);

  const load = useCallback(
    async (background: boolean) => {
      void apiClient;
      void usageHistoryApi;
      void background;
      throw new Error('not implemented');
    },
    [connected, query]
  );

  useEffect(() => {
    void load(false);
    return () => {
      requestRef.current += 1;
    };
  }, [load]);

  useInterval(
    () => {
      if (document.visibilityState === 'hidden') return;
      void load(true);
    },
    connected && live ? USAGE_REFRESH_MS : null
  );

  return { report, loading, error, reload: () => void load(false) };
}

/** Loads prices once when `open` turns true (the price panel is collapsed by default). */
export function useUsagePrices(open: boolean): {
  prices: UsagePrices | null;
  loading: boolean;
  error: string;
} {
  throw new Error('not implemented');
}
