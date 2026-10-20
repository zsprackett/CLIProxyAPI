/**
 * Formatting functions for quota display.
 */

import type { TFunction } from 'i18next';
import { formatInstantShort } from './relativeTime';

export function formatQuotaResetTime(value?: string): string {
  if (!value) return '-';
  const ms = new Date(value).getTime();
  if (Number.isNaN(ms)) return '-';
  return formatInstantShort(ms);
}

export function formatUnixSeconds(value: number | null): string {
  if (!value) return '-';
  const ms = value * 1000;
  if (Number.isNaN(ms)) return '-';
  return formatInstantShort(ms);
}

export function createStatusError(message: string, status?: number): Error & { status?: number } {
  const error = new Error(message) as Error & { status?: number };
  if (status !== undefined) {
    error.status = status;
  }
  return error;
}

export function getStatusFromError(err: unknown): number | undefined {
  if (typeof err === 'object' && err !== null && 'status' in err) {
    const rawStatus = (err as { status?: unknown }).status;
    if (typeof rawStatus === 'number' && Number.isFinite(rawStatus)) {
      return rawStatus;
    }
    const asNumber = Number(rawStatus);
    if (Number.isFinite(asNumber) && asNumber > 0) {
      return asNumber;
    }
  }
  return undefined;
}

export function formatKimiResetHint(t: TFunction, hint?: string): string {
  if (!hint) return '';
  return t('kimi_quota.reset_hint', { hint });
}
