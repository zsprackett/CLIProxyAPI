import { useCallback, useEffect, useMemo, useState } from 'react';
import { readApiKeyNames } from '@/features/config/apiKeyNames';
import { buildClientLabels } from '@/features/requests/requestsModel';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useAuthStore, useConfigStore } from '@/stores';
import { downloadBlob } from '@/utils/download';
import { UsageView } from './UsageView';
import { buildUsageCsv, usageCsvFilename } from './usageCsv';
import { DEFAULT_USAGE_CONTROLS, controlsToQuery, coversToday, type UsageControls } from './usageModel';
import { useUsagePrices, useUsageReport } from './useUsageReport';

/**
 * #/usage. Container: owns controls state, the two hooks, client labels and the
 * CSV side effect. Everything rendered is in the prop-driven UsageView.
 *
 * Routing: router/MainRoutes.tsx { path: '/usage', element: <UsagePage /> };
 * MainLayout navGroups 'observe': { path: '/usage', labelKey: 'nav.usage',
 * metaKey: 'nav_meta.usage', icon: sidebarIcons.usage } between quota and requests.
 */
export function UsagePage() {
  const [controls, setControls] = useState<UsageControls>(DEFAULT_USAGE_CONTROLS);
  const [pricesOpen, setPricesOpen] = useState(false);

  const tz = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', []);
  // today in tz as YYYY-MM-DD via Intl.DateTimeFormat('en-CA', { timeZone: tz }); recomputed per render is fine
  const today = '';

  const query = useMemo(() => controlsToQuery(controls, tz, today), [controls, tz, today]);
  const { report, loading, error, reload } = useUsageReport(query, true);
  const prices = useUsagePrices(pricesOpen);

  const apiBase = useAuthStore((state) => state.apiBase);
  const apiKeys = useConfigStore((state) => state.config?.apiKeys);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const apiKeyNames = useMemo(() => readApiKeyNames(apiBase), [apiBase]);
  useEffect(() => {
    void fetchConfig().catch(() => undefined);
  }, [fetchConfig]);

  const clientLabels = useMemo(
    () =>
      buildClientLabels({
        hashes: report?.options.key ?? [],
        keys: report?.keys ?? {},
        apiKeys,
        names: apiKeyNames,
        apiBase,
      }),
    [apiBase, apiKeyNames, apiKeys, report]
  );

  const exportCsv = useCallback(() => {
    if (!report?.enabled) return;
    const csv = buildUsageCsv(report, {
      dimension: report.query.groupBy,
      label: (key) => clientLabels.get(key) ?? key,
    });
    downloadBlob({
      filename: usageCsvFilename(report.query, controls.filter),
      blob: new Blob([csv], { type: 'text/csv;charset=utf-8' }),
    });
  }, [clientLabels, controls.filter, report]);

  useHeaderRefresh(reload);
  void coversToday;

  return (
    <UsageView
      controls={controls}
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
