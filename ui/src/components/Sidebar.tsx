import { NavLink } from 'react-router-dom';

interface SidebarProps {
  user: any;
}

export default function Sidebar({ user }: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-brand" style={{ display: 'flex', alignItems: 'center', gap: '10px', padding: '12px 16px' }}>
        <img src="/static/logo.svg" alt="Liferay Tunnel" width="28" height="28" style={{ flexShrink: 0 }} />
        <span style={{ fontWeight: 'bold', fontSize: '16px', color: 'var(--text-color)', letterSpacing: '0.5px' }}>Liferay Tunnel</span>
      </div>
      
      <div className="sidebar-menu">
        <div className="sidebar-section-header">
          <span className="sidebar-label">Personal</span>
        </div>
        <div className="sidebar-section-content" style={{ display: 'block' }}>
          <NavLink to="/dashboard" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            Overview
          </NavLink>
        </div>

        {(user?.role === 'admin' || user?.role === 'owner') && (
          <>
            <div className="sidebar-section-header" style={{ marginTop: '24px' }}>
              <span className="sidebar-label">Admin Zone</span>
            </div>
            <div className="sidebar-section-content" style={{ display: 'block' }}>
              <NavLink to="/admin/subdomains" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                Subdomains
              </NavLink>
              <NavLink to="/admin/users" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                Users
              </NavLink>
              <NavLink to="/admin/settings" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                Settings
              </NavLink>
            </div>
          </>
        )}
      </div>
      
      <div className="sidebar-footer" style={{ padding: '16px' }}>
        <div style={{ fontSize: '12px', color: 'var(--text-muted)', marginBottom: '8px' }}>
          Logged in as <strong>{user?.email}</strong>
        </div>
        <button 
          className="btn btn-secondary" 
          style={{ width: '100%', padding: '8px' }}
          onClick={async () => {
            await fetch('/api/auth/logout', { method: 'POST' });
            window.location.href = '/portal-v2/login';
          }}
        >
          Sign Out
        </button>
      </div>
    </div>
  );
}
