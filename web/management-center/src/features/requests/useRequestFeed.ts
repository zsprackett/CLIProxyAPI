/**
 * Loads the request table and keeps it current.
 *
 * A foreground load replaces the feed. The live tail polls `since=<cursor>`
 * while the page is visible and the connection is up, and merges new rows in
 * front. Responses from an older request or connection are dropped, and a
 * failed background poll keeps the rows on screen.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { useInterval } from '@/hooks/useInterval';
import { apiClient, requestLogApi } from '@/services/api';
import type { RequestFilter, RequestPage } from '@/services/api/requestLog';
import { useAuthStore } from '@/stores';
import {
  PAGE_SIZE,
  TAIL_INTERVAL_MS,
  appendOlder,
  canLoadOlder,
  collectOptions,
  emptyFeed,
  feedFromPage,
  mergePoll,
  requestFilterFromControls,
  type KnownOptions,
  type RequestControls,
  type RequestFeed,
} from './requestsModel';

const FILTER_DEBOUNCE_MS = 300;
const NO_OPTIONS: KnownOptions = { models: [], auths: [], keys: [] };

export interface RequestFeedState {
  feed: RequestFeed;
  enabled: boolean;
  reason: string | null;
  loading: boolean;
  error: string;
  loadingOlder: boolean;
  canLoadOlder: boolean;
  options: KnownOptions;
  /** Masked client key labels reported by the backend, by key hash. */
  keys: Record<string, string>;
  live: boolean;
  setLive: (live: boolean) => void;
  reload: () => void;
  loadOlder: () => void;
}

export function useRequestFeed(controls: RequestControls, active: boolean): RequestFeedState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [applied, setApplied] = useState(controls);
  const [feed, setFeed] = useState<RequestFeed>(emptyFeed);
  const [enabled, setEnabled] = useState(false);
  const [reason, setReason] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [options, setOptions] = useState<KnownOptions>(NO_OPTIONS);
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [live, setLive] = useState(true);

  const requestRef = useRef(0);
  const feedRef = useRef(feed);
  const busyRef = useRef(false);

  useEffect(() => {
    feedRef.current = feed;
  }, [feed]);

  // Typing in the session box must not fire one request per keystroke.
  useEffect(() => {
    const timer = setTimeout(() => setApplied(controls), FILTER_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [controls]);

  const noteMeta = useCallback((page: RequestPage) => {
    setEnabled(page.enabled);
    setReason(page.reason);
    setKeys((current) => ({ ...current, ...page.keys }));
    setOptions((current) => collectOptions(current, page.entries));
  }, []);

  const load = useCallback(
    async (silent: boolean) => {
      const requestId = ++requestRef.current;
      if (!connected) {
        setLoading(false);
        return;
      }
      const revision = apiClient.getConnectionRevision();
      const isCurrent = () =>
        requestId === requestRef.current && revision === apiClient.getConnectionRevision();
      busyRef.current = true;
      if (!silent) {
        setLoading(true);
        setError('');
      }
      try {
        const page = await requestLogApi.list({
          ...requestFilterFromControls(applied, Date.now()),
          limit: PAGE_SIZE,
        });
        if (!isCurrent()) return;
        noteMeta(page);
        setFeed(feedFromPage(page));
        setError('');
      } catch (err: unknown) {
        if (!isCurrent() || silent) return;
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (isCurrent()) {
          busyRef.current = false;
          setLoading(false);
        }
      }
    },
    [applied, connected, noteMeta]
  );

  // A filter or connection change starts a new feed; the old rows stay until it arrives.
  useEffect(() => {
    if (!active) return;
    void load(false);
    return () => {
      requestRef.current += 1;
      busyRef.current = false;
    };
  }, [active, load]);

  const poll = useCallback(async () => {
    const current = feedRef.current;
    if (busyRef.current || !current.epoch) return;
    const requestId = requestRef.current;
    const revision = apiClient.getConnectionRevision();
    busyRef.current = true;
    try {
      const filter: RequestFilter = {
        ...requestFilterFromControls(applied, Date.now()),
        since: current.cursor,
        limit: PAGE_SIZE,
      };
      delete filter.fromMs;
      const page = await requestLogApi.list(filter);
      if (requestId !== requestRef.current || revision !== apiClient.getConnectionRevision()) {
        return;
      }
      const merged = mergePoll(current, page);
      // A full page means rows may be missing between the cursor and the oldest returned.
      if (merged.reset || page.more) {
        busyRef.current = false;
        void load(true);
        return;
      }
      noteMeta(page);
      setFeed(merged.feed);
    } catch {
      // The next tick retries; the rows on screen stay.
    } finally {
      busyRef.current = false;
    }
  }, [applied, load, noteMeta]);

  useInterval(
    () => {
      if (document.visibilityState === 'hidden') return;
      void poll();
    },
    active && live && connected && enabled ? TAIL_INTERVAL_MS : null
  );

  const loadOlder = useCallback(async () => {
    const current = feedRef.current;
    if (!canLoadOlder(current) || busyRef.current) return;
    const requestId = requestRef.current;
    const revision = apiClient.getConnectionRevision();
    busyRef.current = true;
    setLoadingOlder(true);
    try {
      const page = await requestLogApi.list({
        ...requestFilterFromControls(applied, Date.now()),
        before: current.oldest,
        limit: PAGE_SIZE,
      });
      if (requestId !== requestRef.current || revision !== apiClient.getConnectionRevision()) {
        return;
      }
      if (page.epoch === current.epoch) {
        setFeed((latest) => appendOlder(latest, page));
        setOptions((known) => collectOptions(known, page.entries));
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      busyRef.current = false;
      setLoadingOlder(false);
    }
  }, [applied]);

  return {
    feed,
    enabled,
    reason,
    loading,
    error,
    loadingOlder,
    canLoadOlder: canLoadOlder(feed),
    options,
    keys,
    live,
    setLive,
    reload: () => void load(false),
    loadOlder: () => void loadOlder(),
  };
}
