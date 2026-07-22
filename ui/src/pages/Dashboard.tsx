import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useTableSort } from '../hooks/useTableSort';
import TunnelsPanel from '../components/TunnelsPanel';
import ReservationsPanel from '../components/ReservationsPanel';
import WhatsNewPanel from '../components/WhatsNewPanel';
import ClientInstallationModal from '../components/ClientInstallationModal';
import OnboardingTour from '../components/OnboardingTour';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';

export default function Dashboard() {
  const { user } = useOutletContext<{ user: any }>();
  const [tokens, setTokens] = useState<any[]>([]);
  const [isInstallModalOpen, setIsInstallModalOpen] = useState(false);
  const [serverConfig, setServerConfig] = useState<any>(null); // Kept for modal if needed, though not fetched here currently
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { items: sortedTokens, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tokens, ['token', 'status']);

  useEffect(() => {
    axios.get('/api/tokens')
      .then(res => setTokens(res.data || []))
      .catch(err => console.error("Failed to fetch tokens", err));
    axios.get('/api/analytics/ping?portal=v2').catch(() => {});
    axios.get('/api/version').then(res => setServerConfig(res.data)).catch(() => {});
  }, [user]);

  const handleExportCSV = () => {
    try {
      let csv = "ID,Name,Prefix,Owner,ExpiresAt,CreatedAt\n";
      tokens.forEach(item => {
        const row = [
          item.id,
          item.name,
          item.token_prefix,
          item.user_id || "",
          item.expires_at || "Never",
          item.created_at || ""
        ].map(val => `"${String(val).replace(/"/g, '""')}"`).join(",");
        csv += row + "\n";
      });
      
      const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.setAttribute("download", "personal_access_tokens.csv");
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
    } catch (e) {
      console.error("Failed to export CSV", e);
    }
  };

  return (
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>

      <div style={{ marginBottom: '32px' }}>
        <h1 id="dashboard-overview" style={{ fontSize: '32px', fontWeight: 800, letterSpacing: '-1px', marginBottom: '8px' }}>{t('dashboard_overview', 'Dashboard Overview')}</h1>
        <p style={{ color: 'var(--text-muted)', fontSize: '16px' }}>{t('dashboard_desc', 'Manage your active tunnels, domains, and tokens.')}</p>
      </div>

      <div className="responsive-grid" style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: '24px', alignItems: 'start', marginBottom: '24px' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          <ReservationsPanel />
          
          <div className="card" style={{ animationDelay: '0.2s' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
              <div>
                <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>{t('pat_title', 'Personal Access Tokens')}</h3>
                <p style={{ color: 'var(--text-muted)', margin: '4px 0 0 0', fontSize: '14px' }}>
                  {t('pat_desc', 'Authenticate your CLI client securely without a browser.')}
                </p>
              </div>
              <div style={{ display: 'flex', gap: '12px' }}>
                {tokens.length > 0 && (
                  <button className="btn btn-outline" onClick={handleExportCSV} style={{ fontSize: '14px', padding: '8px 16px' }}>
                    {t('export_csv', 'Export CSV')}
                  </button>
                )}
                <button className="btn btn-outline" style={{ fontSize: '14px', padding: '8px 16px' }}>{t('generate_token', 'Generate Token')}</button>
              </div>
            </div>
            
            {tokens.length > 0 && (
              <div style={{ marginBottom: '16px' }}>
                <input 
                  type="text" 
                  placeholder="Search tokens..." 
                  value={searchQuery} 
                  onChange={e => setSearchQuery(e.target.value)}
                  style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
                />
              </div>
            )}
            
            {tokens.length === 0 ? (
              <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
                <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>{t('no_active_tokens', 'No active tokens found. Create one to authenticate your CLI.')}</div>
              </div>
            ) : (
              <div className="table-responsive">
                <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                  <thead>
                    <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                      <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('token')} aria-sort={getAriaSort('token')}>{t('token', 'Token')}{getSortIndicator('token')}</th>
                      <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>{t('created', 'Created')}{getSortIndicator('created_at')}</th>
                      <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>{t('expires', 'Expires')}{getSortIndicator('expires_at')}</th>
                      <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>{t('status', 'Status')}{getSortIndicator('status')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sortedTokens.map((tItem, idx) => (
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

          <TunnelsPanel tunnels={user.tunnels || []} serverConfig={serverConfig} user={user} />
        </div>
        
        <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          <WhatsNewPanel />
          
          <div className="card">
            <h3 style={{ margin: '0 0 16px 0', fontSize: '20px', fontWeight: 700 }}>{t('help_resources', 'Help & Resources')}</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              <button className="btn btn-outline" style={{ justifyContent: 'flex-start', textAlign: 'left' }} onClick={() => setIsInstallModalOpen(true)}>
                💻 {t('guide_title', 'Client Installation Guide')}
              </button>
              <button className="btn btn-outline" style={{ justifyContent: 'flex-start', textAlign: 'left' }} onClick={() => {
                // Trigger a global event to start the tour
                window.dispatchEvent(new CustomEvent('start-onboarding-tour'));
              }}>
                🧭 {t('onboarding_guide_title', 'Run Dashboard Onboarding Tour')}
              </button>
            </div>
          </div>
        </div>
      </div>

      <ClientInstallationModal isOpen={isInstallModalOpen} onClose={() => setIsInstallModalOpen(false)} serverConfig={serverConfig} />
      <OnboardingTour user={user} />
    </div>
  );
}
