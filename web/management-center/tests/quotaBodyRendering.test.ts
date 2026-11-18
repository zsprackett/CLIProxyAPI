/**
 * Provider bodies rendered end-to-end.
 *
 * Bodies receive their class map as a prop and import no stylesheet, so unlike
 * QuotaCard they can be rendered directly here — which is the only place the
 * "absolute plus countdown" pairing is checked as actual markup rather than as
 * a formatter's return value.
 */

import { beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { KimiQuotaBody } from '@/features/quota/providers/kimi/KimiQuotaBody';
import { AntigravityQuotaBody } from '@/features/quota/providers/antigravity/AntigravityQuotaBody';
import { PluginQuotaBody } from '@/features/quota/providers/plugin/PluginQuotaBody';
import { QUOTA_CLASS_KEYS, bindQuotaClasses } from '@/features/quota/types';
import { formatInstantShort } from '@/utils/quota';
import { HOUR_MS } from '@/utils/time/durations';
import type { AntigravityQuotaState, KimiQuotaState, PluginQuotaState } from '@/types';

const classes = bindQuotaClasses(
  Object.fromEntries(QUOTA_CLASS_KEYS.map((key) => [key, key])),
  'test-host'
);

/**
 * useNow() freezes to module-load time under renderToStaticMarkup (it reads
 * getServerSnapshot), so instants are placed relative to the real clock.
 */
const now = Date.now();

// The i18n fallback is zh-CN; pin English so the countdown assertions read.
beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('KimiQuotaBody', () => {
  test('renders the concrete reset time alongside its countdown', () => {
    const resetAtMs = now + 3 * HOUR_MS;
    const quota: KimiQuotaState = {
      status: 'success',
      rows: [
        {
          id: 'summary',
          label: 'Weekly limit',
          used: 34,
          limit: 100,
          resetHint: '3h',
          resetAtMs,
          periodHours: 168,
        },
      ],
    };
    const markup = renderToStaticMarkup(createElement(KimiQuotaBody, { quota, classes }));

    expect(markup).toContain(formatInstantShort(resetAtMs));
    expect(markup).toContain('quotaResetRelative');
    expect(markup).toMatch(/3 hours/);
    expect(markup).not.toContain('resets in 3h');
  });
});

describe('quota group notes', () => {
  const bucket = { id: 'b1', label: 'Daily', remainingFraction: 0.6 };

  test('Antigravity puts "models in this group" behind an InfoTip after the group title', () => {
    const quota: AntigravityQuotaState = {
      status: 'success',
      groups: [
        {
          id: 'g1',
          label: 'Custom group',
          description: 'Models within this group: model-a, model-b',
          buckets: [bucket],
        },
        { id: 'g2', label: 'Bare group', buckets: [{ ...bucket, id: 'b2' }] },
      ],
    };
    const markup = renderToStaticMarkup(createElement(AntigravityQuotaBody, { quota, classes }));
    const note = i18n.t('antigravity_quota.group_models_description', {
      models: 'model-a, model-b',
    });
    // Title, then the InfoTip as its sibling; no visible description line under the title.
    expect(markup).toContain(
      '<span class="antigravityQuotaGroupTitle">Custom group</span><button type="button" data-info-tip=""'
    );
    expect(markup).toContain(
      `aria-label="${i18n.t('common.more_info_about', { label: 'Custom group' })}"`
    );
    expect(markup).toContain(`hidden="">${note}</span>`);
    expect(markup).not.toContain('antigravityQuotaGroupDescription');
    // A group without a description gets no icon.
    expect(markup.match(/data-info-tip=""/g)).toHaveLength(1);
  });

  test('plugin groups carry their description the same way', () => {
    const quota: PluginQuotaState = {
      status: 'success',
      summary: [],
      groups: [
        { id: 'g1', label: 'Credits', description: 'Shared by every model', buckets: [bucket] },
      ],
    };
    const markup = renderToStaticMarkup(createElement(PluginQuotaBody, { quota, classes }));
    expect(markup).toContain(
      '<span class="antigravityQuotaGroupTitle">Credits</span><button type="button" data-info-tip=""'
    );
    expect(markup).toContain('hidden="">Shared by every model</span>');
    expect(markup).not.toContain('antigravityQuotaGroupDescription');
  });
});
