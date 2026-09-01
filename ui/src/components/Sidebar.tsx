import { useEffect, useRef, useState } from 'react';
import axios from 'axios';
import { NavLink } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';

interface SidebarProps {
  user: any;
  isOpen: boolean;
  onClose: () => void;
}

export default function Sidebar({ user, isOpen, onClose }: SidebarProps) {
  // Arrow-key movement within the sidebar (#1562), matching V1's behaviour so the two arms of the
  // A/B test cost a keyboard user the same effort.
  //
  // Additive on purpose: every nav item keeps its place in the tab order. The usual roving-tabindex
  // pattern collapses the menu to a single Tab stop, which suits a menubar but takes something away
  // from a plain list of links -- anyone already tabbing this sidebar would find it behaves
  // differently for no reason they asked for. Arrows are a faster route, not a replacement.
  //
  // Scoped to keydowns originating inside the nav, so arrows elsewhere -- inputs, selects, the page
  // itself -- are untouched, and nothing here binds a printable character. Screen readers consume
  // arrow keys in browse mode before the page sees them, so this does not compete with them.
  const navRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const nav = navRef.current;
    if (!nav) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return;
      // Modifier combinations belong to the browser: Alt+Arrow is history, Home/End with one is a
      // document jump.
      if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;

      // Read per keypress rather than cached: which links render depends on role, and on whether
      // the pending-registrations badge or a collapsible section has changed the list.
      const items = Array.from(
        nav.querySelectorAll<HTMLElement>('.nav-item'),
      ).filter((el) => el.offsetParent !== null);
      if (!items.length) return;

      const current = items.indexOf(document.activeElement as HTMLElement);
      if (current === -1) return; // inside the nav, but not on a nav item

      let next: number;
      if (e.key === 'Home') {
        next = 0;
      } else if (e.key === 'End') {
        next = items.length - 1;
      } else {
        const step = e.key === 'ArrowDown' ? 1 : -1;
        next = (current + step + items.length) % items.length;
      }

      e.preventDefault();
      items[next].focus();
    };

    nav.addEventListener('keydown', onKeyDown);
    return () => nav.removeEventListener('keydown', onKeyDown);
  }, []);

  const { t } = useI18n();
  const [pendingCount, setPendingCount] = useState(0);

  useEffect(() => {
    if (user?.role === 'admin' || user?.role === 'owner') {
      axios
        .get('/api/admin/users')
        .then((res) => {
          const list = res.data || [];
          const count = list.filter((u: any) => u.status === 'pending').length;
          setPendingCount(count);
        })
        .catch((err) =>
          console.error('Failed to load users for sidebar badge', err),
        );
    }
    // Keyed on identity and role rather than the polled user object: this fetches the whole
    // user list to count pending registrations, and was doing so every 10 seconds (#1208).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user?.id, user?.role]);

  return (
    <>
      <div
        className={`sidebar-backdrop ${isOpen ? 'visible' : ''}`}
        onClick={onClose}
      ></div>
      {/* A navigation landmark, so screen-reader users can jump straight here
          instead of tabbing through the page to find it (#1219). */}
      <nav
        ref={navRef}
        className={`sidebar ${isOpen ? 'active' : ''}`}
        aria-label="Primary"
      >
        <div className="sidebar-brand flex items-center gap-sm px-lg py-md">
          <img
            src="/static/logo.svg"
            alt="Liferay Tunnel"
            width="28"
            height="28"
            className="flex-shrink-0"
          />
          <span className="fw-bold text-base text-main tracking-wide">
            Liferay Tunnel
          </span>
        </div>

        <div className="sidebar-menu">
          <div className="sidebar-section-header">
            <span className="sidebar-label">
              {t('sidebar_personal', 'Personal')}
            </span>
          </div>
          <div className="sidebar-section-content block">
            <NavLink
              to="/dashboard"
              onClick={onClose}
              end
              className={({ isActive }) =>
                `nav-item ${isActive ? 'active' : ''}`
              }
            >
              {t('sidebar_overview', 'Overview')}
            </NavLink>
            <NavLink
              to="/account"
              onClick={onClose}
              className={({ isActive }) =>
                `nav-item ${isActive ? 'active' : ''}`
              }
            >
              {t('sidebar_account', 'Account Settings')}
            </NavLink>
            {/* Only for non-admins (#1512). An admin already has Analytics under Admin Zone
                and both links render the same page, so showing both would be two entries
                pointing at one thing. Non-admins had no entry at all, which is the bug. */}
            {user?.role !== 'admin' && user?.role !== 'owner' && (
              <NavLink
                to="/analytics"
                onClick={onClose}
                className={({ isActive }) =>
                  `nav-item ${isActive ? 'active' : ''}`
                }
              >
                {t('sidebar_analytics', 'Analytics')}
              </NavLink>
            )}
          </div>

          {(user?.role === 'admin' || user?.role === 'owner') && (
            <>
              <div className="sidebar-section-header mt-xl">
                <span className="sidebar-label">
                  {t('admin_zone', 'Admin Zone')}
                </span>
              </div>
              <div className="sidebar-section-content block">
                <NavLink
                  to="/admin/subdomains"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_admin_subdomains', 'Registered Subdomains')}
                </NavLink>
                <NavLink
                  to="/admin/vanity-domain-status"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_vanity_status', 'Custom Domains')}
                </NavLink>
                <NavLink
                  to="/admin/extensions"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('extensions', 'Extensions')}
                </NavLink>
                <NavLink
                  to="/admin/users"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item flex justify-between items-center ${isActive ? 'active' : ''}`
                  }
                >
                  <span>{t('sidebar_users', 'Users')}</span>
                  {pendingCount > 0 && (
                    <span className="badge badge-danger text-2xs py-2xs px-xs">
                      {pendingCount}
                    </span>
                  )}
                </NavLink>
                <NavLink
                  to="/admin/tokens"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_tokens', 'Tokens')}
                </NavLink>
                <NavLink
                  to="/admin/analytics"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_analytics', 'Analytics')}
                </NavLink>
                <NavLink
                  to="/admin/telemetry"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('telemetry', 'Telemetry')}
                </NavLink>
                <NavLink
                  to="/admin/edge-health"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('network_health', 'Network Health')}
                </NavLink>
                <NavLink
                  to="/admin/audit"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_audit', 'Audit Log')}
                </NavLink>
                <NavLink
                  to="/admin/blacklist"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_blacklist', 'IP Blacklist')}
                </NavLink>
                <NavLink
                  to="/admin/magic-links"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_magic', 'Magic Links')}
                </NavLink>
                <NavLink
                  to="/admin/backups"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_backups', 'Database Backups')}
                </NavLink>
                <NavLink
                  to="/admin/maintenance"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_maintenance_mode', 'Gateway Maintenance')}
                </NavLink>
                <NavLink
                  to="/admin/settings"
                  onClick={onClose}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? 'active' : ''}`
                  }
                >
                  {t('sidebar_system', 'System Settings')}
                </NavLink>
              </div>
            </>
          )}
        </div>

        <div className="sidebar-footer p-lg">
          <div className="pb-lg mb-lg border-b">
            {/* No status link here: the page header already carries one beside the greeting,
                and two links to the same place in one view is one too many (#1603). V1 keeps
                its copy in the footer because V1 has no header indicator at all. */}
            <div className="sidebar-footer-policies text-xs">
              <a
                href="/privacy"
                target="_blank"
                className="sidebar-footer-link"
              >
                {t('privacy_policy', 'Privacy Policy')}
              </a>
              <a
                href="/cookies"
                target="_blank"
                className="sidebar-footer-link"
              >
                {t('cookie_policy', 'Cookies')}
              </a>
            </div>
          </div>

          <div className="text-xs text-muted mb-sm">
            {t('logged_in_as', 'Logged in as')} <strong>{user?.email}</strong>
          </div>
          <button
            className="btn btn-secondary w-full p-sm"
            onClick={async () => {
              await fetch('/api/auth/logout', { method: 'POST' });
              window.location.href = '/portalv2/login';
            }}
          >
            {t('sign_out', 'Sign Out')}
          </button>
        </div>
      </nav>
    </>
  );
}
