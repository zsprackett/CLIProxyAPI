/**
 * The body of each snapshot provider kind. A kind without a body fails to compile.
 */

import type { ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import { WindowList } from '../snapshot/WindowList';
import type { QuotaExtras, QuotaSnapshot } from '../snapshot/types';
import type { QuotaClassMap, SnapshotBodyProps } from '../types';
import { ClaudeQuotaBody } from './claude/ClaudeQuotaBody';
import { CodexQuotaBody } from './codex/CodexQuotaBody';

type QuotaKind = QuotaExtras['kind'];

export interface SnapshotAdapter<K extends QuotaKind> {
  Body: ComponentType<SnapshotBodyProps<K>>;
}

/** xAI and Devin show their windows only until P3 gives them extras bodies. */
function WindowsOnlyBody<K extends 'xai' | 'devin'>({ snapshot, nowMs }: SnapshotBodyProps<K>) {
  const provider = snapshot.provider;
  if (!provider) return null;
  return (
    <WindowList provider={provider} snapshot={snapshot} windows={snapshot.windows} nowMs={nowMs} />
  );
}

export const SNAPSHOT_ADAPTERS: { [K in QuotaKind]: SnapshotAdapter<K> } = {
  claude: { Body: ClaudeQuotaBody },
  codex: { Body: CodexQuotaBody },
  xai: { Body: WindowsOnlyBody },
  devin: { Body: WindowsOnlyBody },
};

export interface SnapshotBodyHostProps {
  snapshot: QuotaSnapshot;
  classes: QuotaClassMap;
  nowMs: number;
}

/** A snapshot with the body of its kind, or its windows alone before the first extras. */
export function SnapshotBody({ snapshot, classes, nowMs }: SnapshotBodyHostProps) {
  const { t } = useTranslation();
  const extras = snapshot.extras;
  const pollError = snapshot.last_poll?.error;
  let body = null;
  if (extras && extras.kind === snapshot.provider) {
    const Body = SNAPSHOT_ADAPTERS[extras.kind].Body as ComponentType<
      SnapshotBodyProps<typeof extras.kind>
    >;
    body = <Body snapshot={snapshot} extras={extras} classes={classes} nowMs={nowMs} />;
  } else if (snapshot.provider) {
    body = (
      <WindowList
        provider={snapshot.provider}
        snapshot={snapshot}
        windows={snapshot.windows}
        nowMs={nowMs}
      />
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
