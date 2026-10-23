/**
 * Loads the usage report for a query and keeps it current while the page is
 * visible and the range includes today. Same stale guards as useQuotaForecast:
 * a foreground load owns the loading state, background reads never preempt it,
 * and responses from an older request, query or connection are dropped.
 *
 * The server flushes pending rollups before it answers, so a refresh shows
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

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

/**
 * `query` must be referentially stable per distinct query (UsagePage memoizes
 * it). `live` = the range covers today; false stops polling. A query change
 * keeps the previous report on screen (dimmed through `loading`) so switching
 * group-by does not flash; a connection change blanks it.
 */
export function useUsageReport(query: UsageQuery, live: boolean): UsageReportState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [report, setReport] = useState<UsageReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const requestRef = useRef(0);
  const foregroundRef = useRef(false);
  const revisionRef = useRef(apiClient.getConnectionRevision());

  const load = useCallback(
    async (background: boolean) => {
      if (background && (foregroundRef.current || !connected)) return;
      const requestId = ++requestRef.current;
      if (!connected) {
        setReport(null);
        setLoading(false);
        return;
      }
      const revision = apiClient.getConnectionRevision();
      if (revision !== revisionRef.current) {
        revisionRef.current = revision;
        setReport(null);
      }
      const isCurrent = () =>
        requestId === requestRef.current && revision === apiClient.getConnectionRevision();
      if (!background) {
        foregroundRef.current = true;
        setLoading(true);
        setError('');
      }
      try {
        const next = await usageHistoryApi.get(query);
        if (!isCurrent()) return;
        setReport(next);
        setError('');
      } catch (err: unknown) {
        // A failed background read keeps the last report; the next tick retries.
        if (!isCurrent() || background) return;
        setError(errorText(err));
      } finally {
        if (!background && isCurrent()) {
          foregroundRef.current = false;
          setLoading(false);
        }
      }
    },
    [connected, query]
  );

  useEffect(() => {
    foregroundRef.current = false;
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

  const reload = useCallback(() => void load(false), [load]);
  return { report, loading, error, reload };
}

export interface UsagePricesState {
  prices: UsagePrices | null;
  loading: boolean;
  error: string;
}

/**
 * Loads prices when `open` turns true (the price panel is collapsed by default)
 * and again on each reopen, so a price refresh on the server shows up.
 */
export function useUsagePrices(open: boolean): UsagePricesState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [prices, setPrices] = useState<UsagePrices | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !connected) return;
    let current = true;
    const revision = apiClient.getConnectionRevision();
    const isCurrent = () => current && revision === apiClient.getConnectionRevision();
    setLoading(true);
    setError('');
    usageHistoryApi
      .prices()
      .then((next) => {
        if (isCurrent()) setPrices(next);
      })
      .catch((err: unknown) => {
        if (isCurrent()) setError(errorText(err));
      })
      .finally(() => {
        if (isCurrent()) setLoading(false);
      });
    return () => {
      current = false;
    };
  }, [connected, open]);

  return { prices, loading, error };
}
