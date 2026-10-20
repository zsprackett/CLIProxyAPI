/**
 * Codex quota body: plan chip (elite = Pro 20x liquid platinum, premium = gold),
 * renewal, credits, the manual reset credits, then the snapshot windows.
 */

import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  normalizePlanType,
  resolvePlanTier,
  PREMIUM_CODEX_PLAN_TYPES,
  buildResetDisplay,
  formatInstantShort,
  parseIsoToMs,
} from '@/utils/quota';
import { resolveTimeZoneLabel } from '@/utils/time/timezone';
import { QuotaResetLabel } from '../../components/QuotaResetLabel';
import { collectQuotaRowInstants, pickUrgentRowId, resetCreditRowId } from '../../resetSchedule';
import { WindowList } from '../../snapshot/WindowList';
import type { QuotaClassMap, SnapshotBodyProps } from '../../types';

const getPlanValueClass = (planType: string | null, classes: QuotaClassMap): string => {
  // tests/quotaPlanTier.test.ts pins the elite/premium order of resolvePlanTier.
  const tier = resolvePlanTier(planType);
  if (tier === 'elite') return classes.elitePlanValue;
  if (tier === 'premium') return classes.premiumPlanValue;
  return classes.codexPlanValue;
};

export function CodexQuotaBody({ snapshot, extras, classes, nowMs }: SnapshotBodyProps<'codex'>) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  // Windows and reset credits compete for the same emphasis, but only during
  // the final hour before the reset or expiry.
  const soonestRowId = useMemo(
    () => pickUrgentRowId(collectQuotaRowInstants('codex', undefined, snapshot), nowMs),
    [snapshot, nowMs]
  );
  const planType = extras.plan?.value ?? null;
  const activeUntil = extras.subscription?.value?.active_until ?? null;
  const credits = extras.credits?.value ?? null;
  const creditsUnlimited = credits?.unlimited === true;
  const creditBalance = credits?.balance ?? null;
  const resetCredits = extras.reset_credits;
  const resetCreditsCount = resetCredits?.value?.available ?? null;
  const resetCreditList = resetCredits?.value?.credits ?? [];
  const resetCreditsError = resetCredits?.status === 'failed' ? (resetCredits.error ?? '') : '';

  const getPlanLabel = (pt: string | null): string | null => {
    const normalized = normalizePlanType(pt);
    if (!normalized) return null;
    if (normalized === 'self_serve_business_prolite') {
      return t('codex_quota.plan_business_premium');
    }
    if (normalized === 'pro') return t('codex_quota.plan_pro');
    if (PREMIUM_CODEX_PLAN_TYPES.has(normalized)) return t('codex_quota.plan_prolite');
    if (normalized === 'plus') return t('codex_quota.plan_plus');
    if (normalized === 'team') return t('codex_quota.plan_team');
    if (normalized === 'free') return t('codex_quota.plan_free');
    return pt || normalized;
  };

  const planLabel = getPlanLabel(planType);
  const planValueClass = getPlanValueClass(planType, classes);
  const activeUntilMs = parseIsoToMs(activeUntil);
  const expiryDisplay = activeUntil
    ? buildResetDisplay(activeUntilMs === null ? activeUntil : null, activeUntilMs, nowMs, locale)
    : null;

  return (
    <>
      {(planLabel ||
        expiryDisplay ||
        creditsUnlimited ||
        creditBalance !== null ||
        resetCreditsCount !== null) && (
        <div className={classes.codexPlan}>
          {planLabel && (
            <span className={classes.codexPlanItem}>
              <span className={classes.codexPlanLabel}>{t('codex_quota.plan_label')}</span>
              <span className={planValueClass}>{planLabel}</span>
            </span>
          )}
          {expiryDisplay && (
            <span className={classes.codexPlanItem}>
              <span className={classes.codexPlanLabel}>{t('codex_quota.expires_label')}</span>
              <span className={classes.codexPlanValue}>{expiryDisplay.absolute}</span>
              {expiryDisplay.relative && (
                <span className={classes.quotaResetRelative}>{expiryDisplay.relative}</span>
              )}
            </span>
          )}
          {(creditsUnlimited || creditBalance !== null) && (
            <span className={classes.codexPlanItem}>
              <span className={classes.codexPlanLabel}>
                {t('codex_quota.credit_balance_label')}
              </span>
              <span className={classes.codexPlanValue}>
                {creditsUnlimited ? t('codex_quota.credit_unlimited') : creditBalance}
              </span>
            </span>
          )}
          {resetCreditsCount !== null && (
            <span className={classes.codexPlanItem}>
              <span className={classes.codexPlanLabel}>{t('codex_quota.reset_credits_label')}</span>
              <span className={classes.codexPlanValue}>{resetCreditsCount.toString()}</span>
            </span>
          )}
        </div>
      )}
      {resetCreditList.length > 0 ? (
        <div className={classes.codexResetCredits}>
          <div className={classes.codexResetCreditsTitle}>
            {t('codex_quota.reset_credits_expiry_label', { timezone: resolveTimeZoneLabel() })}
          </div>
          {resetCreditList.map((credit, index) => {
            const expiresAtMs = parseIsoToMs(credit.expires_at);
            const expiresDisplay = buildResetDisplay(
              expiresAtMs === null ? credit.expires_at : formatInstantShort(expiresAtMs),
              expiresAtMs,
              nowMs,
              locale
            );
            // One expression for both the key and the highlight: two copies
            // that drift would emphasize the wrong row.
            const rowId = resetCreditRowId(credit, index);
            const soon = rowId === soonestRowId;
            return (
              <div
                key={rowId}
                className={
                  soon
                    ? `${classes.codexResetCreditRow} ${classes.codexResetCreditRowSoon}`
                    : classes.codexResetCreditRow
                }
                title={soon ? t('quota_management.soonest_row_hint') : undefined}
              >
                <span className={classes.codexResetCreditLabel}>
                  {t('codex_quota.reset_credit_number', { index: index + 1 })}
                </span>
                <span className={classes.codexResetCreditTime}>
                  {expiresDisplay && (
                    <QuotaResetLabel display={expiresDisplay} classes={classes} soon={soon} />
                  )}
                </span>
              </div>
            );
          })}
        </div>
      ) : resetCreditsError ? (
        <div className={classes.codexResetCreditsError}>
          {t('codex_quota.reset_credits_expiry_failed', { message: resetCreditsError })}
        </div>
      ) : null}
      {snapshot.windows.length === 0 ? (
        <div className={classes.quotaMessage}>{t('codex_quota.empty_windows')}</div>
      ) : (
        <WindowList provider="codex" snapshot={snapshot} windows={snapshot.windows} nowMs={nowMs} />
      )}
    </>
  );
}
