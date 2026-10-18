import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Skeleton } from '@/components/ui/Skeleton';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { RequestEntry, SessionSummary } from '@/services/api/requestLog';
import { FilterBar } from './FilterBar';
import { RequestTable } from './RequestTable';
import { SessionList } from './SessionList';
import { SessionTimeline } from './SessionTimeline';
import {
  DEFAULT_CONTROLS,
  isFiltered,
  shortId,
  type KnownOptions,
  type RequestControls,
} from './requestsModel';
import styles from './RequestsView.module.scss';

export type RequestsTab = 'requests' | 'sessions';

interface PanelState {
  loading: boolean;
  error: string;
  enabled: boolean;
  /** `disabled`, `home_mode` or `unavailable` when `enabled` is false. */
  reason: string | null;
}

export interface RequestsPanel extends PanelState {
  rows: RequestEntry[];
  more: boolean;
  loadingOlder: boolean;
  live: boolean;
}

export interface SessionsPanel extends PanelState {
  sessions: SessionSummary[];
}

export interface TimelinePanel {
  sessionId: string | null;
  /** Oldest first. */
  rows: RequestEntry[];
  loading: boolean;
  error: string;
  truncated: boolean;
}

export interface RequestsViewProps {
  tab: RequestsTab;
  controls: RequestControls;
  options: KnownOptions;
  /** Display label per client key hash. */
  labels: ReadonlyMap<string, string>;
  requests: RequestsPanel;
  sessions: SessionsPanel;
  timeline: TimelinePanel;
  onTabChange?: (tab: RequestsTab) => void;
  onControlsChange?: (controls: RequestControls) => void;
  onToggleLive?: (live: boolean) => void;
  onRefresh?: () => void;
  onLoadOlder?: () => void;
  /** Null returns from the timeline to the list. */
  onSelectSession?: (sessionId: string | null) => void;
}

const TABS: RequestsTab[] = ['requests', 'sessions'];

function Gate({
  state,
  hasData,
  onRetry,
  children,
}: {
  state: PanelState;
  hasData: boolean;
  onRetry?: () => void;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  if (state.error && !hasData) {
    return (
      <div className={styles.error} role="alert">
        <p>
          {t('request_log.error_title')}: {state.error}
        </p>
        <Button variant="secondary" size="sm" onClick={onRetry}>
          {t('request_log.retry')}
        </Button>
      </div>
    );
  }
  if (state.loading && !hasData) {
    return (
      <div className={styles.skeleton} aria-busy="true" aria-label={t('request_log.loading')}>
        {Array.from({ length: 6 }, (_, index) => (
          <Skeleton key={index} height={36} rounded={8} />
        ))}
      </div>
    );
  }
  if (!state.enabled) {
    const reason =
      state.reason === 'home_mode' || state.reason === 'unavailable' ? state.reason : 'disabled';
    return (
      <EmptyState
        title={t(`request_log.${reason}_title`)}
        description={t(`request_log.${reason}_desc`)}
      />
    );
  }
  return <>{children}</>;
}

export function RequestsView({
  tab,
  controls,
  options,
  labels,
  requests,
  sessions,
  timeline,
  onTabChange,
  onControlsChange,
  onToggleLive,
  onRefresh,
  onLoadOlder,
  onSelectSession,
}: RequestsViewProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const change = onControlsChange ?? (() => undefined);

  const filterBar = (
    <FilterBar
      controls={controls}
      options={options}
      labels={labels}
      onChange={change}
      scope={tab}
    />
  );

  const requestsPanel = (
    <Gate state={requests} hasData={requests.rows.length > 0} onRetry={onRefresh}>
      {filterBar}
      {requests.error ? (
        <div className={styles.error} role="alert">
          {t('request_log.error_title')}: {requests.error}
        </div>
      ) : null}
      {requests.rows.length ? (
        <>
          <RequestTable rows={requests.rows} labels={labels} onSelectSession={onSelectSession} />
          {requests.more ? (
            <div className={styles.more}>
              <Button
                variant="secondary"
                size="sm"
                loading={requests.loadingOlder}
                onClick={onLoadOlder}
              >
                {t('request_log.load_older')}
              </Button>
            </div>
          ) : null}
        </>
      ) : isFiltered(controls) ? (
        <EmptyState
          title={t('request_log.empty_filtered_title')}
          description={t('request_log.empty_filtered_desc')}
          action={
            <Button variant="secondary" size="sm" onClick={() => change(DEFAULT_CONTROLS)}>
              {t('request_log.clear_filters')}
            </Button>
          }
        />
      ) : (
        <EmptyState
          title={t('request_log.empty_title')}
          description={t('request_log.empty_desc')}
        />
      )}
    </Gate>
  );

  const timelinePanel = (
    <section className={styles.timeline} aria-label={t('request_log.timeline_title')}>
      <div className={styles.timelineHead}>
        <Button variant="ghost" size="sm" onClick={() => onSelectSession?.(null)}>
          {t('request_log.timeline_back')}
        </Button>
        <h3>
          {t('request_log.timeline_title')}{' '}
          <span className={styles.sessionId} title={timeline.sessionId ?? ''}>
            {shortId(timeline.sessionId ?? '')}
          </span>
        </h3>
      </div>
      <p className={styles.hint}>{t('request_log.timeline_desc')}</p>
      <Gate
        state={{ loading: timeline.loading, error: timeline.error, enabled: true, reason: null }}
        hasData={timeline.rows.length > 0}
        onRetry={onRefresh}
      >
        <SessionTimeline rows={timeline.rows} truncated={timeline.truncated} />
      </Gate>
    </section>
  );

  const sessionsPanel = timeline.sessionId ? (
    timelinePanel
  ) : (
    <Gate state={sessions} hasData={sessions.sessions.length > 0} onRetry={onRefresh}>
      {filterBar}
      {sessions.sessions.length ? (
        <SessionList
          sessions={sessions.sessions}
          labels={labels}
          onSelectSession={onSelectSession}
        />
      ) : (
        <EmptyState
          title={t('request_log.sessions_empty_title')}
          description={t('request_log.sessions_empty_desc')}
        />
      )}
    </Gate>
  );

  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <div>
          <h1 id={titleId} className={styles.title}>
            {t('request_log.title')}
          </h1>
          <p className={styles.subtitle}>{t('request_log.subtitle')}</p>
        </div>
        <div className={styles.actions}>
          {tab === 'requests' ? (
            <ToggleSwitch
              checked={requests.live}
              onChange={(live) => onToggleLive?.(live)}
              label={t('request_log.live_tail')}
              ariaLabel={t('request_log.live_tail_hint')}
            />
          ) : null}
          <Button variant="secondary" size="sm" onClick={onRefresh}>
            {t('request_log.refresh')}
          </Button>
        </div>
      </header>

      <div className={styles.tabs} role="group" aria-labelledby={titleId}>
        {TABS.map((id) => (
          <button
            key={id}
            type="button"
            aria-pressed={tab === id}
            onClick={() => onTabChange?.(id)}
          >
            {t(`request_log.tab_${id}`)}
          </button>
        ))}
      </div>

      <div className={styles.panel}>{tab === 'requests' ? requestsPanel : sessionsPanel}</div>
    </div>
  );
}
