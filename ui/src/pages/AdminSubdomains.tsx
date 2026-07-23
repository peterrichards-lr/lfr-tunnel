import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';

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

  const columns: ColumnDef<SubdomainInfo>[] = useMemo(() => [
    { key: 'subdomain', label: 'Subdomain', sortable: true },
    { key: 'full_host', label: 'Target Host', sortable: true },
    { key: 'user_email', label: 'Owner', sortable: true },
    { key: 'is_online', label: 'Status', sortable: true },
    { key: 'node_id', label: 'Node', sortable: true },
    { key: 'client_ip', label: 'Client IP', sortable: true },
    { key: 'bytes_in', label: 'Bytes In', sortable: true },
    { key: 'bytes_out', label: 'Bytes Out', sortable: true },
  ], []);

  const {
    paginatedItems: paginatedSubdomains,
    currentPage,
    totalPages,
    totalItems,
    pageSize,
    setCurrentPage,
    setPageSize,
    searchQuery,
    setSearchQuery,
    requestSort,
    getSortIndicator,
    getAriaSort,
    isColumnVisible,
    toggleColumn,
    allColumns
  } = useDataTable<SubdomainInfo>(
    'admin_subdomains',
    subdomains,
    ['subdomain', 'full_host', 'user_email', 'client_ip', 'node_id'],
    columns,
    10
  );

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

  const throttleLease = async (fullHost: string) => {
    const rate = await showPrompt('Rate Limit Lease', 'Set max requests per second (0 to remove limit):', '10');
    if (rate === null) return;
    try {
      await axios.post('/api/admin/leases/throttle', { full_host: fullHost, rate_limit: parseInt(rate, 10) });
      showToast(`Updated rate limit for ${fullHost}`, 'success');
      fetchSubdomains();
    } catch (e) {
      console.error(e);
      showToast('Failed to update rate limit', 'error');
    }
  };

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

      <div className="card p-xl">
        <DataTableToolbar
          searchQuery={searchQuery}
          onSearchChange={setSearchQuery}
          searchPlaceholder={t('search_subdomains_placeholder', 'Search subdomains...')}
          pageSize={pageSize}
          onPageSizeChange={setPageSize}
          columns={allColumns}
          isColumnVisible={isColumnVisible}
          onToggleColumn={toggleColumn}
        />

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('subdomain') && <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>Subdomain{getSortIndicator('subdomain')}</th>}
                {isColumnVisible('full_host') && <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>Target Host{getSortIndicator('full_host')}</th>}
                {isColumnVisible('user_email') && <th className="th-col th-col--sortable" onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>Owner{getSortIndicator('user_email')}</th>}
                {isColumnVisible('is_online') && <th className="th-col th-col--sortable" onClick={() => requestSort('is_online')} aria-sort={getAriaSort('is_online')}>Status{getSortIndicator('is_online')}</th>}
                {isColumnVisible('node_id') && <th className="th-col th-col--sortable" onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>Node{getSortIndicator('node_id')}</th>}
                {isColumnVisible('client_ip') && <th className="th-col th-col--sortable" onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>Client IP{getSortIndicator('client_ip')}</th>}
                {isColumnVisible('bytes_in') && <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_in')} aria-sort={getAriaSort('bytes_in')}>Bytes In{getSortIndicator('bytes_in')}</th>}
                {isColumnVisible('bytes_out') && <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_out')} aria-sort={getAriaSort('bytes_out')}>Bytes Out{getSortIndicator('bytes_out')}</th>}
                <th className="th-col text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {paginatedSubdomains.length === 0 ? (
                <tr>
                  <td colSpan={9} className="td-cell text-center text-muted py-xl">No registered subdomains found</td>
                </tr>
              ) : (
                paginatedSubdomains.map((sub) => (
                  <tr key={sub.id} className="border-b hover:bg-white/5 transition-colors">
                    {isColumnVisible('subdomain') && <td className="td-cell font-medium">{sub.subdomain || '(wildcard)'}</td>}
                    {isColumnVisible('full_host') && (
                      <td className="td-cell">
                        <a href={`https://${sub.full_host}`} target="_blank" rel="noreferrer" className="text-primary no-underline font-medium">
                          {sub.full_host}
                        </a>
                        {sub.rate_limit !== undefined && sub.rate_limit > 0 && (
                          <span className="badge ml-sm" style={{ backgroundColor: 'rgba(239, 68, 68, 0.15)', color: '#f87171', border: '1px solid rgba(239, 68, 68, 0.3)' }}>
                            ⏱️ {sub.rate_limit} RPS
                          </span>
                        )}
                      </td>
                    )}
                    {isColumnVisible('user_email') && <td className="td-cell">{sub.user_email}</td>}
                    {isColumnVisible('is_online') && (
                      <td className="td-cell">
                        <span className={`badge ${sub.is_online ? 'badge-success' : ''}`} style={!sub.is_online ? { background: 'rgba(255, 255, 255, 0.05)', color: 'var(--text-muted)', border: '1px solid var(--border)' } : {}}>
                          {sub.is_online ? '🟢 Online' : '⚪ Offline'}
                        </span>
                      </td>
                    )}
                    {isColumnVisible('node_id') && (
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
                    )}
                    {isColumnVisible('client_ip') && <td className="td-cell font-mono text-xs">{sub.client_ip}</td>}
                    {isColumnVisible('bytes_in') && <td className="td-cell text-xs text-muted">{formatBytes(sub.bytes_in || 0)}</td>}
                    {isColumnVisible('bytes_out') && <td className="td-cell text-xs text-muted">{formatBytes(sub.bytes_out || 0)}</td>}
                    <td className="td-cell text-right">
                      <div className="flex gap-xs justify-end">
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

          <DataTablePagination
            currentPage={currentPage}
            totalPages={totalPages}
            totalItems={totalItems}
            pageSize={pageSize}
            onPageChange={setCurrentPage}
          />
        </div>
      </div>
    </div>
  );
}
