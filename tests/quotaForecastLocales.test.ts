import { expect, test } from 'bun:test';
import en from '@/i18n/locales/en.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import ru from '@/i18n/locales/ru.json';

const forecastEntries = (locale: { quota_management: Record<string, string> }) =>
  Object.entries(locale.quota_management).filter(([key]) => key.startsWith('forecast_'));

const placeholders = (value: string) => (value.match(/\{\{\w+\}\}/g) ?? []).sort();

test('quota forecast strings are translated in all four languages with the same placeholders', () => {
  const reference = Object.fromEntries(forecastEntries(en));
  const keys = Object.keys(reference).sort();
  expect(keys.length).toBeGreaterThan(0);
  for (const locale of [zhCN, zhTW, ru]) {
    const entries = Object.fromEntries(forecastEntries(locale));
    expect(Object.keys(entries).sort()).toEqual(keys);
    for (const key of keys) {
      expect(entries[key].trim().length).toBeGreaterThan(0);
      expect(placeholders(entries[key])).toEqual(placeholders(reference[key]));
    }
  }
});
