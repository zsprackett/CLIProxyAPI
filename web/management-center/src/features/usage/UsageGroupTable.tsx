import { useTranslation } from 'react-i18next';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import { formatLatency } from '@/features/requests/requestsModel';
import { USAGE_TOKEN_TYPES, type UsageCell, type UsageReport } from '@/services/api/usageHistory';
import { formatPercent, formatTokens, formatUsd, totalTokens } from './usageModel';
import styles from './UsageView.module.scss';

export interface UsageGroupTableProps {
  report: UsageReport;
  groupLabel: (key: string) => string;
  locale: string;
}

export function UsageGroupTable({ report, groupLabel, locale }: UsageGroupTableProps) {
  const { t } = useTranslation();
  const count = (value: number) => new Intl.NumberFormat(locale).format(value);
  const totalCost = report.totals.costUsd;

  const cells = (cell: UsageCell) => {
    const partial = cell.costUsd !== null && cell.unpricedTokens > 0;
    const share =
      cell.costUsd !== null && totalCost !== null && totalCost > 0
        ? formatPercent(cell.costUsd / totalCost, locale)
        : '-';
    return (
      <>
        <TableCell alignRight>{count(cell.requests)}</TableCell>
        <TableCell alignRight>{count(cell.failures)}</TableCell>
        <TableCell alignRight>{formatLatency(cell.latencyMs)}</TableCell>
        {USAGE_TOKEN_TYPES.map((type) => (
          <TableCell key={type} alignRight>
            {formatTokens(cell.tokens[type], locale)}
          </TableCell>
        ))}
        <TableCell alignRight>{formatTokens(totalTokens(cell), locale)}</TableCell>
        <TableCell alignRight>
          {formatUsd(cell.costUsd, locale)}
          {partial ? (
            <span className={styles.partialMark} title={t('usage.partial_cost')}>
              {'†'}
            </span>
          ) : null}
        </TableCell>
        <TableCell alignRight>{share}</TableCell>
      </>
    );
  };

  return (
    <Table aria-label={t('usage.table_label')} className={styles.table}>
      <TableHeader>
        <TableRow>
          <TableHead>{t(`usage.dim_${report.query.groupBy}`)}</TableHead>
          <TableHead alignRight>{t('usage.col_requests')}</TableHead>
          <TableHead alignRight>{t('usage.col_failures')}</TableHead>
          <TableHead alignRight>{t('usage.col_latency')}</TableHead>
          {USAGE_TOKEN_TYPES.map((type) => (
            <TableHead key={type} alignRight>
              {t(`usage.token_${type}`)}
            </TableHead>
          ))}
          <TableHead alignRight>{t('usage.col_total_tokens')}</TableHead>
          <TableHead alignRight>{t('usage.col_cost')}</TableHead>
          <TableHead alignRight>{t('usage.col_cost_share')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {report.groups.map((group) => (
          <TableRow key={group.key}>
            <TableCell className={styles.groupCell} title={groupLabel(group.key)}>
              {groupLabel(group.key)}
            </TableCell>
            {cells(group)}
          </TableRow>
        ))}
        <TableRow className={styles.totalRow}>
          <TableCell>{t('usage.total_row')}</TableCell>
          {cells(report.totals)}
        </TableRow>
      </TableBody>
    </Table>
  );
}
