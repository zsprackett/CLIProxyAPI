/**
 * Loads the quota forecast for one provider (or every pool's summary when
 * `provider` is null) and keeps it current while the page is visible.
 *
 * Mirrors QuotaPage's file-list loader: a foreground load owns the loading
 * state, background reads never preempt it, and responses from an older
 * request or connection are dropped.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { useInterval } from '@/hooks/useInterval';
import { apiClient, quotaForecastApi } from '@/services/api';
import type { QuotaForecast, QuotaForecastProvider } from '@/services/api/quotaForecast';
import { useAuthStore, useQuotaStore } from '@/stores';
import { QUOTA_FILES_REFRESH_MS } from '../constants';

export interface QuotaForecastState {
  data: QuotaForecast | null;
  loading: boolean;
  error: string;
}

export function useQuotaForecast(provider: QuotaForecastProvider | null): QuotaForecastState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  // Bumped when the session changes; a new session reloads from scratch.
  const session = useQuotaStore((state) => state.cacheGeneration);
  const [data, setData] = useState<QuotaForecast | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const requestRef = useRef(0);
  const foregroundRef = useRef(false);

  const load = useCallback(
    async (background: boolean) => {
      if (background && (foregroundRef.current || !connected)) return;
      const requestId = ++requestRef.current;
      if (!connected) {
        setData(null);
        setLoading(false);
        return;
      }
      const revision = apiClient.getConnectionRevision();
      const isCurrent = () =>
        requestId === requestRef.current &&
        revision === apiClient.getConnectionRevision() &&
        session === useQuotaStore.getState().cacheGeneration;
      if (!background) {
        foregroundRef.current = true;
        setLoading(true);
        setError('');
      }
      try {
        const next = await quotaForecastApi.get({ provider });
        if (!isCurrent()) return;
        setData(next);
        setError('');
      } catch (err: unknown) {
        // A failed background read keeps the last forecast; the next tick retries.
        if (!isCurrent() || background) return;
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!background && isCurrent()) {
          foregroundRef.current = false;
          setLoading(false);
        }
      }
    },
    [connected, provider, session]
  );

  // A provider or connection switch starts from a blank forecast, never the old one.
  useEffect(() => {
    setData(null);
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
    connected ? QUOTA_FILES_REFRESH_MS : null
  );

  return { data, loading, error };
}
