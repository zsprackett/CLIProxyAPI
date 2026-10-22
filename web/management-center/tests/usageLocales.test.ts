import { expect, test } from 'bun:test';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import { RANGE_PRESETS, STACK_BYS, USAGE_METRICS } from '@/features/usage/usageModel';
import { USAGE_DIMENSIONS, USAGE_TOKEN_TYPES, USAGE_UNITS } from '@/services/api/usageHistory';

type Locale = {
  nav: Record<string, string>;
  nav_meta: Record<string, string>;
  usage: Record<string, string>;
};

const placeholders = (value: string) => (value.match(/\{\{\w+\}\}/g) ?? []).sort();

test('usage strings are translated in all four languages with the same placeholders', () => {
  const reference = (en as Locale).usage;
  const keys = Object.keys(reference).sort();
  expect(keys.length).toBeGreaterThan(0);
  for (const locale of [zhCN, zhTW, ru] as Locale[]) {
    expect(Object.keys(locale.usage).sort()).toEqual(keys);
    for (const key of keys) {
      expect(locale.usage[key].trim().length).toBeGreaterThan(0);
      expect(placeholders(locale.usage[key])).toEqual(placeholders(reference[key]));
    }
  }
  for (const locale of [en, zhCN, zhTW, ru] as Locale[]) {
    expect(locale.nav.usage.trim().length).toBeGreaterThan(0);
    expect(locale.nav_meta.usage.trim().length).toBeGreaterThan(0);
  }
});

test('translations are not English copies', () => {
  const reference = (en as Locale).usage;
  for (const locale of [zhCN, zhTW, ru] as Locale[]) {
    expect(locale.usage.title).not.toBe(reference.title);
    expect(locale.usage.footnote_estimate).not.toBe(reference.footnote_estimate);
    expect(locale.nav.usage).not.toBe((en as Locale).nav.usage);
  }
});

test('every key the page builds from a value exists', () => {
  const strings = (en as Locale).usage;
  const dynamic = [
    ...RANGE_PRESETS.map((preset) => `preset_${preset}`),
    ...USAGE_UNITS.map((unit) => `unit_${unit}`),
    ...USAGE_DIMENSIONS.map((dimension) => `dim_${dimension}`),
    ...USAGE_METRICS.map((metric) => `metric_${metric}`),
    ...STACK_BYS.map((stackBy) => `stack_${stackBy}`),
    ...USAGE_TOKEN_TYPES.map((type) => `token_${type}`),
    ...['disabled', 'home_mode', 'unavailable'].flatMap((reason) => [
      `${reason}_title`,
      `${reason}_desc`,
    ]),
    ...['builtin', 'remote', 'override'].map((source) => `source_${source}`),
  ];
  for (const key of dynamic) expect(strings[key]).toBeTruthy();
});
