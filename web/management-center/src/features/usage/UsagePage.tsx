import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { readApiKeyNames } from '@/features/config/apiKeyNames';
import { buildClientLabels } from '@/features/requests/requestsModel';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useInterval } from '@/hooks/useInterval';
import { useAuthStore, useConfigStore } from '@/stores';
import { downloadBlob } from '@/utils/download';
import { UsageView } from './UsageView';
import { buildUsageCsv, usageCsvFilename } from './usageCsv';
import {
  DEFAULT_USAGE_CONTROLS,
  controlsToQuery,
  coversToday,
  groupLabel,
  todayIn,
  type UsageControls,
} from './usageModel';
import { useUsagePrices, useUsageReport } from './useUsageReport';

const browserZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

/**
 * #/usage. Container: owns the controls, the two loaders, client labels and the
 * CSV side effect. Everything rendered is in the prop-driven UsageView.
 */
export function UsagePage() {
  const { t } = useTranslation();
  const [controls, setControls] = useState<UsageControls>(DEFAULT_USAGE_CONTROLS);
  const [pricesOpen, setPricesOpen] = useState(false);
  const [tz] = useState(browserZone);
  const [today, setToday] = useState(() => todayIn(tz, Date.now()));

  // Presets roll over at local midnight while the page stays open.
  useInterval(() => {
    const next = todayIn(tz, Date.now());
    if (next !== today) setToday(next);
  }, 60_000);

  const query = useMemo(() => controlsToQuery(controls, tz, today), [controls, today, tz]);
  // The server only clamps `from` forward, so the requested range decides polling.
  const { report, loading, error, reload } = useUsageReport(query, coversToday(query, today));
  const prices = useUsagePrices(pricesOpen);

  const apiBase = useAuthStore((state) => state.apiBase);
  const apiKeys = useConfigStore((state) => state.config?.apiKeys);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const apiKeyNames = useMemo(() => readApiKeyNames(apiBase), [apiBase]);

  // Saved key names come from the configured keys; the store caches and dedupes this read.
  useEffect(() => {
    void fetchConfig().catch(() => undefined);
  }, [fetchConfig]);

  const clientLabels = useMemo(() => {
    const hashes = new Set(report?.options.key ?? []);
    if (report?.query.groupBy === 'key') for (const group of report.groups) hashes.add(group.key);
    return buildClientLabels({
      hashes: [...hashes],
      keys: report?.keys ?? {},
      apiKeys,
      names: apiKeyNames,
      apiBase,
    });
  }, [apiBase, apiKeyNames, apiKeys, report]);

  const exportCsv = useCallback(() => {
    if (!report?.enabled) return;
    const dimension = report.query.groupBy;
    const translate = (key: string) => t(key, { n: report.foldedGroups });
    const csv = buildUsageCsv(report, {
      dimension,
      label: (key) => groupLabel(dimension, key, clientLabels, translate),
    });
    downloadBlob({
      filename: usageCsvFilename(report.query, query.filter),
      blob: new Blob([csv], { type: 'text/csv;charset=utf-8' }),
    });
  }, [clientLabels, query.filter, report, t]);

  useHeaderRefresh(reload);

  return (
    <UsageView
      controls={controls}
      today={today}
      report={report}
      loading={loading}
      error={error}
      clientLabels={clientLabels}
      prices={{ open: pricesOpen, ...prices }}
      onControlsChange={setControls}
      onExportCsv={exportCsv}
      onTogglePrices={() => setPricesOpen((open) => !open)}
      onRefresh={reload}
    />
  );
}
