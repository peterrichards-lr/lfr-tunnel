import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';

/**
 * The signed-in user's client against the gateway's min_client_version (#2522), from
 * `min_version` on /api/me. Absent there when the client is current, a development build, or
 * never seen.
 *
 * The CLI already says this at startup, but only to someone reading its output; a client run from
 * a script, an IDE task or LDM is not. Three states, from the server's phase:
 *   - no deadline yet: an upgrade is required, with no date -- the window starts when the client
 *     next registers a tunnel, and reading this page must not start it;
 *   - a deadline: the date, and the time left once the warning window has opened;
 *   - expired: new tunnels are refused.
 */
export interface MinVersion {
  required: boolean;
  min_version?: string;
  client_version?: string;
  phase?: 'grace' | 'warning' | 'expired' | '';
  deadline?: string;
  seconds_remaining?: number;
  upgrade_command?: string;
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

export default function ClientVersionBanner({ state }: { state?: MinVersion }) {
  const { t } = useI18n();
  const { formatDate } = useSettings();
  if (!state?.required) return null;

  const expired = state.phase === 'expired';
  const urgent = expired || state.phase === 'warning';
  const fill = (s: string) =>
    s
      .replace('{0}', state.client_version || '')
      .replace('{1}', state.min_version || '');

  let message: string;
  if (expired) {
    message = fill(
      t(
        'client_version_banner_expired',
        'Your Liferay Tunnel client ({0}) is older than the minimum this gateway accepts ({1}), so new tunnels are refused. Tunnels already running are not affected.',
      ),
    );
  } else if (state.deadline) {
    // The time left goes beside the date, inside the sentence, once the warning window is open.
    const remaining =
      state.phase === 'warning' ? formatRemaining(state.seconds_remaining) : '';
    const when =
      formatDate(state.deadline) + (remaining ? ` (${remaining})` : '');
    message = fill(
      t(
        'client_version_banner_deadline',
        'Your Liferay Tunnel client ({0}) is older than the minimum this gateway accepts ({1}). Upgrade by {2}, or new tunnels will be refused.',
      ),
    ).replace('{2}', when);
  } else {
    message = fill(
      t(
        'client_version_banner_required',
        'Your Liferay Tunnel client ({0}) is older than the minimum this gateway accepts ({1}). Upgrade it to keep starting new tunnels.',
      ),
    );
  }

  return (
    <div
      className={`policy-consent-banner${urgent ? ' policy-consent-banner--urgent' : ''}`}
      role="status"
      data-testid="client-version-banner"
    >
      <p className="m-0 text-sm fw-medium">
        {message} {t('client_version_banner_run', 'Run')}{' '}
        <code>{state.upgrade_command || 'lfr-tunnel -upgrade'}</code>
      </p>
    </div>
  );
}
