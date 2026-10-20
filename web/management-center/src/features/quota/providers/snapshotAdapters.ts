/**
 * The body of each snapshot provider kind. A kind without a body fails to compile.
 */

import type { ComponentType } from 'react';
import type { QuotaExtras } from '../snapshot/types';
import type { SnapshotBodyProps } from '../types';
import { ClaudeQuotaBody } from './claude/ClaudeQuotaBody';
import { CodexQuotaBody } from './codex/CodexQuotaBody';
import { WindowsOnlyBody } from './WindowsOnlyBody';

type QuotaKind = QuotaExtras['kind'];

export interface SnapshotAdapter<K extends QuotaKind> {
  Body: ComponentType<SnapshotBodyProps<K>>;
}

export const SNAPSHOT_ADAPTERS: { [K in QuotaKind]: SnapshotAdapter<K> } = {
  claude: { Body: ClaudeQuotaBody },
  codex: { Body: CodexQuotaBody },
  xai: { Body: WindowsOnlyBody },
  devin: { Body: WindowsOnlyBody },
};
