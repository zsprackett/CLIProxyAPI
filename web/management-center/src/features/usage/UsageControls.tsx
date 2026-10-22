import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  USAGE_DIMENSIONS,
  USAGE_FILTER_DIMENSIONS,
  type UsageFilterDimension,
  type UsageReport,
} from '@/services/api/usageHistory';
import {
  RANGE_PRESETS,
  STACK_BYS,
  USAGE_METRICS,
  allowedUnits,
  applyPreset,
  controlsToQuery,
  spanDays,
  type UsageControls as Controls,
} from './usageModel';
import styles from './UsageView.module.scss';

export interface UsageControlsProps {
  controls: Controls;
  today: string;
  options: UsageReport['options'];
  /** Label of a filter value (key hashes resolve to saved names or masked keys). */
  optionLabel: (dimension: UsageFilterDimension, value: string) => string;
  onChange: (next: Controls) => void;
}

function Pills<T extends string>({
  label,
  values,
  value,
  text,
  disabled,
  onChange,
}: {
  label: string;
  values: readonly T[];
  value: T;
  text: (value: T) => string;
  disabled?: (value: T) => boolean;
  onChange: (value: T) => void;
}) {
  return (
    <div className={styles.field}>
      <span className={styles.label}>{label}</span>
      <div className={styles.pills} role="group" aria-label={label}>
        {values.map((item) => (
          <button
            key={item}
            type="button"
            aria-pressed={value === item}
            disabled={disabled?.(item)}
            onClick={() => onChange(item)}
          >
            {text(item)}
          </button>
        ))}
      </div>
    </div>
  );
}

export function UsageControls({
  controls,
  today,
  options,
  optionLabel,
  onChange,
}: UsageControlsProps) {
  const { t } = useTranslation();
  const effective = controlsToQuery(controls, '', today);
  const units = allowedUnits(spanDays(effective.from, effective.to));
  const set = <K extends keyof Controls>(name: K, value: Controls[K]) =>
    onChange({ ...controls, [name]: value });
  const setFilter = (dimension: UsageFilterDimension, value: string) =>
    onChange({ ...controls, filter: { ...controls.filter, [dimension]: value } });

  return (
    <div className={styles.controls} role="group" aria-label={t('usage.controls_label')}>
      <div className={styles.controlRow}>
        <Pills
          label={t('usage.range')}
          values={RANGE_PRESETS}
          value={controls.preset}
          text={(preset) => t(`usage.preset_${preset}`)}
          onChange={(preset) => onChange(applyPreset(controls, preset, today))}
        />
        {controls.preset === 'custom' ? (
          <>
            <div className={styles.field}>
              <span className={styles.label}>{t('usage.from')}</span>
              <Input
                type="date"
                aria-label={t('usage.from')}
                className={styles.dateInput}
                value={effective.from}
                max={today}
                onChange={(event) => set('from', event.target.value)}
              />
            </div>
            <div className={styles.field}>
              <span className={styles.label}>{t('usage.to')}</span>
              <Input
                type="date"
                aria-label={t('usage.to')}
                className={styles.dateInput}
                value={effective.to}
                max={today}
                onChange={(event) => set('to', event.target.value)}
              />
            </div>
          </>
        ) : null}
      </div>

      <div className={styles.controlRow}>
        <div className={styles.field}>
          <span className={styles.label}>{t('usage.bucket')}</span>
          <Select
            size="sm"
            fullWidth
            ariaLabel={t('usage.bucket')}
            value={effective.bucket}
            onChange={(value) => set('bucket', value as Controls['bucket'])}
            options={units.map((unit) => ({ value: unit, label: t(`usage.unit_${unit}`) }))}
          />
        </div>
        <div className={styles.field}>
          <span className={styles.label}>{t('usage.group_by')}</span>
          <Select
            size="sm"
            fullWidth
            ariaLabel={t('usage.group_by')}
            value={controls.groupBy}
            onChange={(value) => set('groupBy', value as Controls['groupBy'])}
            options={USAGE_DIMENSIONS.map((dimension) => ({
              value: dimension,
              label: t(`usage.dim_${dimension}`),
            }))}
          />
        </div>
        {USAGE_FILTER_DIMENSIONS.map((dimension) => {
          const current = controls.filter[dimension] ?? '';
          const values =
            current && !options[dimension].includes(current)
              ? [...options[dimension], current]
              : options[dimension];
          return (
            <div key={dimension} className={styles.field}>
              <span className={styles.label}>{t(`usage.dim_${dimension}`)}</span>
              <Select
                size="sm"
                fullWidth
                ariaLabel={t(`usage.dim_${dimension}`)}
                value={current}
                onChange={(value) => setFilter(dimension, value)}
                options={[
                  { value: '', label: t('usage.filter_any') },
                  ...values.map((value) => ({ value, label: optionLabel(dimension, value) })),
                ]}
              />
            </div>
          );
        })}
      </div>

      <div className={styles.controlRow}>
        <Pills
          label={t('usage.metric')}
          values={USAGE_METRICS}
          value={controls.metric}
          text={(metric) => t(`usage.metric_${metric}`)}
          onChange={(metric) => set('metric', metric)}
        />
        <Pills
          label={t('usage.stack_by')}
          values={STACK_BYS}
          value={controls.metric === 'requests' ? 'group' : controls.stackBy}
          text={(stackBy) => t(`usage.stack_${stackBy}`)}
          disabled={(stackBy) => stackBy === 'tokenType' && controls.metric === 'requests'}
          onChange={(stackBy) => set('stackBy', stackBy)}
        />
      </div>
    </div>
  );
}
