import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import type { UsagePrices, UsageRatesPerMTok } from '@/services/api/usageHistory';
import styles from './UsageView.module.scss';

export interface UsagePricePanel {
  open: boolean;
  prices: UsagePrices | null;
  loading: boolean;
  error: string;
}

const RATE_COLUMNS = [
  ['input', 'col_input'],
  ['output', 'col_output'],
  ['reasoning', 'col_reasoning'],
  ['cacheRead', 'col_cache_read'],
  ['cacheWrite5m', 'col_cache_write_5m'],
  ['cacheWrite1h', 'col_cache_write_1h'],
] as const;

/** The wire marks derived rates by their snake_case field name. */
const WIRE_NAME: Record<(typeof RATE_COLUMNS)[number][0], string> = {
  input: 'input',
  output: 'output',
  reasoning: 'reasoning',
  cacheRead: 'cache_read',
  cacheWrite5m: 'cache_write_5m',
  cacheWrite1h: 'cache_write_1h',
};

export function UsagePriceTable({
  panel,
  locale,
  onToggle,
}: {
  panel: UsagePricePanel;
  locale: string;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const usd = (value: number) =>
    new Intl.NumberFormat(locale, {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: 2,
      maximumFractionDigits: 4,
    }).format(value);

  const rateCells = (rates: UsageRatesPerMTok) =>
    RATE_COLUMNS.map(([field]) => {
      const derived = rates.derived.includes(WIRE_NAME[field]) || rates.derived.includes(field);
      return (
        <TableCell key={field} alignRight>
          {usd(rates[field])}
          {derived ? (
            <span className={styles.partialMark} title={t('usage.prices_derived')}>
              *
            </span>
          ) : null}
        </TableCell>
      );
    });

  const models = panel.prices?.models ?? [];
  const hasDerived = models.some(
    (model) => (model.perMTok?.derived.length ?? 0) + (model.longContext?.derived.length ?? 0) > 0
  );

  return (
    <section className={styles.prices}>
      <Button variant="ghost" size="sm" onClick={onToggle} aria-expanded={panel.open}>
        {panel.open ? t('usage.prices_hide') : t('usage.prices_show')}
      </Button>
      {panel.open ? (
        panel.error ? (
          <div className={styles.error} role="alert">
            {t('usage.prices_error')}: {panel.error}
          </div>
        ) : panel.loading && !panel.prices ? (
          <p className={styles.muted}>{t('usage.prices_loading')}</p>
        ) : models.length === 0 ? (
          <p className={styles.muted}>{t('usage.prices_empty')}</p>
        ) : (
          <>
            <h3 className={styles.sectionTitle}>{t('usage.prices_title')}</h3>
            <Table aria-label={t('usage.prices_title')} className={styles.table}>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('usage.col_provider')}</TableHead>
                  <TableHead>{t('usage.col_model')}</TableHead>
                  <TableHead>{t('usage.col_source')}</TableHead>
                  {RATE_COLUMNS.map(([field, label]) => (
                    <TableHead key={field} alignRight>
                      {t(`usage.${label}`)}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {models.flatMap((model) => {
                  const id = `${model.provider}/${model.model}`;
                  const head = (
                    <>
                      <TableCell>{model.provider}</TableCell>
                      <TableCell className={styles.groupCell} title={model.matched || model.model}>
                        {model.model}
                      </TableCell>
                      <TableCell>
                        {model.source ? (
                          <span className={styles.badge}>{t(`usage.source_${model.source}`)}</span>
                        ) : null}
                      </TableCell>
                    </>
                  );
                  if (!model.priced || !model.perMTok) {
                    return [
                      <TableRow key={id}>
                        {head}
                        <TableCell colSpan={RATE_COLUMNS.length} className={styles.muted}>
                          {t('usage.prices_unpriced')}
                        </TableCell>
                      </TableRow>,
                    ];
                  }
                  const rows = [
                    <TableRow key={id}>
                      {head}
                      {rateCells(model.perMTok)}
                    </TableRow>,
                  ];
                  if (model.longContext) {
                    rows.push(
                      <TableRow key={`${id}:long`} className={styles.subRow}>
                        <TableCell />
                        <TableCell colSpan={2} className={styles.muted}>
                          {t('usage.prices_long_context')}
                        </TableCell>
                        {rateCells(model.longContext)}
                      </TableRow>
                    );
                  }
                  return rows;
                })}
              </TableBody>
            </Table>
            {hasDerived ? <p className={styles.muted}>* {t('usage.prices_derived')}</p> : null}
          </>
        )
      ) : null}
    </section>
  );
}
