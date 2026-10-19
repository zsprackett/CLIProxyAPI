/**
 * i18next configuration
 */

import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhCN from './locales/zh-CN.json';
import zhTW from './locales/zh-TW.json';
import en from './locales/en.json';
import ru from './locales/ru.json';
import { getInitialLanguage } from '@/utils/language';

i18n.use(initReactI18next).init({
  resources: {
    'zh-CN': { translation: zhCN },
    'zh-TW': { translation: zhTW },
    en: { translation: en },
    ru: { translation: ru },
  },
  lng: getInitialLanguage(),
  fallbackLng: 'zh-CN',
  interpolation: {
    escapeValue: false, // React already escapes
  },
  react: {
    useSuspense: false,
  },
});

const PERIOD_UNITS = [
  [86400, 'day'],
  [3600, 'hour'],
  [60, 'minute'],
] as const;

/** `{{seconds, period}}`: a window length in whole days, hours, or minutes. */
i18n.services.formatter?.add('period', (value: unknown, lng: string | undefined) => {
  const seconds = Number(value);
  const [size, unit] =
    PERIOD_UNITS.find(([unitSeconds]) => seconds % unitSeconds === 0) ?? PERIOD_UNITS[2];
  return new Intl.NumberFormat(lng, { style: 'unit', unit, unitDisplay: 'long' }).format(
    Math.round(seconds / size)
  );
});

export default i18n;
