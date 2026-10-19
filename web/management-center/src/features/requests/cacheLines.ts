import type { TFunction } from 'i18next';
import type { CacheDiagnosis } from '@/services/api/requestLog';
import { formatCompactNumber } from '@/utils/format';
import { causeKey, formatSpan, naReasonKey, segmentLabel } from './requestsModel';

export interface CacheLine {
  label: string;
  value: string;
}

const CONFIDENCE = new Set(['high', 'medium', 'low']);

/** The diagnosis as label/value pairs, shared by the badge tooltip and the expanded row. */
export function cacheLines(t: TFunction, cache: CacheDiagnosis): CacheLine[] {
  const lines: CacheLine[] = [];
  if (cache.verdict === 'na') {
    if (cache.reason) {
      lines.push({ label: t('request_log.detail_reason'), value: t(naReasonKey(cache.reason)) });
    }
    return lines;
  }
  if (cache.cause) {
    lines.push({ label: t('request_log.detail_cause'), value: t(causeKey(cache.cause)) });
  }
  if (cache.confidence) {
    lines.push({
      label: t('request_log.detail_confidence'),
      value: CONFIDENCE.has(cache.confidence)
        ? t(`request_log.confidence_${cache.confidence}`)
        : cache.confidence,
    });
  }
  if (cache.causes.length > 1) {
    lines.push({
      label: t('request_log.detail_all_causes'),
      value: cache.causes.map((cause) => t(causeKey(cause))).join(', '),
    });
  }
  const segment = segmentLabel(cache);
  if (segment) {
    lines.push({ label: t('request_log.detail_segment'), value: t(segment.key, segment.values) });
  }
  if (cache.expected) {
    lines.push({
      label: t('request_log.detail_expected'),
      value: formatCompactNumber(cache.expected),
    });
  }
  if (cache.lost) {
    lines.push({ label: t('request_log.detail_lost'), value: formatCompactNumber(cache.lost) });
  }
  if (cache.gapSec) {
    lines.push({
      label: t('request_log.detail_gap'),
      value: cache.ttlSec
        ? `${formatSpan(cache.gapSec)} / ${formatSpan(cache.ttlSec)}`
        : formatSpan(cache.gapSec),
    });
  }
  return lines;
}
