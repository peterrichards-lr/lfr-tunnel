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

function isOlderVersion(current: string, target: string): boolean {
  if (!current || !target) return false;
  const cParts = current.replace(/^v/, '').split('-')[0].split('.').map(n => parseInt(n) || 0);
  const tParts = target.replace(/^v/, '').split('-')[0].split('.').map(n => parseInt(n) || 0);
  for (let i = 0; i < Math.max(cParts.length, tParts.length); i++) {
    const cVal = cParts[i] || 0;
    const tVal = tParts[i] || 0;
    if (cVal < tVal) return true;
    if (cVal > tVal) return false;
  }
  return false;
}

export default function Dashboard() {
  const { user } = useOutletContext<{ user: any }>();
  const [tokens, setTokens] = useState<any[]>([]);
  const [isInstallModalOpen, setIsInstallModalOpen] = useState(false);
  const [serverConfig, setServerConfig] = useState<any>(null); // Kept for modal if needed, though not fetched here currently
  const [copiedUpgrade, setCopiedUpgrade] = useState(false);
  const [isCreateTokenModalOpen, setIsCreateTokenModalOpen] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
  const [newTokenExpiresDays, setNewTokenExpiresDays] = useState(30);
  const [generatedToken, setGeneratedToken] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { items: sortedTokens, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tokens, ['name', 'token_prefix', 'status']);

  const fetchTokens = () => {
    axios.get('/api/tokens')
      .then(res => setTokens(res.data || []))
      .catch(err => console.error("Failed to fetch tokens", err));
  };

  useEffect(() => {
    fetchTokens();
    axios.get('/api/analytics/ping?portal=v2').catch(() => {});
    axios.get('/api/version').then(res => setServerConfig(res.data)).catch(() => {});
  }, [user]);

  const handleCreateToken = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newTokenName.trim()) return;
    setGenerating(true);
    try {
      const res = await axios.post('/api/tokens', {
        name: newTokenName,
        expires_in_days: Number(newTokenExpiresDays)
      });
      setGeneratedToken(res.data.raw_token);
      fetchTokens();
    } catch (err: any) {
      console.error(err);
      alert(err.response?.data?.error || 'Failed to generate token');
    } finally {
      setGenerating(false);
    }
  };

  const handleExportCSV = () => {
    window.location.href = '/api/tokens/export';
  };

  const handleCopyUpgradeCmd = () => {
    navigator.clipboard.writeText('lfr-tunnel -upgrade');
    setCopiedUpgrade(true);
    setTimeout(() => setCopiedUpgrade(false), 2000);
  };

  return (
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>

      <div style={{ marginBottom: 'var(--spacing-2xl)' }}>
        <h1 id="dashboard-overview" style={{ fontSize: '32px', fontWeight: 800, letterSpacing: '-1px', marginBottom: 'var(--spacing-sm)' }}>{t('dashboard_overview', 'Dashboard Overview')}</h1>
        <p style={{ color: 'var(--text-muted)', fontSize: '16px' }}>{t('dashboard_desc', 'Manage your active tunnels, domains, and tokens.')}</p>
      </div>

      {user?.last_client_version && serverConfig?.latest_version && isOlderVersion(user.last_client_version, serverConfig.latest_version) && (
        <div style={{
          background: 'rgba(245, 158, 11, 0.12)',
          border: '1px solid rgba(245, 158, 11, 0.25)',
          color: '#fbbf24',
          padding: '16px 20px',
          borderRadius: '12px',
          marginBottom: '24px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: '12px',
          animation: 'fadeInUp 0.4s ease-out'
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
            <span style={{ fontSize: '20px' }}>🚀</span>
            <div>
              <strong style={{ display: 'block', fontSize: '15px', color: '#fbbf24' }}>
                {t('update_available', 'Update Available')} ({serverConfig.latest_version})
              </strong>
              <span style={{ fontSize: '13px', color: 'rgba(251, 191, 36, 0.8)' }}>
                {t('older_client_warn', 'You are currently running version')} {user.last_client_version}. {t('please_update_client', 'Please update to get the latest features and security improvements.')}
              </span>
            </div>
          </div>
          <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              background: 'rgba(0, 0, 0, 0.3)',
              borderRadius: '6px',
              border: '1px solid var(--border)',
              padding: '2px 8px 2px 12px'
            }}>
              <code style={{
                fontSize: '12px',
                fontFamily: 'monospace',
                color: 'var(--text-main)',
                marginRight: '8px',
                border: 'none',
                background: 'none',
                padding: 0
              }}>
                lfr-tunnel -upgrade
              </code>
              <button 
                onClick={handleCopyUpgradeCmd}
                style={{
                  background: 'none',
                  border: 'none',
                  color: copiedUpgrade ? 'var(--success)' : 'var(--text-muted)',
                  cursor: 'pointer',
                  padding: '4px',
                  fontSize: '12px',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  transition: 'color 0.2s'
                }}
                title={copiedUpgrade ? 'Copied!' : 'Copy to clipboard'}
              >
                {copiedUpgrade ? '✓' : '📋'}
              </button>
            </div>
            <button 
              className="btn btn-secondary" 
              style={{ padding: '6px 12px', fontSize: '12px', width: 'auto' }}
              onClick={() => setIsInstallModalOpen(true)}
            >
              {t('view_guide', 'View Guide')}
            </button>
          </div>
        </div>
      )}

      <div className="responsive-grid" style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 'var(--spacing-xl)', alignItems: 'start', marginBottom: 'var(--spacing-xl)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-xl)' }}>
          <ReservationsPanel />
          
          <div className="card" style={{ animationDelay: '0.2s' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xl)' }}>
              <div>
                <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>{t('pat_title', 'Personal Access Tokens')}</h3>
                <p style={{ color: 'var(--text-muted)', margin: 'var(--spacing-xs) 0 0 0', fontSize: '14px' }}>
                  {t('pat_desc', 'Authenticate your CLI client securely without a browser.')}
                </p>
              </div>
              <div style={{ display: 'flex', gap: 'var(--spacing-md)' }}>
                {tokens.length > 0 && (
                  <button className="btn btn-outline" onClick={handleExportCSV} style={{ fontSize: '14px', padding: 'var(--spacing-sm) var(--spacing-lg)' }}>
                    {t('export_csv', 'Export CSV')}
                  </button>
                )}
                <button 
                  className="btn btn-outline" 
                  onClick={() => {
                    setNewTokenName('');
                    setNewTokenExpiresDays(30);
                    setGeneratedToken(null);
                    setIsCreateTokenModalOpen(true);
                  }}
                  style={{ fontSize: '14px', padding: 'var(--spacing-sm) var(--spacing-lg)' }}
                >
                  {t('generate_token', 'Generate Token')}
                </button>
              </div>
            </div>
            
            {tokens.length > 0 && (
              <div style={{ marginBottom: 'var(--spacing-lg)' }}>
                <input 
                  type="text" 
                  placeholder={t('search_tokens_placeholder', 'Search tokens...')} 
                  value={searchQuery} 
                  onChange={e => setSearchQuery(e.target.value)}
                  style={{ padding: 'var(--spacing-sm) var(--spacing-md)', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
                />
              </div>
            )}
            
            {tokens.length === 0 ? (
              <div style={{ textAlign: 'center', padding: 'var(--spacing-3xl) var(--spacing-lg)', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: 'var(--spacing-md)' }}>
                <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>{t('no_active_tokens', 'No active tokens found. Create one to authenticate your CLI.')}</div>
              </div>
            ) : (
              <div className="table-responsive">
                <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                  <thead>
                    <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                      <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('name')} aria-sort={getAriaSort('name')}>{t('name', 'Name')}{getSortIndicator('name')}</th>
                      <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('token_prefix')} aria-sort={getAriaSort('token_prefix')}>{t('prefix', 'Prefix')}{getSortIndicator('token_prefix')}</th>
                      <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>{t('created', 'Created')}{getSortIndicator('created_at')}</th>
                      <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>{t('expires', 'Expires')}{getSortIndicator('expires_at')}</th>
                      <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>{t('status', 'Status')}{getSortIndicator('status')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sortedTokens.map((tItem, idx) => (
                      <tr key={idx} style={{ cursor: 'pointer' }}>
                        <td style={{ padding: 'var(--spacing-lg)', fontSize: '14px', fontWeight: 500 }}>{tItem.name}</td>
                        <td style={{ padding: 'var(--spacing-lg)', fontFamily: 'monospace', fontSize: '14px' }}>{tItem.token_prefix}...</td>
                        <td style={{ padding: 'var(--spacing-lg)', fontSize: '14px' }}>{formatDate(tItem.created_at)}</td>
                        <td style={{ padding: 'var(--spacing-lg)', fontSize: '14px', color: 'var(--text-muted)' }}>{formatDate(tItem.expires_at)}</td>
                        <td style={{ padding: 'var(--spacing-lg)' }}>
                          <span style={{ 
                            padding: 'var(--spacing-xs) var(--spacing-md)', 
                            borderRadius: '20px', 
                            fontSize: '12px', 
                            fontWeight: 600, 
                            background: (tItem.status || 'active') === 'active' ? 'var(--status-success-bg)' : 'var(--status-danger-bg)',
                            color: (tItem.status || 'active') === 'active' ? 'var(--status-success-text)' : 'var(--status-danger-text)',
                            border: `1px solid ${(tItem.status || 'active') === 'active' ? 'var(--status-success-border)' : 'var(--status-danger-border)'}`
                          }}>
                            {(tItem.status || 'active').toUpperCase()}
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
        
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-xl)' }}>
          <WhatsNewPanel />
          
          <div className="card">
            <h3 style={{ margin: '0 0 var(--spacing-lg) 0', fontSize: '20px', fontWeight: 700 }}>{t('help_resources', 'Help & Resources')}</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
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
      
      {isCreateTokenModalOpen && (
        <div style={{
          position: 'fixed', top: 0, left: 0, width: '100%', height: '100%',
          backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1000,
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 'var(--spacing-lg)'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '500px', background: 'var(--bg-base)', border: '1px solid var(--border)', borderRadius: '12px', padding: '24px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-lg)' }}>
              <h3 style={{ margin: 0 }}>{t('generate_new_token', 'Generate Personal Access Token')}</h3>
              <button onClick={() => setIsCreateTokenModalOpen(false)} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>

            {!generatedToken ? (
              <form onSubmit={handleCreateToken}>
                <div style={{ marginBottom: '16px' }}>
                  <label style={{ display: 'block', fontSize: '13px', color: 'var(--text-muted)', marginBottom: '8px' }}>
                    {t('token_name_label', 'Token Name / Description')}
                  </label>
                  <input 
                    type="text" 
                    className="input-field" 
                    required 
                    placeholder={t('token_name_placeholder', 'e.g. Work Laptop')} 
                    value={newTokenName}
                    onChange={(e) => setNewTokenName(e.target.value)}
                  />
                </div>

                <div style={{ marginBottom: '24px' }}>
                  <label style={{ display: 'block', fontSize: '13px', color: 'var(--text-muted)', marginBottom: '8px' }}>
                    {t('expiration', 'Expiration')}
                  </label>
                  <select 
                    className="input-field" 
                    value={newTokenExpiresDays} 
                    onChange={(e) => setNewTokenExpiresDays(Number(e.target.value))}
                  >
                    <option value={30}>30 Days</option>
                    <option value={90}>90 Days</option>
                    <option value={365}>365 Days</option>
                    {(user?.role === 'admin' || user?.role === 'owner') && (
                      <option value={0}>Never Expire</option>
                    )}
                  </select>
                </div>

                <div style={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
                  <button type="button" className="btn btn-secondary" onClick={() => setIsCreateTokenModalOpen(false)} style={{ width: 'auto' }}>
                    {t('cancel', 'Cancel')}
                  </button>
                  <button type="submit" className="btn btn-primary" disabled={generating} style={{ width: 'auto' }}>
                    {generating ? t('generating', 'Generating...') : t('generate', 'Generate')}
                  </button>
                </div>
              </form>
            ) : (
              <div style={{ animation: 'fadeInUp 0.3s ease-out' }}>
                <div className="alert alert-warning" style={{ marginBottom: '20px', fontSize: '13px' }}>
                  ⚠️ {t('token_warning', 'Copy this token now! It will not be shown again for security reasons.')}
                </div>
                
                <div style={{ display: 'flex', gap: '8px', marginBottom: '20px' }}>
                  <input 
                    type="text" 
                    className="input-field" 
                    readOnly 
                    value={generatedToken} 
                    style={{ marginBottom: 0, fontFamily: 'monospace', fontSize: '13px', width: '100%' }} 
                  />
                  <button 
                    type="button" 
                    className="btn btn-primary" 
                    style={{ width: 'auto', padding: '0 16px' }}
                    onClick={() => {
                      navigator.clipboard.writeText(generatedToken);
                      alert('Token copied to clipboard!');
                    }}
                  >
                    {t('copy', 'Copy')}
                  </button>
                </div>

                <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                  <button type="button" className="btn btn-secondary" onClick={() => setIsCreateTokenModalOpen(false)} style={{ width: 'auto' }}>
                    {t('close', 'Close')}
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      <OnboardingTour user={user} />
    </div>
  );
}
