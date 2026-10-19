/**
 * Loads the session list and the timeline of one session. Both drop answers
 * from an older request or connection, and keep their last data on error.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { apiClient, requestLogApi } from '@/services/api';
import type { RequestEntry, SessionPage, SessionSummary } from '@/services/api/requestLog';
import { useAuthStore } from '@/stores';
import { DEFAULT_CONTROLS, requestFilterFromControls, type RequestControls } from './requestsModel';

const SESSION_LIMIT = 200;
const TIMELINE_LIMIT = 2000;

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

export interface SessionListState {
  sessions: SessionSummary[];
  enabled: boolean;
  reason: string | null;
  keys: Record<string, string>;
  loading: boolean;
  error: string;
  reload: () => void;
}

export function useSessionList(controls: RequestControls, active: boolean): SessionListState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [page, setPage] = useState<SessionPage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestRef = useRef(0);

  const { key, auth, model, range } = controls;
  const load = useCallback(async () => {
    const requestId = ++requestRef.current;
    if (!connected) return;
    const revision = apiClient.getConnectionRevision();
    const isCurrent = () =>
      requestId === requestRef.current && revision === apiClient.getConnectionRevision();
    setLoading(true);
    setError('');
    try {
      const filter = requestFilterFromControls(
        { ...DEFAULT_CONTROLS, key, auth, model, range },
        Date.now()
      );
      const next = await requestLogApi.sessions({
        key: filter.key,
        auth: filter.auth,
        model: filter.model,
        fromMs: filter.fromMs,
        limit: SESSION_LIMIT,
      });
      if (isCurrent()) setPage(next);
    } catch (err: unknown) {
      if (isCurrent()) setError(errorText(err));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [auth, connected, key, model, range]);

  useEffect(() => {
    if (!active) return;
    void load();
    return () => {
      requestRef.current += 1;
    };
  }, [active, load]);

  return {
    sessions: page?.sessions ?? [],
    enabled: page?.enabled ?? false,
    reason: page?.reason ?? null,
    keys: page?.keys ?? {},
    loading,
    error,
    reload: () => void load(),
  };
}

export interface SessionTimelineState {
  rows: RequestEntry[];
  truncated: boolean;
  loading: boolean;
  error: string;
  reload: () => void;
}

/** The newest requests of a session, returned oldest first for drawing. */
export function useSessionTimeline(sessionId: string | null): SessionTimelineState {
  const connected = useAuthStore((state) => state.connectionStatus) === 'connected';
  const [rows, setRows] = useState<RequestEntry[]>([]);
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const requestId = ++requestRef.current;
    if (!sessionId || !connected) return;
    const revision = apiClient.getConnectionRevision();
    const isCurrent = () =>
      requestId === requestRef.current && revision === apiClient.getConnectionRevision();
    setLoading(true);
    setError('');
    try {
      // Newest first with a limit keeps the recent requests of a very long session.
      const page = await requestLogApi.list({
        session: sessionId,
        limit: TIMELINE_LIMIT,
        order: 'desc',
      });
      if (!isCurrent()) return;
      setRows([...page.entries].sort((a, b) => a.seq - b.seq));
      setTruncated(page.more);
    } catch (err: unknown) {
      if (isCurrent()) setError(errorText(err));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [connected, sessionId]);

  useEffect(() => {
    setRows([]);
    setTruncated(false);
    if (!sessionId) return;
    void load();
    return () => {
      requestRef.current += 1;
    };
  }, [load, sessionId]);

  return { rows, truncated, loading, error, reload: () => void load() };
}
