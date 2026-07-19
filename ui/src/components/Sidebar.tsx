import { NavLink } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';

interface SidebarProps {
  user: any;
  isOpen: boolean;
  onClose: () => void;
}

export default function Sidebar({ user, isOpen, onClose }: SidebarProps) {
  const { theme, toggleTheme, useUTC, toggleUTC } = useSettings();
  const { language, setLanguage, t, availableLanguages } = useI18n();

  return (
    <>
      <div className={`sidebar-backdrop ${isOpen ? 'visible' : ''}`} onClick={onClose}></div>
      <div className={`sidebar ${isOpen ? 'active' : ''}`}>
        <div className="sidebar-brand" style={{ display: 'flex', alignItems: 'center', gap: '10px', padding: '12px 16px' }}>
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
              <div className="sidebar-section-header" style={{ marginTop: '24px' }}>
                <span className="sidebar-label">{t('admin_zone', 'Admin Zone')}</span>
              </div>
              <div className="sidebar-section-content" style={{ display: 'block' }}>
                <NavLink to="/admin/subdomains" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('subdomains', 'Subdomains')}
                </NavLink>
                <NavLink to="/admin/extensions" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('extensions', 'Extensions')}
                </NavLink>
                <NavLink to="/admin/users" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('users', 'Users')}
                </NavLink>
                <NavLink to="/admin/analytics" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('analytics', 'Analytics')}
                </NavLink>
                <NavLink to="/admin/audit" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('audit_log', 'Audit Log')}
                </NavLink>
                <NavLink to="/admin/blacklist" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('ip_blacklist', 'IP Blacklist')}
                </NavLink>
                <NavLink to="/admin/settings" onClick={onClose} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {t('settings', 'Settings')}
                </NavLink>
              </div>
            </>
          )}
        </div>
        
        <div className="sidebar-footer" style={{ padding: '16px' }}>
          <div style={{ paddingBottom: '16px', marginBottom: '16px', borderBottom: '1px solid var(--border)' }}>
            
            <div style={{ marginBottom: '12px' }}>
              <select 
                className="input-field" 
                style={{ padding: '6px 8px', fontSize: '13px', width: '100%', cursor: 'pointer', background: 'var(--input-bg)' }}
                value={language}
                onChange={(e) => setLanguage(e.target.value)}
              >
                {availableLanguages.map(l => (
                  <option key={l.code} value={l.code}>{l.label}</option>
                ))}
              </select>
            </div>

            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '12px' }}>
              <span style={{ fontSize: '13px', color: 'var(--text-muted)' }}>{t('theme_dark', 'Dark Mode')}</span>
              <label className="switch">
                <input type="checkbox" checked={theme === 'dark'} onChange={toggleTheme} />
                <span className="slider round"></span>
              </label>
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
              <span style={{ fontSize: '13px', color: 'var(--text-muted)' }}>{t('utc_time', 'UTC Time')}</span>
              <label className="switch">
                <input type="checkbox" checked={useUTC} onChange={toggleUTC} />
                <span className="slider round"></span>
              </label>
            </div>
            <div style={{ display: 'flex', gap: '12px', fontSize: '12px' }}>
              <a href="/privacy" target="_blank" style={{ color: 'var(--text-muted)', textDecoration: 'none' }} onMouseOver={e => e.currentTarget.style.color='var(--primary)'} onMouseOut={e => e.currentTarget.style.color='var(--text-muted)'}>{t('privacy_policy', 'Privacy Policy')}</a>
              <a href="/cookies" target="_blank" style={{ color: 'var(--text-muted)', textDecoration: 'none' }} onMouseOver={e => e.currentTarget.style.color='var(--primary)'} onMouseOut={e => e.currentTarget.style.color='var(--text-muted)'}>{t('cookie_policy', 'Cookies')}</a>
            </div>
          </div>

          <div style={{ fontSize: '12px', color: 'var(--text-muted)', marginBottom: '8px' }}>
            {t('logged_in_as', 'Logged in as')} <strong>{user?.email}</strong>
          </div>
          <button 
            className="btn btn-secondary" 
            style={{ width: '100%', padding: '8px' }}
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
