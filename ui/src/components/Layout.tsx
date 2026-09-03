import { useEffect, useState } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import axios from 'axios';
import Sidebar from './Sidebar';
import ViewAsBar from './ViewAsBar';
import ScrollToTopButton from './ScrollToTopButton';
import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';
import ShortcutsOverlay from './ShortcutsOverlay';
import SessionExpiryWarning from './SessionExpiryWarning';
import PolicyConsentGate from './PolicyConsentGate';

export default function Layout() {
  const [user, setUser] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [uptime, setUptime] = useState<string>('');
  // V1's footer has linked out to the status page all along; V2's did not (#1559).
  const [statusPageUrl, setStatusPageUrl] = useState<string>('');
  // Gateway and client versions for the sidebar footer (#1647). Deliberately without uptime:
  // the header already shows that beside the status indicator, and two displays of one fact in
  // a single view is what #1603 removed for the status link -- they disagree the moment one is
  // not refreshed.
  const [serverVersion, setServerVersion] = useState<string>('');
  const [clientVersion, setClientVersion] = useState<string>('');
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const navigate = useNavigate();
  const { t } = useI18n();
  const { formatDate, showPortalBanner, setShowPortalBanner } = useSettings();

  // Read from SettingsContext rather than local state so the Account Settings toggle can turn it
  // back on (#1626); dismissing here is the same preference, written the same way.
  const showV1Promo = showPortalBanner;

  const dismissV1Promo = () => setShowPortalBanner(false);

  const dismissTargetedMessage = async () => {
    try {
      await axios.post('/api/me/dismiss-message');
      setUser((prev: any) => ({ ...prev, targeted_message: '' }));
    } catch (e) {
      console.error('Failed to dismiss message', e);
    }
  };

  // Re-reads /api/me so session_expires_at reflects the slide the request itself caused
  // (#1655/#1656). Declared here rather than inside the effect because the expiry warning needs
  // to call it after extending.
  const refreshUser = async () => {
    try {
      const res = await axios.get('/api/me');
      setUser(res.data);
    } catch {
      navigate('/login');
    }
  };

  useEffect(() => {
    const fetchInitial = async () => {
      try {
        const [userRes, versionRes] = await Promise.all([
          axios.get('/api/me'),
          axios.get('/api/version').catch(() => ({ data: {} })),
        ]);
        setUser(userRes.data);

        // Only rendered when configured. V1 fell back to a hardcoded status.lfr-demo.se when it
        // was not, which put one deployment's URL in the source and showed a link that a
        // different deployment could not honour.
        setStatusPageUrl(versionRes.data?.status_page_url || '');

        // server_version first, latest_version as the fallback -- the same order V1 uses, so
        // the two arms cannot disagree about what "Gateway" means.
        setServerVersion(
          versionRes.data?.server_version ||
            versionRes.data?.latest_version ||
            '',
        );
        setClientVersion(versionRes.data?.latest_version || '');

        // Calculate Uptime
        const seconds = versionRes.data?.uptime_seconds;
        if (typeof seconds === 'number') {
          const d = Math.floor(seconds / (3600 * 24));
          const h = Math.floor((seconds % (3600 * 24)) / 3600);
          const m = Math.floor((seconds % 3600) / 60);
          setUptime(`${d}d ${h}h ${m}m`);
        }
      } catch {
        navigate('/login');
      } finally {
        setLoading(false);
      }
    };

    fetchInitial();

    const interval = setInterval(() => {
      axios
        // Marked as a background poll so it does not extend the session (#1676). Without this
        // an open tab renews itself every ten seconds forever, and the idle timeout measures
        // whether a tab is open rather than whether anyone is at the keyboard.
        .get('/api/me', { headers: { 'X-Background-Poll': '1' } })
        .then((res) => {
          setUser(res.data);
        })
        .catch((err) => {
          if (err.response?.status === 401) {
            navigate('/login');
          }
        });
    }, 10000);

    return () => clearInterval(interval);
  }, [navigate]);

  if (loading) {
    return (
      <div
        id="loader"
        className="flex items-center justify-center min-h-screen"
      >
        <div className="spinner"></div>
      </div>
    );
  }

  if (!user) return null;

  return (
    <div className="flex flex-col h-screen w-full overflow-hidden">
      {/* Above the fold and above every other banner: while previewing, the session is
          read-only, so an owner who cannot tell would report the refusals as bugs. */}
      {/* First thing in the tab order: a keyboard user should not have to tab through
          the whole sidebar to reach the page content (#1219). Visually hidden until
          focused, which is the conventional treatment. */}
      <a href="#main-content" className="skip-link">
        {t('skip_to_content', 'Skip to content')}
      </a>

      {/* Above everything, including the session warning: while the grace window is
          expired this is the only thing on the page that can be acted on, and during the
          window it is the one notice with a deadline attached (#1707). */}
      <PolicyConsentGate
        consent={user.policy_consent}
        suppressed={user.policy_gate_suppressed}
        onRemindLater={() =>
          setUser((prev: any) => ({ ...prev, policy_gate_suppressed: true }))
        }
      />

      <ViewAsBar viewAs={user.view_as} canViewAs={user.can_view_as} />

      {/* Above the promo banner: losing unsaved work matters more than the offer to switch
          portals, and only one of the two is time-critical (#1656). */}
      <SessionExpiryWarning
        expiresAt={user.session_expires_at}
        onExtended={refreshUser}
      />

      {showV1Promo && (
        <div className="v1-promo-banner">
          <p className="m-0 text-sm fw-medium">
            {t('banner_legacy_interface', 'Need the legacy interface?')}{' '}
            <a href="/portal/">{t('btn_switch_v1', 'Switch back to V1 →')}</a>
          </p>
          <button
            onClick={dismissV1Promo}
            className="v1-promo-banner__dismiss"
            title={t('dismiss_promo_banner', 'Dismiss promo banner')}
            aria-label={t('dismiss_promo_banner', 'Dismiss promo banner')}
          >
            &times;
          </button>
        </div>
      )}

      <div
        id="dashboard-screen"
        className="flex flex-1 min-h-0 transition-all duration-200"
      >
        <Sidebar
          user={user}
          isOpen={isSidebarOpen}
          onClose={() => setIsSidebarOpen(false)}
          serverVersion={serverVersion}
          clientVersion={clientVersion}
        />

        {/* Registers the shortcuts and owns the overlay that documents them (#1611). */}
        <ShortcutsOverlay user={user} />

        {/* Mobile Top Header */}
        <div className="mobile-header p-lg border-b items-center gap-lg">
          <button
            className="btn btn-secondary p-sm border text-main"
            onClick={() => setIsSidebarOpen(true)}
          >
            <svg
              width="24"
              height="24"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <line x1="3" y1="12" x2="21" y2="12"></line>
              <line x1="3" y1="6" x2="21" y2="6"></line>
              <line x1="3" y1="18" x2="21" y2="18"></line>
            </svg>
          </button>
          <span className="fw-bold text-base text-main">Liferay Tunnel</span>
        </div>

        <main id="main-content" className="main-content" tabIndex={-1}>
          {user.broadcast_message && (
            <div className="alert-banner alert-banner--info">
              <span className="text-lg">📢</span>
              <div className="flex-1 text-sm">
                <strong>{t('broadcast_alert', 'System Broadcast')}:</strong>{' '}
                {user.broadcast_message}
              </div>
            </div>
          )}

          {user.targeted_message && (
            <div className="alert-banner alert-banner--info">
              <span className="text-lg">💬</span>
              <div className="flex-1 text-sm">
                <strong>{t('admin_message', 'Admin Message')}:</strong>{' '}
                {user.targeted_message}
              </div>
              <button
                onClick={dismissTargetedMessage}
                className="btn btn-secondary py-xs px-md text-xs"
              >
                {t('dismiss', 'Dismiss')}
              </button>
            </div>
          )}

          <header className="content-header flex justify-between items-center mb-2xl">
            <div>
              <p className="m-0 text-muted">
                {t('welcome_back', 'Welcome back')}, {user.first_name}
              </p>
              {user.last_login_at && !user.last_login_at.startsWith('0001') && (
                <p className="mt-xs m-0 text-muted text-xs">
                  Last login: {formatDate(user.last_login_at)} from{' '}
                  <code className="surface-subtle px-xs py-2xs rounded">
                    {user.last_login_ip || 'Unknown'}
                  </code>
                </p>
              )}
            </div>
            <div className="text-right">
              {/* Linked only when the deployment configures a status page (#1603). This used to
                  hardcode status.lfr-demo.se, so every other deployment pointed its users at
                  someone else's status page -- the same thing #1586 removed from the footer.
                  The indicator itself still shows either way: "System Online" is worth saying
                  on its own, it just should not link somewhere arbitrary. */}
              {statusPageUrl ? (
                <a
                  href={statusPageUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-sm justify-end mb-xs no-underline"
                >
                  <div className="status-dot status-dot--online"></div>
                  <span className="text-xs fw-semibold text-main">
                    {t('system_online', 'System Online')}
                  </span>
                </a>
              ) : (
                <div className="flex items-center gap-sm justify-end mb-xs">
                  <div className="status-dot status-dot--online"></div>
                  <span className="text-xs fw-semibold text-main">
                    {t('system_online', 'System Online')}
                  </span>
                </div>
              )}
              {uptime && (
                <div className="text-xs text-muted">
                  {t('uptime', 'Uptime')}: {uptime}
                </div>
              )}
            </div>
          </header>
          <div>
            <Outlet context={{ user }} />
          </div>
          <ScrollToTopButton />
        </main>
      </div>
    </div>
  );
}
