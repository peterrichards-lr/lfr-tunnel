import { useEffect, useState } from 'react';
import axios from 'axios';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

interface TunnelLease {
  user_id: string;
  subdomain_prefix: string;
  full_host: string;
  local_port: number;
  client_ip: string;
  status: string;
  bytes_in: number;
  bytes_out: number;
  created_at: string;
  node_id?: string;
  rate_limit?: number;
}

export default function AdminSubdomains() {
  const [leases, setLeases] = useState<TunnelLease[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 15;

  const fetchLeases = async () => {
    try {
      const res = await axios.get('/api/admin/leases');
      setLeases(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchLeases();
    const interval = setInterval(fetchLeases, 5000);
    return () => clearInterval(interval);
  }, []);

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const kickLease = async (subdomain: string) => {
    if (!confirm(`Are you sure you want to kick the tunnel for ${subdomain}?`)) return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
      fetchLeases();
    } catch {
      alert('Failed to kick lease');
    }
  };

  const { items: sortedLeases, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(leases, ['subdomain_prefix', 'full_host', 'node_id', 'client_ip']);
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
        <h3>Active Subdomains</h3>
      </div>
      <div style={{ marginBottom: 'var(--spacing-lg)' }}>
        <input 
          type="text" 
          placeholder="Search subdomains..." 
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
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('subdomain_prefix')} aria-sort={getAriaSort('subdomain_prefix')}>Subdomain{getSortIndicator('subdomain_prefix')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>Target Host{getSortIndicator('full_host')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>Node{getSortIndicator('node_id')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>Client IP{getSortIndicator('client_ip')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('bytes_in')} aria-sort={getAriaSort('bytes_in')}>Bytes In{getSortIndicator('bytes_in')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('bytes_out')} aria-sort={getAriaSort('bytes_out')}>Bytes Out{getSortIndicator('bytes_out')}</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {leases.length === 0 ? (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', padding: 'var(--spacing-xl)' }}>No active tunnels</td>
                </tr>
              ) : (
                sortedLeases.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE).map((lease) => (
                  <tr key={lease.subdomain_prefix}>
                    <td style={{ fontWeight: 500 }}>{lease.subdomain_prefix}</td>
                    <td>
                      <a href={`https://${lease.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none' }}>
                        {lease.full_host}
                      </a>
                    </td>
                    <td>
                      {lease.node_id && lease.node_id !== 'control' ? (
                        <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                          🌍 {lease.node_id}
                        </span>
                      ) : (
                        <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                          🇬🇧 Control
                        </span>
                      )}
                    </td>
                    <td>{lease.client_ip}</td>
                    <td>{formatBytes(lease.bytes_in || 0)}</td>
                    <td>{formatBytes(lease.bytes_out || 0)}</td>
                    <td>
                      <button className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => kickLease(lease.subdomain_prefix)}>
                        Kick
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
          
          {sortedLeases.length > 0 && (
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--spacing-lg)', borderTop: '1px solid var(--border-color)' }}>
              <div style={{ fontSize: '13px', color: 'var(--text-muted)' }}>
                Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedLeases.length)} of {sortedLeases.length}
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
                <span style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }}>Page {page + 1} of {Math.ceil(sortedLeases.length / ROWS_PER_PAGE)}</span>
                <button 
                  className="btn btn-secondary" 
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedLeases.length} 
                  onClick={() => setPage(page + 1)}
                  style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '13px' }}
                >
                  Next
                </button>
                <button 
                  className="btn btn-secondary" 
                  onClick={() => setPage(Math.max(0, Math.ceil(sortedLeases.length / ROWS_PER_PAGE) - 1))}
                  disabled={(page + 1) * ROWS_PER_PAGE >= sortedLeases.length}
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
