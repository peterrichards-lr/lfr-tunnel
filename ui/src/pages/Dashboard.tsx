import { useEffect, useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import TunnelsPanel from '../components/TunnelsPanel';
import ReservationsPanel from '../components/ReservationsPanel';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';

export default function Dashboard() {
  const { user } = useOutletContext<{ user: any }>();
  const [tokens, setTokens] = useState<any[]>([]);
  const { formatDate } = useSettings();
  const { t } = useI18n();

  useEffect(() => {
    setTokens(user.tokens || []);
  }, [user]);

  return (
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <div style={{ marginBottom: '32px' }}>
        <h1 style={{ fontSize: '32px', fontWeight: 800, letterSpacing: '-1px', marginBottom: '8px' }}>{t('dashboard_overview', 'Dashboard Overview')}</h1>
        <p style={{ color: 'var(--text-muted)', fontSize: '16px' }}>{t('dashboard_desc', 'Manage your active tunnels, domains, and tokens.')}</p>
      </div>

      <TunnelsPanel tunnels={user.tunnels || []} />
      
      <ReservationsPanel />

      <div className="card" style={{ marginBottom: '24px', animationDelay: '0.2s' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <div>
            <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>{t('pat_title', 'Personal Access Tokens')}</h3>
            <p style={{ color: 'var(--text-muted)', margin: '4px 0 0 0', fontSize: '14px' }}>
              {t('pat_desc', 'Authenticate your CLI client securely without a browser.')}
            </p>
          </div>
          <button className="btn btn-outline" style={{ fontSize: '14px', padding: '8px 16px' }}>{t('generate_token', 'Generate Token')}</button>
        </div>
        
        {tokens.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
            <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>{t('no_active_tokens', 'No active tokens found. Create one to authenticate your CLI.')}</div>
          </div>
        ) : (
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('token', 'Token')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('created', 'Created')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('expires', 'Expires')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('status', 'Status')}</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((tItem, idx) => (
                  <tr key={idx} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s', cursor: 'pointer' }} onMouseOver={e => e.currentTarget.style.background = 'rgba(255,255,255,0.03)'} onMouseOut={e => e.currentTarget.style.background = 'transparent'}>
                    <td style={{ padding: '16px', fontFamily: 'monospace', fontWeight: 500, fontSize: '14px' }}>{tItem.token}</td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>{formatDate(tItem.created_at)}</td>
                    <td style={{ padding: '16px', fontSize: '14px', color: 'var(--text-muted)' }}>{formatDate(tItem.expires_at)}</td>
                    <td style={{ padding: '16px' }}>
                      <span style={{ 
                        padding: '4px 12px', 
                        borderRadius: '20px', 
                        fontSize: '12px', 
                        fontWeight: 600, 
                        background: tItem.status === 'active' ? 'rgba(16, 185, 129, 0.15)' : 'rgba(239, 68, 68, 0.15)',
                        color: tItem.status === 'active' ? '#34d399' : '#f87171',
                        border: `1px solid ${tItem.status === 'active' ? 'rgba(16, 185, 129, 0.3)' : 'rgba(239, 68, 68, 0.3)'}`
                      }}>
                        {tItem.status.toUpperCase()}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
