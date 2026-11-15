import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useThemeStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { AUTH_FILES_CHANGED_EVENT } from '@/features/authFiles/authFilesEvents';
import { reauthNeed, reauthPanelProviderId } from '@/features/authFiles/reauth';
import { OAuthFlowDialog } from '@/features/oauth/components/OAuthFlowDialog';
import { useOAuthFlows } from '@/features/oauth/hooks/useOAuthFlows';
import {
  OAUTH_PROVIDERS,
  resolveThemedIcon,
  supportsManualCallback,
  type BuiltInOAuthProviderCard,
} from '@/features/oauth/providers';

interface ReauthTarget {
  card: BuiltInOAuthProviderCard;
  authIndex: string;
  name: string;
}

/**
 * One targeted OAuth login per page. A new target abandons the previous attempt, so a
 * late completion of the old login cannot be shown under the new credential's name.
 */
export function useCredentialReauth(onCredentialChanged: () => void) {
  const { t } = useTranslation();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const [target, setTarget] = useState<ReauthTarget | null>(null);
  const [open, setOpen] = useState(false);
  const focusedRef = useRef<string | null>(null);
  const flows = useOAuthFlows({
    providerCards: OAUTH_PROVIDERS,
    isFocused: (provider) => focusedRef.current === provider,
  });

  const onChangedRef = useRef(onCredentialChanged);
  useEffect(() => {
    onChangedRef.current = onCredentialChanged;
  }, [onCredentialChanged]);
  useEffect(() => {
    const handle = () => onChangedRef.current();
    window.addEventListener(AUTH_FILES_CHANGED_EVENT, handle);
    return () => window.removeEventListener(AUTH_FILES_CHANGED_EVENT, handle);
  }, []);

  const start = (file: AuthFileItem, name: string) => {
    const need = reauthNeed(file);
    if (!need) return;
    const providerId = reauthPanelProviderId(need.provider);
    const card = OAUTH_PROVIDERS.find((item) => item.id === providerId);
    if (!card) return;
    const sameTarget =
      target?.card.id === providerId &&
      target.authIndex === need.authIndex &&
      Boolean(flows.states[providerId]?.status);
    focusedRef.current = providerId;
    setTarget({ card, authIndex: need.authIndex, name });
    setOpen(true);
    if (sameTarget) return;
    if (target) flows.abandonAuth(target.card.id);
    void flows.startAuth(providerId, { authIndex: need.authIndex });
  };

  const close = () => {
    if (target) flows.dismissSuccess(target.card.id);
    focusedRef.current = null;
    setOpen(false);
  };

  const card = target?.card;
  const label = card ? (typeof card.label === 'string' ? card.label : t(card.label.key)) : '';
  const dialog =
    card && target ? (
      <OAuthFlowDialog
        open={open}
        providerId={card.id}
        mode="reauth"
        heading={{
          title: t('auth_reauth.dialog_title', { name: target.name }),
          caption: `${label} · ${t(
            card.flow === 'device' ? 'auth_login.method_device' : 'auth_login.method_browser'
          )}`,
          icon: resolveThemedIcon(card.icon, resolvedTheme),
        }}
        state={flows.states[card.id] ?? {}}
        text={(suffix) => flows.providerText(card.id, suffix)}
        supportsCallback={supportsManualCallback(card)}
        onStart={() => void flows.startAuth(card.id, { authIndex: target.authIndex })}
        onCancel={() => void flows.cancelAuth(card.id)}
        onCallbackChange={(value) => flows.setCallbackInput(card.id, value)}
        onCallbackSubmit={() => void flows.submitCallback(card.id)}
        onClose={close}
      />
    ) : null;

  return { start, dialog };
}
