import { useEffect, useState } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import axios from 'axios';
import Sidebar from './Sidebar';
import ScrollToTopButton from './ScrollToTopButton';
import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';


export default function Layout() {
  const [user, setUser] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [uptime, setUptime] = useState<string>('');
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const navigate = useNavigate();
  const { t } = useI18n();
  const { formatDate } = useSettings();


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
    return <div id="loader" className="flex items-center justify-center min-h-screen"><div className="spinner"></div></div>;
  }

  if (!user) return null;

  return (
    <div className="flex flex-col min-h-screen w-full">
      {showV1Promo && (
        <div className="bg-primary text-white py-xs px-xl text-center z-40 box-border shadow-sm flex items-center justify-center relative shrink-0">
          <p className="m-0 text-sm fw-medium">
            {t('banner_legacy_interface', 'Need the legacy interface?')} <a href="/portal/" className="text-white underline fw-bold ml-xs">{t('btn_switch_v1', 'Switch back to V1 →')}</a>
          </p>
          <button 
            onClick={dismissV1Promo} 
            className="btn-text absolute right-xl top-1/2 -translate-y-1/2 text-white text-lg p-xs leading-none hover:opacity-80 transition-opacity"
            style={{ background: 'none', border: 'none', cursor: 'pointer' }}
            title="Dismiss promo banner"
          >
            &times;
          </button>
        </div>
      )}

      <div id="dashboard-screen" className="flex flex-1 transition-all duration-200">
        <Sidebar user={user} isOpen={isSidebarOpen} onClose={() => setIsSidebarOpen(false)} />

      {/* Mobile Top Header */}
      <div className="mobile-header p-lg bg-card border-b items-center gap-lg">
        <button className="btn btn-secondary p-sm bg-transparent border text-main" onClick={() => setIsSidebarOpen(true)}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="3" y1="12" x2="21" y2="12"></line>
            <line x1="3" y1="6" x2="21" y2="6"></line>
            <line x1="3" y1="18" x2="21" y2="18"></line>
          </svg>
        </button>
        <span className="fw-bold text-base text-main">Liferay Tunnel</span>
      </div>

      <div className="main-content">
        {user.broadcast_message && (
          <div className="bg-accent text-white p-md px-lg rounded-sm mb-xl flex items-center gap-md shadow-md">
            <span className="text-lg">📢</span>
            <div className="flex-1 text-sm">
              <strong>{t('broadcast_alert', 'System Broadcast')}:</strong> {user.broadcast_message}
            </div>
          </div>
        )}

        {user.targeted_message && (
          <div className="bg-primary text-white p-md px-lg rounded-sm mb-xl flex items-center gap-md shadow-md">
            <span className="text-lg">💬</span>
            <div className="flex-1 text-sm">
              <strong>{t('admin_message', 'Admin Message')}:</strong> {user.targeted_message}
            </div>
            <button onClick={dismissTargetedMessage} className="btn btn-secondary bg-black/20 text-white border-none py-xs px-md text-xs">
              {t('dismiss', 'Dismiss')}
            </button>
          </div>
        )}

        <header className="content-header flex justify-between items-center mb-2xl">
          <div>
            <p className="m-0 text-muted">{t('welcome_back', 'Welcome back')}, {user.first_name}</p>
            {user.last_login_at && !user.last_login_at.startsWith('0001') && (
              <p className="mt-xs m-0 text-muted text-xs">
                Last login: {formatDate(user.last_login_at)} from <code className="bg-black/10 px-xs py-2xs rounded">{user.last_login_ip || 'Unknown'}</code>
              </p>
            )}
          </div>
          <div className="text-right">
            <a href="https://status.lfr-demo.se/" target="_blank" rel="noopener noreferrer" className="flex items-center gap-sm justify-end mb-xs no-underline">
              <div className="status-dot status-dot--online"></div>
              <span className="text-xs fw-semibold text-main">{t('system_online', 'System Online')}</span>
            </a>
            {uptime && <div className="text-xs text-muted">{t('uptime', 'Uptime')}: {uptime}</div>}
          </div>
        </header>
        <div>
          <Outlet context={{ user }} />
        </div>
        <ScrollToTopButton />
      </div>
    </div>
  </div>
);
}
