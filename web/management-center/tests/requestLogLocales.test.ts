import { expect, test } from 'bun:test';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import { CACHE_VERDICTS } from '@/services/api/requestLog';
import { causeKey, naReasonKey, verdictKey } from '@/features/requests/requestsModel';

type Locale = {
  nav: Record<string, string>;
  nav_meta: Record<string, string>;
  request_log: Record<string, string>;
};

const placeholders = (value: string) => (value.match(/\{\{\w+\}\}/g) ?? []).sort();

test('request log strings are translated in all four languages with the same placeholders', () => {
  const reference = (en as Locale).request_log;
  const keys = Object.keys(reference).sort();
  expect(keys.length).toBeGreaterThan(0);
  for (const locale of [zhCN, zhTW, ru] as Locale[]) {
    expect(Object.keys(locale.request_log).sort()).toEqual(keys);
    for (const key of keys) {
      expect(locale.request_log[key].trim().length).toBeGreaterThan(0);
      expect(placeholders(locale.request_log[key])).toEqual(placeholders(reference[key]));
    }
  }
  for (const locale of [en, zhCN, zhTW, ru] as Locale[]) {
    expect(locale.nav.requests.trim().length).toBeGreaterThan(0);
    expect(locale.nav_meta.requests.trim().length).toBeGreaterThan(0);
  }
});

test('every label key the model can produce exists', () => {
  const strings = (en as Locale).request_log;
  const own = (key: string) => strings[key.replace('request_log.', '')];
  for (const verdict of CACHE_VERDICTS) expect(own(verdictKey(verdict))).toBeTruthy();
  for (const cause of ['idle_gap', 'tools_changed', 'unexplained', 'nonsense']) {
    expect(own(causeKey(cause))).toBeTruthy();
  }
  for (const reason of ['failed', 'no_session', 'concurrent', '']) {
    expect(own(naReasonKey(reason))).toBeTruthy();
  }
});
