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
  node_id?: string;
}

export default function AdminSubdomains() {
  const [subdomains, setSubdomains] = useState<SubdomainInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();
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

  const { items: sortedSubdomains, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(subdomains, ['subdomain', 'full_host', 'user_email', 'client_ip']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xl)' }}>
          <Skeleton width={180} height={28} />
        </div>
        
        <div className="card" style={{ padding: 'var(--spacing-xl)', marginBottom: 'var(--spacing-xl)' }}>
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center' }}>
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card" style={{ padding: 'var(--spacing-xl)' }}>
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={100} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={60} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={120} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="90%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="85%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="70%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="80%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="50%" height={16} /></td>
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xl)' }}>
        <h3>Registered Subdomains</h3>
        <a href="/api/admin/leases/export" className="btn btn-secondary">Export CSV</a>
      </div>
      <div style={{ marginBottom: 'var(--spacing-lg)' }}>
        <input 
          type="text" 
          placeholder={t('search_subdomains_placeholder', 'Search subdomains...')} 
          value={searchQuery} 
          onChange={e => { setSearchQuery(e.target.value); setPage(0); }}
          style={{ padding: 'var(--spacing-sm) var(--spacing-md)', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
        />
      </div>
      <div className="card" style={{ padding: '0' }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>Subdomain{getSortIndicator('subdomain')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>Target Host{getSortIndicator('full_host')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>Owner{getSortIndicator('user_email')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('is_online')} aria-sort={getAriaSort('is_online')}>Status{getSortIndicator('is_online')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>Node{getSortIndicator('node_id')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>Client IP{getSortIndicator('client_ip')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('bytes_in')} aria-sort={getAriaSort('bytes_in')}>Bytes In{getSortIndicator('bytes_in')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('bytes_out')} aria-sort={getAriaSort('bytes_out')}>Bytes Out{getSortIndicator('bytes_out')}</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedSubdomains.length === 0 ? (
                <tr>
                  <td colSpan={9} style={{ textAlign: 'center', padding: 'var(--spacing-xl)' }}>No registered subdomains found</td>
                </tr>
              ) : (
                sortedSubdomains.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE).map((sub) => (
                  <tr key={sub.id}>
                    <td style={{ fontWeight: 500 }}>{sub.subdomain || '(wildcard)'}</td>
                    <td>
                      <a href={`https://${sub.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none' }}>
                        {sub.full_host}
                      </a>
                    </td>
                    <td>{sub.user_email}</td>
                    <td>
                      <span className="badge" style={{ 
                        background: sub.is_online ? 'var(--status-success-bg)' : 'rgba(255, 255, 255, 0.05)', 
                        color: sub.is_online ? 'var(--status-success-text)' : 'var(--text-muted)',
                        border: `1px solid ${sub.is_online ? 'var(--status-success-border)' : 'var(--border)'}`
                      }}>
                        {sub.is_online ? '🟢 Online' : '⚪ Offline'}
                      </span>
                    </td>
                    <td>
                      {sub.is_online ? (
                        sub.node_id && sub.node_id !== 'control' ? (
                          <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                            🌍 {sub.node_id}
                          </span>
                        ) : (
                          <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                            🇬🇧 Control
                          </span>
                        )
                      ) : '-'}
                    </td>
                    <td>{sub.client_ip}</td>
                    <td>{formatBytes(sub.bytes_in || 0)}</td>
                    <td>{formatBytes(sub.bytes_out || 0)}</td>
                    <td>
                      <button 
                        className="btn btn-danger" 
                        disabled={!sub.is_online}
                        style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} 
                        onClick={() => kickLease(sub.subdomain)}
                      >
                        Kick
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
          
          {sortedSubdomains.length > 0 && (
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--spacing-lg)', borderTop: '1px solid var(--border-color)' }}>
              <div style={{ fontSize: '13px', color: 'var(--text-muted)' }}>
                Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedSubdomains.length)} of {sortedSubdomains.length}
              </div>
              <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                <button 
                  className="btn btn-secondary" 
                  onClick={() => setPage(0)}
                  disabled={page === 0}
                  style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '13px' }}
                >
                  First
                </button>
                <button 
                  className="btn btn-secondary" 
                  disabled={page === 0} 
                  onClick={() => setPage(page - 1)}
                  style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '13px' }}
                >
                  Previous
                </button>
                <span style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }}>Page {page + 1} of {Math.ceil(sortedSubdomains.length / ROWS_PER_PAGE)}</span>
                <button 
                  className="btn btn-secondary" 
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedSubdomains.length} 
                  onClick={() => setPage(page + 1)}
                  style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '13px' }}
                >
                  Next
                </button>
                <button 
                  className="btn btn-secondary" 
                  onClick={() => setPage(Math.max(0, Math.ceil(sortedSubdomains.length / ROWS_PER_PAGE) - 1))}
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedSubdomains.length}
                  style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '13px' }}
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

