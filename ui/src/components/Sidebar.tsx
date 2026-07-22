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
        <div className="sidebar-brand" style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', padding: 'var(--spacing-md) var(--spacing-lg)' }}>
          <img src="/static/logo.svg" alt="Liferay Tunnel" width="28" height="28" style={{ flexShrink: 0 }} />
          <span style={{ fontWeight: 'bold', fontSize: '16px', color: 'var(--text-color)', letterSpacing: '0.5px' }}>Liferay Tunnel</span>
        </div>
        
        <div className="sidebar-menu">
          <div className="sidebar-section-header">
            <span className="sidebar-label">{t('personal', 'Personal')}</span>
          </div>
          <div className="sidebar-section-content" style={{ display: 'block' }}>
            <NavLink to="/dashboard" onClick={onClose} end className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              {t('overview', 'Overview')}
            </NavLink>
            <NavLink to="/account" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              {t('account_settings', 'Account Settings')}
            </NavLink>
          </div>

          {(user?.role === 'admin' || user?.role === 'owner') && (
            <>
              <div className="sidebar-section-header" style={{ marginTop: 'var(--spacing-xl)' }}>
                <span className="sidebar-label">{t('admin_zone', 'Admin Zone')}</span>
              </div>
              <div className="sidebar-section-content" style={{ display: 'block' }}>
                <NavLink to="/admin/subdomains" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('subdomains', 'Subdomains')}
                </NavLink>
                <NavLink to="/admin/extensions" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('extensions', 'Extensions')}
                </NavLink>
                <NavLink to="/admin/users" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>{t('users', 'Users')}</span>
                  {pendingCount > 0 && (
                    <span className="badge" style={{ 
                      background: 'var(--danger, #ef4444)', 
                      color: 'white', 
                      borderRadius: 'var(--spacing-xs)', 
                      padding: '2px var(--spacing-sm)', 
                      fontSize: '11px', 
                      fontWeight: 'bold',
                      lineHeight: '1'
                    }}>
                      {pendingCount}
                    </span>
                  )}
                </NavLink>
                <NavLink to="/admin/tokens" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('tokens', 'Tokens')}
                </NavLink>
                <NavLink to="/admin/analytics" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('analytics', 'Analytics')}
                </NavLink>
                <NavLink to="/admin/telemetry" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('telemetry', 'Telemetry')}
                </NavLink>
                <NavLink to="/admin/edge-health" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('network_health', 'Network Health')}
                </NavLink>
                <NavLink to="/admin/audit" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('audit_log', 'Audit Log')}
                </NavLink>
                <NavLink to="/admin/blacklist" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('ip_blacklist', 'IP Blacklist')}
                </NavLink>
                <NavLink to="/admin/magic-links" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('magic_links', 'Magic Links')}
                </NavLink>
                <NavLink to="/admin/settings" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('settings', 'Settings')}
                </NavLink>
              </div>
            </>
          )}
        </div>
        
        <div className="sidebar-footer" style={{ padding: 'var(--spacing-lg)' }}>
          <div style={{ paddingBottom: 'var(--spacing-lg)', marginBottom: 'var(--spacing-lg)', borderBottom: '1px solid var(--border)' }}>
            

            <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '12px' }}>
              <a href="/privacy" target="_blank" className="sidebar-footer-link">{t('privacy_policy', 'Privacy Policy')}</a>
              <a href="/cookies" target="_blank" className="sidebar-footer-link">{t('cookie_policy', 'Cookies')}</a>
            </div>
          </div>

          <div style={{ fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-sm)' }}>
            {t('logged_in_as', 'Logged in as')} <strong>{user?.email}</strong>
          </div>
          <button 
            className="btn btn-secondary" 
            style={{ width: '100%', padding: 'var(--spacing-sm)' }}
            onClick={async () => {
              await fetch('/api/auth/logout', { method: 'POST' });
              window.location.href = '/portal-v2/login';
            }}
          >
            {t('sign_out', 'Sign Out')}
          </button>
        </div>
      </div>
    </>
  );
}
