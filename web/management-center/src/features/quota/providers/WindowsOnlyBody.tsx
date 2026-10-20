import { WindowList } from '../snapshot/WindowList';
import type { SnapshotBodyProps } from '../types';

/** xAI and Devin show their windows only until P3 gives them extras bodies. */
export function WindowsOnlyBody<K extends 'xai' | 'devin'>({
  snapshot,
  nowMs,
}: SnapshotBodyProps<K>) {
  const provider = snapshot.provider;
  if (!provider) return null;
  return (
    <WindowList provider={provider} snapshot={snapshot} windows={snapshot.windows} nowMs={nowMs} />
  );
}
