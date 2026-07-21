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

  const [showV1Promo, setShowV1Promo] = useState(!localStorage.getItem('v1_promo_dismissed'));

  const dismissV1Promo = () => {
    localStorage.setItem('v1_promo_dismissed', 'true');
    setShowV1Promo(false);
  };

  const dismissTargetedMessage = async () => {
    try {
      await axios.post('/api/me/dismiss-message');
      setUser((prev: any) => ({ ...prev, targeted_message: '' }));
    } catch (e) {
      console.error('Failed to dismiss message', e);
    }
  };

  useEffect(() => {
    const fetchInitial = async () => {
      try {
        const [userRes, versionRes] = await Promise.all([
          axios.get('/api/me'),
          axios.get('/api/version').catch(() => ({ data: {} }))
        ]);
        setUser(userRes.data);
        
        // Calculate Uptime
        const seconds = versionRes.data?.uptime_seconds;
        if (typeof seconds === 'number') {
          const d = Math.floor(seconds / (3600*24));
          const h = Math.floor(seconds % (3600*24) / 3600);
          const m = Math.floor(seconds % 3600 / 60);
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
      axios.get('/api/me').then(res => {
        setUser(res.data);
      }).catch(err => {
        if (err.response?.status === 401) {
          navigate('/login');
        }
      });
    }, 10000);

    return () => clearInterval(interval);
  }, [navigate]);

  if (loading) {
    return <div id="loader" style={{ display: 'flex' }}><div className="spinner"></div></div>;
  }

  if (!user) return null;

  return (
    <div id="dashboard-screen" style={{ display: 'flex', paddingTop: showV1Promo ? '44px' : '0', transition: 'padding-top 0.2s ease' }}>
      <Sidebar user={user} isOpen={isSidebarOpen} onClose={() => setIsSidebarOpen(false)} />
      
      {showV1Promo && (
        <div style={{ backgroundColor: '#0b5fff', color: 'white', padding: 'var(--spacing-md) var(--spacing-xl)', textAlign: 'center', position: 'fixed', top: 0, left: 0, width: '100%', zIndex: 9999, boxSizing: 'border-box', boxShadow: '0 4px 12px rgba(0,0,0,0.15)' }}>
          <p style={{ margin: 0, fontSize: '14px', fontWeight: 500 }}>
            Need the legacy interface? <a href="/portal/" style={{ color: 'white', textDecoration: 'underline', fontWeight: 700, marginLeft: 'var(--spacing-sm)' }}>Switch back to V1 &rarr;</a>
          </p>
          <button onClick={dismissV1Promo} style={{ position: 'absolute', right: 'var(--spacing-xl)', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', color: 'white', cursor: 'pointer', fontSize: '18px', padding: 'var(--spacing-xs)', lineHeight: 1 }}>&times;</button>
        </div>
      )}

      {/* Mobile Top Header */}
      <div className="mobile-header" style={{ display: 'none', padding: 'var(--spacing-lg)', background: 'var(--bg-card)', borderBottom: '1px solid var(--border)', alignItems: 'center', gap: 'var(--spacing-lg)' }}>
        <button className="btn" onClick={() => setIsSidebarOpen(true)} style={{ padding: 'var(--spacing-sm)', background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-main)' }}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="var(--text-main)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="3" y1="12" x2="21" y2="12"></line>
            <line x1="3" y1="6" x2="21" y2="6"></line>
            <line x1="3" y1="18" x2="21" y2="18"></line>
          </svg>
        </button>
        <span style={{ fontWeight: 'bold', fontSize: '16px', color: 'var(--text-main)' }}>Liferay Tunnel</span>
      </div>

      <div className="main-content">
        {user.broadcast_message && (
          <div style={{ background: 'var(--accent)', color: '#fff', padding: 'var(--spacing-md) var(--spacing-lg)', borderRadius: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xl)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
            <span style={{ fontSize: '18px' }}>📢</span>
            <div style={{ flex: 1, fontSize: '14px' }}>
              <strong>{t('broadcast_alert', 'System Broadcast')}:</strong> {user.broadcast_message}
            </div>
          </div>
        )}

        {user.targeted_message && (
          <div style={{ background: 'var(--primary)', color: '#fff', padding: 'var(--spacing-md) var(--spacing-lg)', borderRadius: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xl)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
            <span style={{ fontSize: '18px' }}>💬</span>
            <div style={{ flex: 1, fontSize: '14px' }}>
              <strong>{t('admin_message', 'Admin Message')}:</strong> {user.targeted_message}
            </div>
            <button onClick={dismissTargetedMessage} className="btn" style={{ background: 'rgba(0,0,0,0.2)', color: 'white', border: 'none', padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '12px' }}>
              {t('dismiss', 'Dismiss')}
            </button>
          </div>
        )}

        <header className="content-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-2xl)' }}>
          <div>
            <p style={{ margin: 0, color: 'var(--text-muted)' }}>{t('welcome_back', 'Welcome back')}, {user.first_name}</p>
            {user.last_login_at && !user.last_login_at.startsWith('0001') && (
              <p style={{ margin: 'var(--spacing-xs) 0 0 0', color: 'var(--text-muted)', fontSize: '13px' }}>
                Last login: {new Date(user.last_login_at).toLocaleString()} from <code style={{background: 'rgba(0,0,0,0.1)', padding: '2px 4px', borderRadius: '4px'}}>{user.last_login_ip || 'Unknown'}</code>
              </p>
            )}
          </div>
          <div style={{ textAlign: 'right' }}>
            <a href="https://status.lfr-demo.se/" target="_blank" rel="noopener noreferrer" style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', justifyContent: 'flex-end', marginBottom: 'var(--spacing-xs)', textDecoration: 'none' }}>
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--success)' }}></div>
              <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--text-main)' }}>{t('system_online', 'System Online')}</span>
            </a>
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
