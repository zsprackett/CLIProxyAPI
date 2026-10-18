import { useCallback, useEffect, useMemo, useState } from 'react';
import { readApiKeyNames } from '@/features/config/apiKeyNames';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useAuthStore, useConfigStore } from '@/stores';
import { RequestsView, type RequestsTab } from './RequestsView';
import { DEFAULT_CONTROLS, buildClientLabels, type RequestControls } from './requestsModel';
import { useRequestFeed } from './useRequestFeed';
import { useSessionList, useSessionTimeline } from './useSessions';

/** Per-request history and prompt-cache diagnosis. */
export function RequestsPage() {
  const [tab, setTab] = useState<RequestsTab>('requests');
  const [controls, setControls] = useState<RequestControls>(DEFAULT_CONTROLS);
  const [sessionId, setSessionId] = useState<string | null>(null);

  const apiBase = useAuthStore((state) => state.apiBase);
  const apiKeys = useConfigStore((state) => state.config?.apiKeys);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const apiKeyNames = useMemo(() => readApiKeyNames(apiBase), [apiBase]);

  // Saved key names come from the configured keys; the store caches and dedupes this read.
  useEffect(() => {
    void fetchConfig().catch(() => undefined);
  }, [fetchConfig]);

  const feed = useRequestFeed(controls, tab === 'requests');
  const sessions = useSessionList(controls, tab === 'sessions' && sessionId === null);
  const timeline = useSessionTimeline(tab === 'sessions' ? sessionId : null);

  const hashes = useMemo(() => {
    const found = new Set<string>(feed.options.keys);
    for (const session of sessions.sessions) found.add(session.keyHash);
    for (const row of feed.feed.rows) found.add(row.keyHash);
    return [...found];
  }, [feed.feed.rows, feed.options.keys, sessions.sessions]);

  const labels = useMemo(
    () =>
      buildClientLabels({
        hashes,
        keys: { ...sessions.keys, ...feed.keys },
        apiKeys,
        names: apiKeyNames,
        apiBase,
      }),
    [apiBase, apiKeyNames, apiKeys, feed.keys, hashes, sessions.keys]
  );

  const selectSession = useCallback((id: string | null) => {
    setSessionId(id);
    if (id) setTab('sessions');
  }, []);

  const refresh = useCallback(() => {
    if (tab === 'requests') feed.reload();
    else if (sessionId) timeline.reload();
    else sessions.reload();
  }, [feed, sessionId, sessions, tab, timeline]);
  useHeaderRefresh(refresh);

  return (
    <RequestsView
      tab={tab}
      controls={controls}
      options={feed.options}
      labels={labels}
      requests={{
        loading: feed.loading,
        error: feed.error,
        enabled: feed.enabled,
        reason: feed.reason,
        rows: feed.feed.rows,
        more: feed.canLoadOlder,
        loadingOlder: feed.loadingOlder,
        live: feed.live,
      }}
      sessions={{
        loading: sessions.loading,
        error: sessions.error,
        enabled: sessions.enabled,
        reason: sessions.reason,
        sessions: sessions.sessions,
      }}
      timeline={{
        sessionId,
        rows: timeline.rows,
        loading: timeline.loading,
        error: timeline.error,
        truncated: timeline.truncated,
      }}
      onTabChange={setTab}
      onControlsChange={setControls}
      onToggleLive={feed.setLive}
      onRefresh={refresh}
      onLoadOlder={feed.loadOlder}
      onSelectSession={selectSession}
    />
  );
}
