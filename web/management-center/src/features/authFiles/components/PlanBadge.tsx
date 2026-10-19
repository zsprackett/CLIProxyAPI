import { useTranslation } from 'react-i18next';
import styles from './PlanBadge.module.scss';

export type PlanBadgeProps = {
  /** Already-translated plan name, e.g. "Max". */
  label: string;
};

/** Subscription plan pill shared by the auth file cards and the quota ledger. */
export function PlanBadge({ label }: PlanBadgeProps) {
  const { t } = useTranslation();
  return (
    <span className={styles.badge} title={t('auth_files.plan_badge', { plan: label })}>
      {label}
    </span>
  );
}
