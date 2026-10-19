import { QUOTA_PROGRESS_HIGH_THRESHOLD, QUOTA_PROGRESS_MEDIUM_THRESHOLD } from './QuotaMeter';

export type RemainingTone = 'toneHigh' | 'toneMedium' | 'toneLow' | 'toneUnknown';

/** Same thresholds as QuotaMeter, so a percentage reads the same color everywhere. */
export function remainingTone(remaining: number | null): RemainingTone {
  if (remaining === null) return 'toneUnknown';
  if (remaining >= QUOTA_PROGRESS_HIGH_THRESHOLD) return 'toneHigh';
  if (remaining >= QUOTA_PROGRESS_MEDIUM_THRESHOLD) return 'toneMedium';
  return 'toneLow';
}
