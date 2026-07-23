import { useEffect, useState } from 'react';
import axios from 'axios';
import { NavLink } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';

interface SidebarProps {
  user: any;
  isOpen: boolean;
  onClose: () => void;
}

export default function Sidebar({ user, isOpen, onClose }: SidebarProps) {
  const { t } = useI18n();
  const [pendingCount, setPendingCount] = useState(0);

  useEffect(() => {
    if (user?.role === 'admin' || user?.role === 'owner') {
      axios.get('/api/admin/users')
        .then(res => {
          const list = res.data || [];
          const count = list.filter((u: any) => u.status === 'pending').length;
          setPendingCount(count);
        })
        .catch(err => console.error("Failed to load users for sidebar badge", err));
    }
  }, [user]);

  return (
    <>
      <div className={`sidebar-backdrop ${isOpen ? 'visible' : ''}`} onClick={onClose}></div>
      <div className={`sidebar ${isOpen ? 'active' : ''}`}>
        <div className="sidebar-brand flex items-center gap-sm px-lg py-md">
          <img src="/static/logo.svg" alt="Liferay Tunnel" width="28" height="28" className="shrink-0" />
          <span className="fw-bold text-base text-main tracking-wide">Liferay Tunnel</span>
        </div>
        
        <div className="sidebar-menu">
          <div className="sidebar-section-header">
            <span className="sidebar-label">{t('sidebar_personal', 'Personal')}</span>
          </div>
          <div className="sidebar-section-content block">
            <NavLink to="/dashboard" onClick={onClose} end className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              {t('sidebar_overview', 'Overview')}
            </NavLink>
            <NavLink to="/account" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              {t('sidebar_account', 'Account Settings')}
            </NavLink>
          </div>

          {(user?.role === 'admin' || user?.role === 'owner') && (
            <>
              <div className="sidebar-section-header mt-xl">
                <span className="sidebar-label">{t('admin_zone', 'Admin Zone')}</span>
              </div>
              <div className="sidebar-section-content block">
                <NavLink to="/admin/subdomains" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_reservations', 'Subdomains')}
                </NavLink>
                <NavLink to="/admin/extensions" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('extensions', 'Extensions')}
                </NavLink>
                <NavLink to="/admin/users" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>{t('sidebar_users', 'Users')}</span>
                  {pendingCount > 0 && (
                    <span className="badge badge-danger text-2xs py-2xs px-xs">
                      {pendingCount}
                    </span>
                  )}
                </NavLink>
                <NavLink to="/admin/tokens" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_tokens', 'Tokens')}
                </NavLink>
                <NavLink to="/admin/analytics" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_analytics', 'Analytics')}
                </NavLink>
                <NavLink to="/admin/telemetry" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('telemetry', 'Telemetry')}
                </NavLink>
                <NavLink to="/admin/edge-health" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('network_health', 'Network Health')}
                </NavLink>
                <NavLink to="/admin/audit" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_audit', 'Audit Log')}
                </NavLink>
                <NavLink to="/admin/blacklist" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_blacklist', 'IP Blacklist')}
                </NavLink>
                <NavLink to="/admin/magic-links" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('sidebar_magic', 'Magic Links')}
                </NavLink>
                <NavLink to="/admin/settings" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('settings', 'Settings')}
                </NavLink>
              </div>
            </>
          )}
        </div>
        
        <div className="sidebar-footer p-lg">
          <div className="pb-lg mb-lg border-b">
            <div className="flex gap-md text-xs">
              <a href="/privacy" target="_blank" className="sidebar-footer-link">{t('privacy_policy', 'Privacy Policy')}</a>
              <a href="/cookies" target="_blank" className="sidebar-footer-link">{t('cookie_policy', 'Cookies')}</a>
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
          <a
            href="/portal"
            className="sidebar-footer-link block text-center text-xs mt-sm"
          >
            ← {t('use_classic_dashboard', 'Use Classic Dashboard')}
          </a>
        </div>
      </div>
    </>
  );
}
