/**
 * One renderer for quota windows, for any provider: remaining bar and percent,
 * "unknown", "limit reached", "stale since", and the reset.
 */

import { useTranslation } from 'react-i18next';
import { formatInstantShort, formatRelativeInstant } from '@/utils/quota';
import { remainingTone } from '../components/ledgerTone';
import styles from '../components/QuotaLedger.module.scss';
import { snapshotLedgerWindows, type LedgerWindow } from '../ledgerModel';
import type { QuotaProviderKind, QuotaSnapshot, QuotaWindow } from './types';

const CLASS_KEYS = [
  'windows',
  'window',
  'windowHead',
  'windowLabel',
  'windowPercent',
  'windowLimit',
  'windowStale',
  'bar',
  'barFill',
  'toneHigh',
  'toneMedium',
  'toneLow',
  'toneUnknown',
  'windowReset',
  'resetRelative',
] as const;

export type WindowListClasses = Record<(typeof CLASS_KEYS)[number], string>;

const LEDGER_CLASSES = Object.fromEntries(
  CLASS_KEYS.map((key) => [key, styles[key] ?? key])
) as WindowListClasses;

export interface LedgerWindowListProps {
  windows: readonly LedgerWindow[];
  nowMs: number;
  classes?: Partial<WindowListClasses>;
}

/** Renders ledger windows, from a live reading or from a snapshot. */
export function LedgerWindowList({ windows, nowMs, classes }: LedgerWindowListProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const cls = { ...LEDGER_CLASSES, ...classes };

  const renderWindow = (window: LedgerWindow) => {
    const label = window.labelKey
      ? t(window.labelKey, window.labelParams)
      : (window.label ?? window.id);
    const known = window.remaining !== null;
    const resetAtMs =
      window.resetAtMs !== null && window.resetAtMs > nowMs ? window.resetAtMs : null;
    return (
      <div key={window.id} className={cls.window}>
        <div className={cls.windowHead}>
          <span className={cls.windowLabel}>{label}</span>
          <span className={cls.windowPercent}>
            {known
              ? `${Math.round(window.remaining as number)}%`
              : window.awaitingData
                ? t('quota_management.window_reset_awaiting')
                : t('quota_management.window_unknown')}
          </span>
        </div>
        {known && (
          <span className={cls.bar} aria-hidden="true">
            <span
              className={`${cls.barFill} ${cls[remainingTone(window.remaining)]}`}
              style={{ width: `${window.remaining}%` }}
            />
          </span>
        )}
        {window.limitReached && (
          <span className={cls.windowLimit}>{t('quota_management.window_limit_reached')}</span>
        )}
        {window.staleSinceMs != null && (
          <span className={cls.windowStale} title={t('quota_management.window_stale_hint')}>
            {t('quota_management.window_stale_since', {
              age: formatRelativeInstant(window.staleSinceMs, nowMs, locale),
            })}
          </span>
        )}
        <span className={cls.windowReset}>
          {resetAtMs === null ? (
            t('quota_management.ledger_no_reset')
          ) : (
            <>
              <span className={cls.resetRelative}>
                {formatRelativeInstant(resetAtMs, nowMs, locale)}
              </span>
              {' · '}
              {formatInstantShort(resetAtMs)}
            </>
          )}
        </span>
      </div>
    );
  };

  return <div className={cls.windows}>{windows.map(renderWindow)}</div>;
}

export interface WindowListProps {
  provider: QuotaProviderKind;
  snapshot: QuotaSnapshot;
  windows: readonly QuotaWindow[];
  nowMs: number;
  classes?: Partial<WindowListClasses>;
}

/** Renders windows of a backend quota snapshot. */
export function WindowList({ provider, snapshot, windows, nowMs, classes }: WindowListProps) {
  return (
    <LedgerWindowList
      windows={snapshotLedgerWindows(provider, snapshot, windows, nowMs)}
      nowMs={nowMs}
      classes={classes}
    />
  );
}
