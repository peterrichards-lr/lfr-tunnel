import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
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
  const [serverConfig, setServerConfig] = useState<any>(null);
  const [copiedUpgrade, setCopiedUpgrade] = useState(false);
  const [isCreateTokenModalOpen, setIsCreateTokenModalOpen] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
  const [newTokenExpiresDays, setNewTokenExpiresDays] = useState(30);
  const [generatedToken, setGeneratedToken] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);
  const { formatDate } = useSettings();
  const { t } = useI18n();

  const columns: ColumnDef<any>[] = useMemo(() => [
    { key: 'name', label: t('name', 'Name'), sortable: true },
    { key: 'token_prefix', label: t('prefix', 'Prefix'), sortable: true },
    { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
    { key: 'created_at', label: t('created', 'Created'), sortable: true }
  ], [t]);

  const isTokenRevoked = (tItem: any) => {
    if (!tItem.revoked_at) return false;
    const d = new Date(tItem.revoked_at).getTime();
    return !isNaN(d) && d > 946684800000;
  };

  const isTokenExpired = (tItem: any) => {
    if (!tItem.expires_at) return false;
    const d = new Date(tItem.expires_at).getTime();
    return !isNaN(d) && d > 946684800000 && d <= Date.now();
  };

  const mappedTokens = useMemo(() => {
    return tokens.map(tItem => {
      const statusLabel = isTokenRevoked(tItem) ? 'revoked' : (isTokenExpired(tItem) ? 'expired' : 'active');
      return {
        ...tItem,
        computed_status: statusLabel
      };
    });
  }, [tokens]);

  const statusOptions = useMemo(() => [
    { value: 'active', label: t('status_active', 'active') },
    { value: 'expired', label: t('status_expired', 'expired') },
    { value: 'revoked', label: t('status_revoked', 'revoked') }
  ], [t]);

  const {
    paginatedItems: paginatedTokens,
    currentPage,
    totalPages,
    totalItems,
    pageSize,
    setCurrentPage,
    setPageSize,
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    requestSort,
    getSortIndicator,
    getAriaSort,
    isColumnVisible,
    toggleColumn,
    allColumns
  } = useDataTable<any>(
    'dashboard_pat_tokens',
    mappedTokens,
    ['name', 'token_prefix'],
    columns,
    10,
    ['created_at'],
    'computed_status',
    statusOptions,
    'active'
  );

  const formatRelativeExpiry = (expiresAt: string | null | undefined): { label: string; color: string } => {
    if (!expiresAt) return { label: t('never', 'Never'), color: 'var(--text-muted)' };
    const diff = new Date(expiresAt).getTime() - Date.now();
    if (diff <= 0) return { label: t('expired', 'Expired'), color: 'var(--status-danger-text)' };
    const days = Math.floor(diff / 86400000);
    const hours = Math.floor((diff % 86400000) / 3600000);
    if (days > 30) return { label: `${t('in', 'in')} ${days}d`, color: 'var(--text-muted)' };
    if (days > 0)  return { label: `${t('in', 'in')} ${days}d ${hours}h`, color: 'var(--status-warning-text)' };
    return { label: `${t('in', 'in')} ${hours}h`, color: 'var(--status-danger-text)' };
  };

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

  const handleRevokeToken = async (tokenId: string, tokenName: string) => {
    if (!confirm(`Revoke token "${tokenName}"? This action cannot be undone.`)) return;
    try {
      await axios.delete(`/api/tokens/${tokenId}`);
      fetchTokens();
    } catch (err: any) {
      alert(err.response?.data?.error || 'Failed to revoke token');
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

      <div className="mb-2xl">
        <h1 id="dashboard-overview" className="text-2xl fw-extrabold tracking-tight mb-xs">{t('dashboard_overview', 'Dashboard Overview')}</h1>
        <p className="text-muted text-base">{t('dashboard_desc', 'Manage your active tunnels, domains, and tokens.')}</p>
      </div>

      {user?.last_client_version && serverConfig?.latest_version && isOlderVersion(user.last_client_version, serverConfig.latest_version) && (
        <div className="alert-banner alert-banner--warning flex-wrap gap-md items-center justify-between mb-xl">
          <div className="flex items-center gap-md">
            <span className="text-lg">🚀</span>
            <div>
              <strong className="block text-sm fw-bold">
                {t('update_available', 'Update Available')} ({serverConfig.latest_version})
              </strong>
              <span className="text-xs opacity-80">
                {t('older_client_warn', 'You are currently running version')} {user.last_client_version}. {t('please_update_client', 'Please update to get the latest features and security improvements.')}
              </span>
            </div>
          </div>
          <div className="flex gap-md items-center">
            <div className="flex items-center bg-black/30 rounded border px-md py-xs gap-sm">
              <code className="text-xs font-mono text-main">
                lfr-tunnel -upgrade
              </code>
              <button 
                onClick={handleCopyUpgradeCmd}
                className="btn-text p-xs text-xs rounded hover:bg-white/10 transition-colors"
                style={{ color: copiedUpgrade ? 'var(--success)' : 'var(--text-muted)' }}
                title={copiedUpgrade ? 'Copied!' : 'Copy to clipboard'}
              >
                {copiedUpgrade ? '✓' : '📋'}
              </button>
            </div>
            <button 
              className="btn btn-secondary py-xs px-md text-xs w-auto" 
              onClick={() => setIsInstallModalOpen(true)}
            >
              {t('view_guide', 'View Guide')}
            </button>
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-xl items-start mb-2xl">
        <div className="lg:col-span-2 flex flex-col gap-xl">
          <ReservationsPanel />
          
          <div className="card p-0" style={{ animationDelay: '0.2s' }}>
            <div className="p-xl border-b flex justify-between items-center flex-wrap gap-md">
              <div>
                <h3 className="m-0 text-md fw-bold">{t('pat_title', 'Personal Access Tokens')}</h3>
                <p className="text-muted text-sm mt-xs m-0">
                  {t('pat_desc', 'Authenticate your CLI client securely without a browser.')}
                </p>
              </div>
              <div className="flex gap-md">
                {tokens.length > 0 && (
                  <button type="button" className="btn btn-outline py-sm px-lg text-sm" onClick={handleExportCSV}>
                    {t('export_csv', 'Export CSV')}
                  </button>
                )}
                <button 
                  type="button"
                  className="btn btn-outline py-sm px-lg text-sm" 
                  onClick={() => {
                    setNewTokenName('');
                    setNewTokenExpiresDays(30);
                    setGeneratedToken(null);
                    setIsCreateTokenModalOpen(true);
                  }}
                >
                  {t('generate_token', 'Generate Token')}
                </button>
              </div>
            </div>
            
            {tokens.length > 0 && (
              <div className="p-md border-b">
                <DataTableToolbar
                  searchQuery={searchQuery}
                  onSearchChange={setSearchQuery}
                  searchPlaceholder={t('search_tokens_placeholder', 'Search tokens...')}
                  pageSize={pageSize}
                  onPageSizeChange={setPageSize}
                  columns={allColumns}
                  isColumnVisible={isColumnVisible}
                  onToggleColumn={toggleColumn}
                  statusFilter={statusFilter}
                  onStatusFilterChange={setStatusFilter}
                  statusOptions={statusOptions}
                />
              </div>
            )}
            
            {tokens.length === 0 ? (
              <div className="card text-center p-2xl border-dashed">
                <div className="text-muted text-base">{t('no_active_tokens', 'No active tokens found. Create one to authenticate your CLI.')}</div>
              </div>
            ) : (
              <>
                <div className="table-responsive">
                  <table className="w-full">
                    <thead>
                      <tr className="border-b text-left">
                        {isColumnVisible('name') && <th className="th-col th-col--sortable" onClick={() => requestSort('name')} aria-sort={getAriaSort('name')}>{t('name', 'Name')}{getSortIndicator('name')}</th>}
                        {isColumnVisible('token_prefix') && <th className="th-col th-col--sortable" onClick={() => requestSort('token_prefix')} aria-sort={getAriaSort('token_prefix')}>{t('prefix', 'Prefix')}{getSortIndicator('token_prefix')}</th>}
                        {isColumnVisible('created_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>{t('created', 'Created')}{getSortIndicator('created_at')}</th>}
                        {isColumnVisible('expires_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>{t('expires', 'Expires')}{getSortIndicator('expires_at')}</th>}
                        <th className="th-col">{t('status', 'Status')}</th>
                        <th className="th-col">{t('expires_in', 'Expires In')}</th>
                        <th className="th-col">{t('actions', 'Actions')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {paginatedTokens.map((tItem: any, idx: number) => {
                        const expiryInfo = formatRelativeExpiry(tItem.expires_at);
                        const statusVal = tItem.computed_status || 'active';
                        const badgeClass = statusVal === 'active' ? 'badge-success' : statusVal === 'expired' ? 'badge-warning' : 'badge-danger';
                        return (
                        <tr key={idx} className="border-b">
                          {isColumnVisible('name') && <td className="td-cell fw-medium">{tItem.name}</td>}
                          {isColumnVisible('token_prefix') && <td className="td-cell--mono">{tItem.token_prefix}...</td>}
                          {isColumnVisible('created_at') && <td className="td-cell">{formatDate(tItem.created_at)}</td>}
                          {isColumnVisible('expires_at') && <td className="td-cell text-muted">{formatDate(tItem.expires_at)}</td>}
                          <td className="td-cell">
                            <span className={`badge ${badgeClass}`}>
                              {statusVal}
                            </span>
                          </td>
                          <td className="td-cell">
                            <span className="text-sm fw-medium" style={{ color: expiryInfo.color }}>{expiryInfo.label}</span>
                          </td>
                          <td className="td-cell">
                            {statusVal === 'active' && (
                              <button
                                type="button"
                                className="btn btn-outline-danger py-xs px-sm text-xs w-auto"
                                onClick={() => handleRevokeToken(tItem.id, tItem.name)}
                              >
                                {t('revoke', 'Revoke')}
                              </button>
                            )}
                          </td>
                        </tr>
                       );
                      })}
                    </tbody>
                  </table>
                </div>
                <DataTablePagination
                  currentPage={currentPage}
                  totalPages={totalPages}
                  pageSize={pageSize}
                  totalItems={totalItems}
                  onPageChange={setCurrentPage}
                />
              </>
            )}
          </div>

          <TunnelsPanel tunnels={user.tunnels || []} serverConfig={serverConfig} user={user} />
        </div>
        
        <div className="flex flex-col gap-xl">
          <WhatsNewPanel />
          
          <div className="card">
            <h3 className="section-title mb-lg">{t('help_resources', 'Help & Resources')}</h3>
            <div className="flex flex-col gap-md">
              <button type="button" className="btn btn-outline justify-start text-left" onClick={() => setIsInstallModalOpen(true)}>
                💻 {t('guide_title', 'Client Installation Guide')}
              </button>
              <button type="button" className="btn btn-outline justify-start text-left" onClick={() => {
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
        <div className="modal-backdrop">
          <div 
            className="modal-card modal-card--sm"
            role="dialog"
            aria-modal="true"
            aria-labelledby="generate-token-title"
          >
            <div className="modal-header">
              <h3 id="generate-token-title" className="modal-title">{t('generate_new_token', 'Generate Personal Access Token')}</h3>
              <button type="button" onClick={() => setIsCreateTokenModalOpen(false)} className="modal-close" aria-label={t('close', 'Close')}>✕</button>
            </div>

            {!generatedToken ? (
              <form onSubmit={handleCreateToken}>
                <div className="form-group mb-lg">
                  <label className="form-label">
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

                <div className="form-group mb-xl">
                  <label className="form-label">
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

                <div className="modal-footer">
                  <button type="button" className="btn btn-secondary w-auto" onClick={() => setIsCreateTokenModalOpen(false)}>
                    {t('cancel', 'Cancel')}
                  </button>
                  <button type="submit" className="btn btn-primary w-auto" disabled={generating}>
                    {generating ? t('generating', 'Generating...') : t('generate', 'Generate')}
                  </button>
                </div>
              </form>
            ) : (
              <div style={{ animation: 'fadeInUp 0.3s ease-out' }}>
                <div className="alert-banner alert-banner--warning text-xs mb-xl">
                  ⚠️ {t('token_warning', 'Copy this token now! It will not be shown again for security reasons.')}
                </div>
                
                <div className="flex gap-sm mb-xl">
                  <input 
                    type="text" 
                    className="input-field font-mono text-xs mb-0 w-full" 
                    readOnly 
                    value={generatedToken} 
                  />
                  <button 
                    type="button" 
                    className="btn btn-primary px-lg w-auto" 
                    onClick={() => {
                      navigator.clipboard.writeText(generatedToken);
                      alert('Token copied to clipboard!');
                    }}
                  >
                    {t('copy', 'Copy')}
                  </button>
                </div>

                <div className="modal-footer">
                  <button type="button" className="btn btn-secondary w-auto" onClick={() => setIsCreateTokenModalOpen(false)}>
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
