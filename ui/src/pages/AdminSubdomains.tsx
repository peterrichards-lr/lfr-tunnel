import { useEffect, useState } from 'react';
import axios from 'axios';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface SubdomainInfo {
  id: number;
  user_id: string;
  user_email: string;
  subdomain: string;
  domain: string;
  full_host: string;
  expires_at: string;
  extension_requested: boolean;
  passcode: string;
  whitelist_ips: string;
  access_mode: string;
  created_at: string;
  updated_at: string;
  
  // Active lease status
  is_online: boolean;
  local_port?: number;
  client_ip?: string;
  bytes_in: number;
  bytes_out: number;
  rate_limit?: number;
  node_id?: string;
}

export default function AdminSubdomains() {
  const [subdomains, setSubdomains] = useState<SubdomainInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const { t } = useI18n();
  const { showToast, showConfirm, showPrompt } = useUI();
  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 15;

  const fetchSubdomains = async () => {
    try {
      const [subRes, leaseRes] = await Promise.all([
        axios.get('/api/admin/subdomains'),
        axios.get('/api/admin/leases')
      ]);
      
      const subs = subRes.data || [];
      const leases = leaseRes.data || [];
      
      const mapped = subs.map((sub: any) => {
        const fullHost = sub.subdomain ? `${sub.subdomain}.${sub.domain}` : sub.domain;
        const matchingLease = leases.find((l: any) => 
          (l.subdomain_prefix === sub.subdomain || (!sub.subdomain && !l.subdomain_prefix)) && 
          l.full_host.endsWith(sub.domain)
        );
        
        return {
          ...sub,
          full_host: fullHost,
          is_online: !!matchingLease,
          local_port: matchingLease?.local_port,
          client_ip: matchingLease?.client_ip || '-',
          bytes_in: matchingLease?.bytes_in || 0,
          bytes_out: matchingLease?.bytes_out || 0,
          rate_limit: matchingLease?.rate_limit || 0,
          node_id: matchingLease?.node_id
        };
      });
      
      setSubdomains(mapped);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSubdomains();
    const interval = setInterval(fetchSubdomains, 5000);
    return () => clearInterval(interval);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const kickLease = async (subdomain: string) => {
    const prefix = subdomain || "_";
    if (!(await showConfirm('Kick Tunnel', `Are you sure you want to kick the tunnel for ${subdomain || 'wildcard domain'}?`))) return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(prefix)}`);
      fetchSubdomains();
      showToast('Tunnel kicked successfully', 'success');
    } catch {
      showToast('Failed to kick lease', 'error');
    }
  };

  const throttleLease = async (host: string) => {
    const limitStr = await showPrompt('Throttle Tunnel', `Enter the max Requests Per Second (RPS) for ${host}. Enter 0 to remove limits.`, '0');
    if (limitStr === null) return;
    const limit = parseInt(limitStr, 10);
    if (isNaN(limit) || limit < 0) {
      showToast('Invalid rate limit', 'error');
      return;
    }
    try {
      await axios.post('/api/admin/leases/rate-limit', { host, rate_limit: limit });
      showToast('Rate limit updated successfully', 'success');
    } catch {
      showToast('Failed to update rate limit', 'error');
    }
  };

  const { items: sortedSubdomains, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(subdomains, ['subdomain', 'full_host', 'user_email', 'client_ip']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="page-header">
          <Skeleton width={180} height={28} />
        </div>
        
        <div className="card p-xl mb-xl">
          <div className="flex gap-md items-center">
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={60} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="80%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={16} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="page-header">
        <h3 className="page-header__title">Registered Subdomains</h3>
        <a 
          href="/api/admin/leases/export" 
          className="btn btn-secondary w-auto inline-flex items-center gap-sm" 
          style={{ whiteSpace: 'nowrap' }}
        >
          📥 {t('export_csv', 'Export CSV')}
        </a>
      </div>
      <div className="search-row">
        <input 
          type="text" 
          placeholder={t('search_subdomains_placeholder', 'Search subdomains...')} 
          value={searchQuery} 
          onChange={e => { setSearchQuery(e.target.value); setPage(0); }}
          className="search-input"
        />
      </div>
      <div className="card p-0">
        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>Subdomain{getSortIndicator('subdomain')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>Target Host{getSortIndicator('full_host')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>Owner{getSortIndicator('user_email')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('is_online')} aria-sort={getAriaSort('is_online')}>Status{getSortIndicator('is_online')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>Node{getSortIndicator('node_id')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>Client IP{getSortIndicator('client_ip')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_in')} aria-sort={getAriaSort('bytes_in')}>Bytes In{getSortIndicator('bytes_in')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_out')} aria-sort={getAriaSort('bytes_out')}>Bytes Out{getSortIndicator('bytes_out')}</th>
                <th className="th-col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedSubdomains.length === 0 ? (
                <tr>
                  <td colSpan={9} className="td-empty">No registered subdomains found</td>
                </tr>
              ) : (
                sortedSubdomains.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE).map((sub) => (
                  <tr key={sub.id} className="border-b">
                    <td className="td-cell fw-medium">{sub.subdomain || '(wildcard)'}</td>
                    <td className="td-cell">
                      <a href={`https://${sub.full_host}`} target="_blank" rel="noreferrer" className="text-primary no-underline fw-medium">
                        {sub.full_host}
                      </a>
                      {sub.rate_limit !== undefined && sub.rate_limit > 0 && (
                        <span className="badge ml-sm" style={{ backgroundColor: 'rgba(239, 68, 68, 0.15)', color: '#f87171', border: '1px solid rgba(239, 68, 68, 0.3)' }}>
                          ⏱️ {sub.rate_limit} RPS
                        </span>
                      )}
                    </td>
                    <td className="td-cell">{sub.user_email}</td>
                    <td className="td-cell">
                      <span className={`badge ${sub.is_online ? 'badge-success' : ''}`} style={!sub.is_online ? { background: 'rgba(255, 255, 255, 0.05)', color: 'var(--text-muted)', border: '1px solid var(--border)' } : {}}>
                        {sub.is_online ? '🟢 Online' : '⚪ Offline'}
                      </span>
                    </td>
                    <td className="td-cell">
                      {sub.is_online ? (
                        sub.node_id && sub.node_id !== 'control' ? (
                          <span className="badge badge-node">
                            🌍 {sub.node_id}
                          </span>
                        ) : (
                          <span className="badge badge-control">
                            🇬🇧 Control
                          </span>
                        )
                      ) : '-'}
                    </td>
                    <td className="td-cell--mono text-sm">{sub.client_ip}</td>
                    <td className="td-cell text-sm">{formatBytes(sub.bytes_in || 0)}</td>
                    <td className="td-cell text-sm">{formatBytes(sub.bytes_out || 0)}</td>
                    <td className="td-cell">
                      <div className="flex gap-sm">
                        <button 
                          className="btn btn-secondary py-xs px-sm text-xs" 
                          disabled={!sub.is_online}
                          onClick={() => throttleLease(sub.full_host)}
                        >
                          Throttle
                        </button>
                        <button 
                          className="btn btn-danger py-xs px-sm text-xs" 
                          disabled={!sub.is_online}
                          onClick={() => kickLease(sub.subdomain)}
                        >
                          Kick
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
          
          {sortedSubdomains.length > 0 && (
            <div className="pagination-row p-lg border-t">
              <div className="pagination-count">
                Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedSubdomains.length)} of {sortedSubdomains.length}
              </div>
              <div className="pagination-controls">
                <button 
                  className="btn btn-secondary py-xs px-md text-xs w-auto" 
                  onClick={() => setPage(0)}
                  disabled={page === 0}
                >
                  First
                </button>
                <button 
                  className="btn btn-secondary py-xs px-md text-xs w-auto" 
                  disabled={page === 0} 
                  onClick={() => setPage(page - 1)}
                >
                  Previous
                </button>
                <span className="pagination-page-label">Page {page + 1} of {Math.ceil(sortedSubdomains.length / ROWS_PER_PAGE)}</span>
                <button 
                  className="btn btn-secondary py-xs px-md text-xs w-auto" 
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedSubdomains.length} 
                  onClick={() => setPage(page + 1)}
                >
                  Next
                </button>
                <button 
                  className="btn btn-secondary py-xs px-md text-xs w-auto" 
                  onClick={() => setPage(Math.max(0, Math.ceil(sortedSubdomains.length / ROWS_PER_PAGE) - 1))}
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedSubdomains.length}
                >
                  Last
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

