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
import { QUOTA_CLASS_KEYS, bindQuotaClasses } from '@/features/quota/types';
import { formatInstantShort } from '@/utils/quota';
import { HOUR_MS } from '@/utils/time/durations';
import type { KimiQuotaState } from '@/types';

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
