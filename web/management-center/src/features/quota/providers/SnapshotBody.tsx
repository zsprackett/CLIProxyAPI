import type { ComponentType, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { WindowList } from '../snapshot/WindowList';
import type { QuotaSnapshot } from '../snapshot/types';
import type { QuotaClassMap, SnapshotBodyProps } from '../types';
import { SNAPSHOT_ADAPTERS } from './snapshotAdapters';

export interface SnapshotBodyHostProps {
  snapshot: QuotaSnapshot;
  classes: QuotaClassMap;
  nowMs: number;
  /** Host-owned chips for the plan row, rendered for every snapshot of a known provider. */
  planItems?: ReactNode;
  /** Host-owned content after the plan row, rendered for every snapshot of a known provider. */
  afterPlan?: ReactNode;
}

/** A snapshot with the body of its kind, or its windows alone before the first extras. */
export function SnapshotBody({
  snapshot,
  classes,
  nowMs,
  planItems,
  afterPlan,
}: SnapshotBodyHostProps) {
  const { t } = useTranslation();
  const extras = snapshot.extras;
  const pollError = snapshot.last_poll?.error;
  let body = null;
  if (extras && extras.kind === snapshot.provider) {
    const Body = SNAPSHOT_ADAPTERS[extras.kind].Body as ComponentType<
      SnapshotBodyProps<typeof extras.kind>
    >;
    body = (
      <Body
        snapshot={snapshot}
        extras={extras}
        classes={classes}
        nowMs={nowMs}
        planItems={planItems}
        afterPlan={afterPlan}
      />
    );
  } else if (snapshot.provider) {
    body = (
      <>
        {planItems && <div className={classes.codexPlan}>{planItems}</div>}
        {afterPlan}
        <WindowList
          provider={snapshot.provider}
          snapshot={snapshot}
          windows={snapshot.windows}
          nowMs={nowMs}
        />
      </>
    );
  }
  return (
    <>
      {pollError && (
        <div className={classes.codexResetCreditsError} role="status">
          {t('quota_management.poll_failed', { message: pollError })}
        </div>
      )}
      {body}
    </>
  );
}
