import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ForecastAttribution, ForecastAttributionRow } from '@/services/api/quotaForecast';
import { formatCompactNumber } from '@/utils/format';
import { formatAccounts, type ClientLabel } from './forecastModel';
import styles from './AttributionTable.module.scss';

const GROUPS = ['models', 'clients', 'agents'] as const;
export type AttributionGroup = (typeof GROUPS)[number];

export interface AttributionTableProps {
  attribution: ForecastAttribution;
  clientLabels: Map<string, ClientLabel>;
  initialGroup?: AttributionGroup;
}

/** Splits recorded burn across models, client keys and agents. Raw keys never render. */
export function AttributionTable({
  attribution,
  clientLabels,
  initialGroup = 'models',
}: AttributionTableProps) {
  const { t } = useTranslation();
  const [group, setGroup] = useState<AttributionGroup>(initialGroup);
  const rows = attribution[group];

  const rowLabel = (row: ForecastAttributionRow): string => {
    if (group === 'models') return row.model || t('quota_management.forecast_attr_unknown_model');
    if (group === 'agents') {
      return row.agent && row.agent !== '-'
        ? row.agent
        : t('quota_management.forecast_attr_unknown_agent');
    }
    const label = clientLabels.get(row.keyHash || '-');
    if (!label || label.kind === 'none') return t('quota_management.forecast_attr_no_key');
    return label.text;
  };

  return (
    <section className={styles.attribution} aria-label={t('quota_management.forecast_attr_title')}>
      <div className={styles.head}>
        <h3 className={styles.title}>{t('quota_management.forecast_attr_title')}</h3>
        <div
          className={styles.modes}
          role="group"
          aria-label={t('quota_management.forecast_attr_by_label')}
        >
          {GROUPS.map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={group === value}
              onClick={() => setGroup(value)}
            >
              {t(`quota_management.forecast_attr_${value}`)}
            </button>
          ))}
        </div>
      </div>

      {rows.length === 0 ? (
        <p className={styles.empty}>{t('quota_management.forecast_attr_empty')}</p>
      ) : (
        <div className={styles.scroller}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">{t(`quota_management.forecast_attr_${group}`)}</th>
                <th scope="col">{t('quota_management.forecast_attr_tokens')}</th>
                <th scope="col">{t('quota_management.forecast_attr_burn')}</th>
                <th scope="col">{t('quota_management.forecast_attr_rate')}</th>
                <th scope="col">{t('quota_management.forecast_attr_share')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => {
                const label = rowLabel(row);
                return (
                  <tr key={`${label}:${index}`}>
                    <th scope="row" title={label}>
                      {label}
                    </th>
                    <td>{formatCompactNumber(row.tokens)}</td>
                    <td>{formatAccounts(row.burnAccounts)}</td>
                    <td>{row.percentPerMTok.toFixed(2)}</td>
                    <td>
                      <span className={styles.share}>
                        <span className={styles.shareBar} aria-hidden="true">
                          <span
                            className={styles.shareFill}
                            style={{ width: `${row.share * 100}%` }}
                          />
                        </span>
                        {Math.round(row.share * 100)}%
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <p className={styles.footnote}>
        {t('quota_management.forecast_attr_footnote', {
          method: t(`quota_management.forecast_attr_method_${attribution.method}`),
          r2: attribution.r2 === null ? '—' : attribution.r2.toFixed(2),
          rows: attribution.rows,
        })}
      </p>
    </section>
  );
}
