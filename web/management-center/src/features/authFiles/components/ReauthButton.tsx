import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconKey } from '@/components/ui/icons';
import type { ReauthNeed } from '@/features/authFiles/reauth';

export interface ReauthButtonProps {
  need: NonNullable<ReauthNeed>;
  /** Credential identity as the card shows it. */
  name: string;
  disabled?: boolean;
  className?: string;
  onClick: () => void;
}

export function ReauthButton({ need, name, disabled, className, onClick }: ReauthButtonProps) {
  const { t } = useTranslation();
  return (
    <Button
      variant={need.level === 'required' ? 'primary' : 'secondary'}
      size="sm"
      className={className}
      disabled={disabled}
      onClick={onClick}
      aria-haspopup="dialog"
      aria-label={t('auth_reauth.button_aria', { name })}
      data-reauth={need.level}
    >
      <IconKey size={13} aria-hidden="true" />
      {t('auth_reauth.button')}
    </Button>
  );
}
