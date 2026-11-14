/**
 * Claude quota body: plan and extra-usage chips, host slots, then the snapshot windows.
 */

import { useTranslation } from 'react-i18next';
import { planValueClassForTier, resolveClaudeCardTier } from '../../cardTier';
import { WindowList } from '../../snapshot/WindowList';
import type { SnapshotBodyProps } from '../../types';

export function ClaudeQuotaBody({
  snapshot,
  extras,
  classes,
  nowMs,
  planItems,
  afterPlan,
}: SnapshotBodyProps<'claude'>) {
  const { t } = useTranslation();
  const plan = extras.plan?.value ?? null;
  const extraUsage = extras.extra_usage?.value ?? null;

  return (
    <>
      {(plan || planItems) && (
        <div className={classes.codexPlan}>
          {plan && (
            <span className={classes.codexPlanItem} data-quota-plan="">
              <span className={classes.codexPlanLabel}>{t('claude_quota.plan_label')}</span>
              <span className={planValueClassForTier(resolveClaudeCardTier(plan), classes)}>
                {t(`claude_quota.${plan}`)}
              </span>
            </span>
          )}
          {planItems}
        </div>
      )}
      {afterPlan}
      {extraUsage?.enabled && (
        <div className={classes.codexPlan}>
          <span className={classes.codexPlanLabel}>{t('claude_quota.extra_usage_label')}</span>
          <span className={classes.codexPlanValue}>
            {`$${(extraUsage.used_credits / 100).toFixed(2)} / $${(extraUsage.monthly_limit / 100).toFixed(2)}`}
          </span>
        </div>
      )}
      {snapshot.windows.length === 0 ? (
        <div className={classes.quotaMessage}>{t('claude_quota.empty_windows')}</div>
      ) : (
        <WindowList
          provider="claude"
          snapshot={snapshot}
          windows={snapshot.windows}
          nowMs={nowMs}
        />
      )}
    </>
  );
}
