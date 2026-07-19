import { useEffect, useState } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import axios from 'axios';
import Sidebar from './Sidebar';
import { useI18n } from '../contexts/I18nContext';

export default function Layout() {
  const [user, setUser] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [uptime, setUptime] = useState<string>('');
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const navigate = useNavigate();
  const { t } = useI18n();

  useEffect(() => {
    // Fetch current user and version info in parallel
    Promise.all([
      axios.get('/api/me'),
      axios.get('/api/version').catch(() => ({ data: {} }))
    ])
      .then(([userRes, versionRes]) => {
        setUser(userRes.data);
        
        // Calculate Uptime
        const seconds = versionRes.data?.uptime_seconds;
        if (typeof seconds === 'number') {
          const d = Math.floor(seconds / (3600*24));
          const h = Math.floor(seconds % (3600*24) / 3600);
          const m = Math.floor(seconds % 3600 / 60);
          setUptime(`${d}d ${h}h ${m}m`);
        }
      })
      .catch(() => {
        // Not authenticated
        navigate('/login');
      })
      .finally(() => {
        setLoading(false);
      });
  }, [navigate]);

  if (loading) {
    return <div id="loader" style={{ display: 'flex' }}><div className="spinner"></div></div>;
  }

  if (!user) return null;

  return (
    <div id="dashboard-screen" style={{ display: 'flex' }}>
      <Sidebar user={user} isOpen={isSidebarOpen} onClose={() => setIsSidebarOpen(false)} />
      
      {/* Mobile Top Header */}
      <div className="mobile-header" style={{ display: 'none', padding: '16px', background: 'var(--bg-card)', borderBottom: '1px solid var(--border)', alignItems: 'center', gap: '16px' }}>
        <button className="btn" onClick={() => setIsSidebarOpen(true)} style={{ padding: '8px', background: 'transparent', border: '1px solid var(--border)' }}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="3" y1="12" x2="21" y2="12"></line>
            <line x1="3" y1="6" x2="21" y2="6"></line>
            <line x1="3" y1="18" x2="21" y2="18"></line>
          </svg>
        </button>
        <span style={{ fontWeight: 'bold', fontSize: '16px' }}>Liferay Tunnel</span>
      </div>

      <div className="main-content">
        <header className="content-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '32px' }}>
          <div>
            <h2 style={{ margin: 0 }}>{t('dashboard', 'Dashboard')}</h2>
            <p style={{ margin: 0, color: 'var(--text-muted)' }}>{t('welcome_back', 'Welcome back')}, {user.first_name}</p>
          </div>
          <div style={{ textAlign: 'right' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', justifyContent: 'flex-end', marginBottom: '4px' }}>
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--success)' }}></div>
              <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--text-main)' }}>{t('system_online', 'System Online')}</span>
            </div>
            {uptime && <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>{t('uptime', 'Uptime')}: {uptime}</div>}
          </div>
        </header>
        <div>
          <Outlet context={{ user }} />
        </div>
      </div>
    </div>
  );
}
