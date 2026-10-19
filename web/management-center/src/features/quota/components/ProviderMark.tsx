import { useTranslation } from 'react-i18next';
import type { ResolvedTheme } from '@/types';
import {
  getAuthFileIcon,
  getThemeSurfaceIconBackground,
  getTypeLabel,
  isThemeSurfaceIconProvider,
} from '@/features/authFiles/constants';
import styles from './ProviderMark.module.scss';

export interface ProviderMarkProps {
  provider: string;
  resolvedTheme: ResolvedTheme;
}

/** Provider icon plus its name, as used by the ledger section and summary headers. */
export function ProviderMark({ provider, resolvedTheme }: ProviderMarkProps) {
  const { t } = useTranslation();
  const iconSrc = getAuthFileIcon(provider, resolvedTheme);
  const label = getTypeLabel(t, provider);

  return (
    <span className={styles.mark}>
      <span
        className={styles.iconWrap}
        aria-hidden="true"
        style={
          isThemeSurfaceIconProvider(provider)
            ? { background: getThemeSurfaceIconBackground(resolvedTheme) }
            : undefined
        }
      >
        {iconSrc ? (
          <img src={iconSrc} alt="" className={styles.icon} />
        ) : (
          <span className={styles.iconFallback}>{label.slice(0, 1).toUpperCase()}</span>
        )}
      </span>
      <span className={styles.label}>{label}</span>
    </span>
  );
}
