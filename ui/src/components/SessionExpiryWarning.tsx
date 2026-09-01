import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';

/** How long before expiry the warning appears. */
const WARN_BEFORE_MS = 15 * 60 * 1000;
/** How often the remaining time is recomputed once the warning is up. */
const TICK_MS = 30 * 1000;

function minutesLeft(expiresAt: string): number {
  return Math.max(0, Math.ceil((Date.parse(expiresAt) - Date.now()) / 60000));
}

/**
 * Warns before the portal session expires, and offers a way to stay signed in (#1656).
 *
 * Until #1655 this could not have been built honestly: the server slid the session's expiry but
 * never re-issued the cookie, so the browser's copy died 24h after login regardless. A countdown
 * against the server's expiry would have shown time the user did not have -- and a warning that
 * is wrong is worse than none.
 *
 * The expiry has to come from the server (`session_expires_at` on /api/me). The cookie is
 * HttpOnly by design, so the client cannot read its own.
 */
export default function SessionExpiryWarning({
  expiresAt,
  onExtended,
}: {
  expiresAt?: string;
  onExtended: () => void;
}) {
  const { t } = useI18n();
  const [remaining, setRemaining] = useState<number | null>(null);
  const [extending, setExtending] = useState(false);

  useEffect(() => {
    if (!expiresAt) {
      setRemaining(null);
      return;
    }

    const check = () => {
      const msLeft = Date.parse(expiresAt) - Date.now();
      setRemaining(msLeft <= WARN_BEFORE_MS ? minutesLeft(expiresAt) : null);
    };

    check();
    const id = setInterval(check, TICK_MS);
    return () => clearInterval(id);
  }, [expiresAt]);

  if (remaining === null) return null;

  const staySignedIn = async () => {
    setExtending(true);
    try {
      // A real authenticated request, not just dismissing the notice. Every request slides the
      // session server-side and re-issues the cookie (#1655), so this genuinely extends it --
      // a button that only hid the warning would be a lie.
      await axios.get('/api/me');
      onExtended();
    } finally {
      setExtending(false);
    }
  };

  return (
    <div className="session-expiry-banner" role="status">
      <p className="m-0 text-sm fw-medium">
        {remaining > 0
          ? t(
              'session_expiring_in',
              `Your session ends in about ${remaining} minute${remaining === 1 ? '' : 's'}.`,
            )
          : t('session_expired_soon', 'Your session is about to end.')}
      </p>
      <button
        type="button"
        className="btn btn-secondary py-xs px-md text-xs w-auto"
        onClick={staySignedIn}
        disabled={extending}
      >
        {t('stay_signed_in', 'Stay signed in')}
      </button>
    </div>
  );
}
