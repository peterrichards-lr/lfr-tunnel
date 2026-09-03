import { useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';

/**
 * The login-time gate and persistent banner for an updated policy (#1707).
 *
 * Three phases, and they are not three styles of the same thing -- they differ in what
 * the user can still do:
 *
 *   grace    the portal works. A gate appears with Accept / Remind me later; once
 *            dismissed a banner stays for the rest of the session.
 *   warning  the same, with the banner escalated. Clients also warn at startup.
 *   expired  the portal is blocked until accepted, and new tunnels are refused. The
 *            gate has no dismiss and no way past it except accepting.
 *
 * "Remind me later" is per SESSION, cleared by the server when the session ends, so the
 * gate is seen again at the next login. A permanent dismissal would leave the banner
 * applying no pressure and make the grace window the only mechanism -- which is the
 * outcome the gate exists to prevent.
 *
 * Server-side enforcement does not depend on any of this. `/api/*` is refused with 403
 * `policy_consent_required` once the window expires, and `POST /api/register` refuses new
 * tunnels, whatever the browser chooses to render.
 */
export interface PolicyConsent {
  required: boolean;
  document_id?: string;
  version?: string;
  phase?: 'grace' | 'warning' | 'expired' | '';
  deadline?: string;
  seconds_remaining?: number;
  policy_url?: string;
  cookie_url?: string;
  accepted_at?: string;
}

/** Days and hours, or hours and minutes inside the last day. Matches the client's format. */
function formatRemaining(seconds?: number): string {
  if (!seconds || seconds <= 0) return '';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  const minutes = Math.floor((seconds % 3600) / 60);
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

export default function PolicyConsentGate({
  consent,
  suppressed,
  onRemindLater,
}: {
  consent?: PolicyConsent;
  suppressed?: boolean;
  onRemindLater: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  if (!consent?.required) return null;

  const expired = consent.phase === 'expired';
  const warning = consent.phase === 'warning';
  const remaining = formatRemaining(consent.seconds_remaining);

  const accept = async () => {
    setBusy(true);
    setError('');
    try {
      await axios.post('/api/me/policy-consent');
      // A full reload rather than a state update: every panel that failed with 403 while
      // the gate was up already ran its fetch and will not retry on its own, which is the
      // same reasoning as the MFA gate's reload (#1071).
      window.location.reload();
    } catch {
      setError(
        t(
          'policy_consent_error',
          'Could not record your acceptance. Please try again.',
        ),
      );
      setBusy(false);
    }
  };

  const remindLater = async () => {
    setBusy(true);
    try {
      await axios.post('/api/me/policy-consent/remind-later');
      onRemindLater();
    } finally {
      setBusy(false);
    }
  };

  const links = (
    <p className="text-sm m-0 mt-md">
      {consent.policy_url && (
        <a href={consent.policy_url} target="_blank" rel="noopener noreferrer">
          {t('privacy_policy', 'Privacy Policy')}
        </a>
      )}
      {consent.policy_url && consent.cookie_url && ' · '}
      {consent.cookie_url && (
        <a href={consent.cookie_url} target="_blank" rel="noopener noreferrer">
          {t('cookie_disclosure', 'Cookie Disclosure')}
        </a>
      )}
    </p>
  );

  // The banner. Shown once the gate has been dismissed for this session, and never
  // instead of the expired gate -- an expired user has nothing to dismiss it back to.
  if (suppressed && !expired) {
    return (
      <div
        className={`policy-consent-banner${warning ? ' policy-consent-banner--urgent' : ''}`}
        role="status"
        data-testid="policy-consent-banner"
      >
        <p className="m-0 text-sm fw-medium">
          {warning
            ? t(
                'policy_consent_banner_urgent',
                'The Privacy Policy has changed. Accept it to keep your tunnels working.',
              )
            : t(
                'policy_consent_banner',
                'The Privacy Policy has been updated and needs your acceptance.',
              )}
          {remaining ? ` (${remaining})` : ''}
        </p>
        <button
          type="button"
          className="btn btn-secondary py-xs px-md text-xs w-auto"
          onClick={accept}
          disabled={busy}
        >
          {t('policy_consent_accept', 'Review and accept')}
        </button>
      </div>
    );
  }

  return (
    <div className="dialog-overlay dialog-overlay--gate">
      <div
        className="card gate-card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="policy-gate-title"
        data-testid="policy-consent-gate"
      >
        <h3 id="policy-gate-title" className="m-0 mb-sm">
          {expired
            ? t(
                'policy_consent_title_expired',
                'Accept the Updated Privacy Policy to Continue',
              )
            : t('policy_consent_title', 'Our Privacy Policy Has Changed')}
        </h3>
        <p className="text-muted text-sm mb-lg">
          {expired
            ? t(
                'policy_consent_desc_expired',
                'The time to accept the updated Privacy Policy and Cookie Disclosure has passed. The portal is unavailable and new tunnels are being refused until you accept. Tunnels already running have not been interrupted.',
              )
            : t(
                'policy_consent_desc',
                'We have updated our Privacy Policy and Cookie Disclosure. Please read and accept them to carry on using Liferay Tunnel.',
              )}
        </p>

        {!expired && remaining && (
          <div
            className={`alert-banner ${warning ? 'alert-banner--warning' : 'alert-banner--info'} mb-md`}
          >
            <div className="flex-1 text-sm">
              {t('policy_consent_remaining', 'Time left to accept')}:{' '}
              {remaining}
            </div>
          </div>
        )}

        {links}

        {error && (
          <div className="alert-banner alert-banner--danger mt-md">{error}</div>
        )}

        <button
          type="button"
          className="btn btn-primary mt-lg"
          onClick={accept}
          disabled={busy}
        >
          {busy
            ? t('policy_consent_saving', 'Saving...')
            : t('policy_consent_agree', 'I have read and accept')}
        </button>

        {!expired && (
          <button
            type="button"
            className="btn btn-secondary mt-sm text-sm"
            onClick={remindLater}
            disabled={busy}
          >
            {t('policy_consent_later', 'Remind me later')}
          </button>
        )}
      </div>
    </div>
  );
}
