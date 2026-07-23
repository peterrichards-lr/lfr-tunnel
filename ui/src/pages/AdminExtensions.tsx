import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface ExtRequest {
  id: string;
  user_email: string;
  subdomain: string;
  domain: string;
  expires_at: string;
  created_at?: string;
}

export default function AdminExtensions() {
  const [requests, setRequests] = useState<ExtRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast } = useUI();

  const columns: ColumnDef<ExtRequest>[] = useMemo(() => [
    { key: 'user_email', label: 'Email', sortable: true },
    { key: 'subdomain', label: 'Subdomain', sortable: true },
    { key: 'domain', label: 'Domain', sortable: true },
    { key: 'expires_at', label: 'Expires', sortable: true },
    { key: 'created_at', label: 'Created Date', sortable: true },
  ], []);

  const {
    paginatedItems: paginatedRequests,
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
  } = useDataTable<ExtRequest>(
    'admin_extensions',
    requests,
    ['user_email', 'subdomain', 'domain'],
    columns,
    10,
    ['created_at']
  );

  const fetchRequests = async () => {
    try {
      const res = await axios.get('/api/admin/reservations/extensions');
      setRequests(res.data || []);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchRequests();
  }, []);

  const handleAction = async (id: string, action: 'approve' | 'reject') => {
    try {
      if (action === 'approve') {
        await axios.post(`/api/admin/reservations/${id}/approve-extension`);
      } else {
        await axios.post(`/api/admin/reservations/${id}/demote`);
      }
      fetchRequests();
      showToast(`Request successfully ${action === 'approve' ? 'approved' : 'rejected'}.`, 'success');
    } catch (err) {
      console.error(err);
      showToast('Action failed', 'error');
    }
  };

  if (loading) {
    return (
      <div>
        <div className="page-header mb-xl">
          <div>
            <Skeleton width={200} height={28} />
            <Skeleton width={300} height={16} className="mt-sm" />
          </div>
        </div>
        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col text-right"><Skeleton width={100} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                    <td className="td-cell text-right"><Skeleton width="80%" height={16} /></td>
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
      <div className="page-header mb-xl">
        <div>
          <h3 className="page-header__title">{t('extension_requests', 'Extension Requests')}</h3>
          <p className="page-header__desc">{t('extension_requests_desc', 'Review and approve subdomain lease extension requests.')}</p>
        </div>
      </div>

      <div className="card p-0">
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t('search_extensions_placeholder', 'Search extension requests...')}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={allColumns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
          />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('user_email') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>
                    Email{getSortIndicator('user_email')}
                  </th>
                )}
                {isColumnVisible('subdomain') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>
                    Subdomain{getSortIndicator('subdomain')}
                  </th>
                )}
                {isColumnVisible('domain') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('domain')} aria-sort={getAriaSort('domain')}>
                    Domain{getSortIndicator('domain')}
                  </th>
                )}
                {isColumnVisible('expires_at') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>
                    Expires{getSortIndicator('expires_at')}
                  </th>
                )}
                {isColumnVisible('created_at') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>
                    Created Date{getSortIndicator('created_at')}
                  </th>
                )}
                <th className="th-col text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {paginatedRequests.length === 0 ? (
                <tr>
                  <td colSpan={6} className="td-empty">
                    No pending extension requests.
                  </td>
                </tr>
              ) : (
                paginatedRequests.map((req) => (
                  <tr key={req.id} className="border-b">
                    {isColumnVisible('user_email') && <td className="td-cell">{req.user_email}</td>}
                    {isColumnVisible('subdomain') && <td className="td-cell font-mono text-xs">{req.subdomain}</td>}
                    {isColumnVisible('domain') && <td className="td-cell font-mono text-xs">{req.domain}</td>}
                    {isColumnVisible('expires_at') && <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>{req.expires_at ? formatDate(req.expires_at) : 'Never'}</td>}
                    {isColumnVisible('created_at') && <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>{req.created_at ? formatDate(req.created_at) : '—'}</td>}
                    <td className="td-cell text-right">
                      <div className="flex gap-sm justify-end">
                        <button className="btn btn-primary px-md py-xs text-xs" onClick={() => handleAction(req.id, 'approve')}>Approve</button>
                        <button className="btn btn-secondary px-md py-xs text-xs" onClick={() => handleAction(req.id, 'reject')}>Reject</button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
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
      </div>
    </div>
  );
}
