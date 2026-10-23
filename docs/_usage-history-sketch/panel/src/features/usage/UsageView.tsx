/**
 * Prop-driven Usage page. No hooks beyond useTranslation and local hover state;
 * tested with renderToStaticMarkup over fixture reports.
 *
 * Component tree (each a file in features/usage/ with a .module.scss beside it):
 *
 *   UsageView
 *   ├─ UsageDisabled            report.enabled === false: reason text
 *   │                           (disabled -> "Turn on observability.usage.usage-history.enabled";
 *   │                            home_mode -> "Not available in Home mode"; unavailable -> "Usage database unavailable, see logs")
 *   ├─ UsageControls            preset segmented control, custom from/to <input type=date>,
 *   │                           bucket select (allowedUnits), group-by select,
 *   │                           five filter selects (options from report.options, labels via groupLabel),
 *   │                           metric toggle cost|tokens|requests, stack toggle group|token type,
 *   │                           "Export CSV" button
 *   ├─ UsageSummary             tiles: estimated cost ("at least" when partial) with the
 *   │                           "API-price estimate" caption, total tokens, requests + failure rate,
 *   │                           cache read share, 1h share of cache writes + cache-write cost
 *   ├─ UsageChart               <svg> from buildUsageChartGeometry; legend from stacks.series;
 *   │                           hover column -> tooltip with the bucket's per-series values
 *   ├─ UsageGroupTable          components/ui/Table: one row per report.groups entry + total row;
 *   │                           columns requests, failures, avg latency, the seven token types,
 *   │                           cost, share of cost; "-" for null cost, a dagger for partial cost
 *   ├─ UsageFootnote            unpriced models list (with a hint to add a same-as override),
 *   │                           price source + fetched date, "clamped to retention" note,
 *   │                           "as of" time, recording-since date
 *   └─ UsagePriceTable          collapsed by default; effective price per model from
 *                               /observability/usage/prices with source badges and "derived" marks
 *
 * Locales: `usage.*` keys + nav.usage + nav_meta.usage in en, zh-CN, zh-TW, ru;
 * parity enforced by tests/usageLocales.test.ts.
 */

import type { UsagePrices, UsageReport } from '@/services/api/usageHistory';
import type { UsageControls } from './usageModel';

export interface UsageViewProps {
  controls: UsageControls;
  report: UsageReport | null;
  loading: boolean;
  error: string;
  /** Key hash -> display label (saved name, masked key, or #hash). */
  clientLabels: Map<string, string>;
  prices: { open: boolean; prices: UsagePrices | null; loading: boolean; error: string };
  onControlsChange: (next: UsageControls) => void;
  onExportCsv: () => void;
  onTogglePrices: () => void;
  onRefresh: () => void;
}

export function UsageView(props: UsageViewProps): JSX.Element {
  throw new Error('not implemented');
}

// Sub-component prop shapes (each file exports one component).

export interface UsageChartProps {
  report: UsageReport;
  metric: UsageControls['metric'];
  stackBy: UsageControls['stackBy'];
  seriesLabel: (seriesId: string) => string;
  /** Measured by the View with a ResizeObserver-free fixed viewBox (960x280), scaled by CSS. */
  width?: number;
  height?: number;
}

export interface UsageGroupTableProps {
  report: UsageReport;
  groupLabel: (key: string) => string;
  locale: string;
}

export interface UsageSummaryProps {
  report: UsageReport;
  locale: string;
}
