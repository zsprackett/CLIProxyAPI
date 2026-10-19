import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { CACHE_VERDICTS, type CacheVerdict } from '@/services/api/requestLog';
import {
  DEFAULT_CONTROLS,
  isFiltered,
  TIME_RANGES,
  verdictKey,
  type KnownOptions,
  type RequestControls,
} from './requestsModel';
import styles from './FilterBar.module.scss';

export interface FilterBarProps {
  controls: RequestControls;
  options: KnownOptions;
  labels: ReadonlyMap<string, string>;
  onChange: (next: RequestControls) => void;
  /** The sessions list has no session, status or verdict filter. */
  scope?: 'requests' | 'sessions';
}

const STATUS_VALUES = ['ok', 'failed'] as const;

export function FilterBar({
  controls,
  options,
  labels,
  onChange,
  scope = 'requests',
}: FilterBarProps) {
  const { t } = useTranslation();
  const set = <K extends keyof RequestControls>(name: K, value: RequestControls[K]) =>
    onChange({ ...controls, [name]: value });

  const withAny = (items: { value: string; label: string }[]) => [
    { value: '', label: t('request_log.filter_any') },
    ...items,
  ];
  const keyed = (values: string[], current: string) =>
    Array.from(new Set(current && !values.includes(current) ? [...values, current] : values));

  const toggleVerdict = (verdict: CacheVerdict) =>
    set(
      'verdicts',
      controls.verdicts.includes(verdict)
        ? controls.verdicts.filter((item) => item !== verdict)
        : [...controls.verdicts, verdict]
    );

  return (
    <div className={styles.bar} role="search">
      {scope === 'requests' ? (
        <div className={styles.field}>
          <span className={styles.label}>{t('request_log.filter_session')}</span>
          <Input
            aria-label={t('request_log.filter_session')}
            className={styles.sessionInput}
            value={controls.session}
            onChange={(event) => set('session', event.target.value)}
            spellCheck={false}
          />
        </div>
      ) : null}
      <div className={styles.field}>
        <span className={styles.label}>{t('request_log.filter_key')}</span>
        <Select
          size="sm"
          fullWidth
          ariaLabel={t('request_log.filter_key')}
          value={controls.key}
          onChange={(value) => set('key', value)}
          options={withAny(
            keyed(options.keys, controls.key).map((hash) => ({
              value: hash,
              label: labels.get(hash) || hash,
            }))
          )}
        />
      </div>
      <div className={styles.field}>
        <span className={styles.label}>{t('request_log.filter_credential')}</span>
        <Select
          size="sm"
          fullWidth
          ariaLabel={t('request_log.filter_credential')}
          value={controls.auth}
          onChange={(value) => set('auth', value)}
          options={withAny(
            keyed(options.auths, controls.auth).map((auth) => ({ value: auth, label: auth }))
          )}
        />
      </div>
      <div className={styles.field}>
        <span className={styles.label}>{t('request_log.filter_model')}</span>
        <Select
          size="sm"
          fullWidth
          ariaLabel={t('request_log.filter_model')}
          value={controls.model}
          onChange={(value) => set('model', value)}
          options={withAny(
            keyed(options.models, controls.model).map((model) => ({ value: model, label: model }))
          )}
        />
      </div>
      {scope === 'requests' ? (
        <div className={styles.field}>
          <span className={styles.label}>{t('request_log.filter_status')}</span>
          <Select
            size="sm"
            fullWidth
            ariaLabel={t('request_log.filter_status')}
            value={controls.status}
            onChange={(value) => set('status', value)}
            options={withAny(
              STATUS_VALUES.map((value) => ({ value, label: t(`request_log.status_${value}`) }))
            )}
          />
        </div>
      ) : null}
      <div className={styles.field}>
        <span className={styles.label}>{t('request_log.filter_range')}</span>
        <Select
          size="sm"
          fullWidth
          ariaLabel={t('request_log.filter_range')}
          value={controls.range}
          onChange={(value) => set('range', value as RequestControls['range'])}
          options={TIME_RANGES.map((range) => ({
            value: range,
            label: t(`request_log.range_${range}`),
          }))}
        />
      </div>
      {scope === 'requests' ? (
        <div className={`${styles.field} ${styles.verdicts}`}>
          <span className={styles.label} id="request-log-verdict-label">
            {t('request_log.filter_verdict')}
          </span>
          <div className={styles.chips} role="group" aria-labelledby="request-log-verdict-label">
            {CACHE_VERDICTS.map((verdict) => (
              <button
                key={verdict}
                type="button"
                className={styles.chip}
                data-verdict={verdict}
                aria-pressed={controls.verdicts.includes(verdict)}
                onClick={() => toggleVerdict(verdict)}
              >
                {t(verdictKey(verdict))}
              </button>
            ))}
          </div>
        </div>
      ) : null}
      {isFiltered(controls) ? (
        <Button variant="ghost" size="sm" onClick={() => onChange(DEFAULT_CONTROLS)}>
          {t('request_log.clear_filters')}
        </Button>
      ) : null}
    </div>
  );
}
