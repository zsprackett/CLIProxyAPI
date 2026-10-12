import { useMemo } from 'react';
import { readApiKeyNames } from '@/features/config/apiKeyNames';
import type { QuotaForecastProvider } from '@/services/api/quotaForecast';
import { useAuthStore, useConfigStore } from '@/stores';
import type { ResolvedTheme } from '@/types';
import { QuotaForecastView } from './QuotaForecastView';
import { useQuotaForecast } from './useQuotaForecast';

export interface QuotaForecastProps {
  provider: QuotaForecastProvider | null;
  nowMs: number;
  showIdentities: boolean;
  resolvedTheme: ResolvedTheme;
}

/** Pooled quota trend, runway and attribution for the selected quota tab. */
export function QuotaForecast({
  provider,
  nowMs,
  showIdentities,
  resolvedTheme,
}: QuotaForecastProps) {
  const { data, loading, error } = useQuotaForecast(provider);
  const apiBase = useAuthStore((state) => state.apiBase);
  const apiKeys = useConfigStore((state) => state.config?.apiKeys);
  const apiKeyNames = useMemo(() => readApiKeyNames(apiBase), [apiBase]);

  return (
    <QuotaForecastView
      provider={provider}
      data={data}
      loading={loading}
      error={error}
      nowMs={nowMs}
      showIdentities={showIdentities}
      resolvedTheme={resolvedTheme}
      apiKeys={apiKeys}
      apiKeyNames={apiKeyNames}
      apiBase={apiBase}
    />
  );
}
