import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';

interface PAT {
  id: number;
  user_id: string;
  token_prefix: string;
  name: string;
  expires_at?: string;
  revoked_at?: string;
  last_used_at?: string;
  created_at: string;
}

export default function AdminTokens() {
  const [tokens, setTokens] = useState<PAT[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();

  const columns: ColumnDef<PAT>[] = useMemo(() => [
    { key: 'user_id', label: t('owner', 'Owner'), sortable: true },
    { key: 'name', label: t('name', 'Name'), sortable: true },
    { key: 'token_prefix', label: t('prefix', 'Prefix'), sortable: true },
    { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
    { key: 'created_at', label: t('created_at', 'Created'), sortable: true },
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
    requestSort,
    getSortIndicator,
    getAriaSort,
    isColumnVisible,
    toggleColumn,
    allColumns
  } = useDataTable<PAT>(
    'admin_tokens',
    tokens,
    ['name', 'user_id', 'token_prefix'],
    columns,
    10
  );

  const fetchTokens = async () => {
    try {
      const res = await axios.get('/api/admin/tokens');
      setTokens(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTokens();
  }, []);

  const handleRevoke = async (id: number, name: string) => {
    if (!(await showConfirm(t('revoke_token_title', 'Revoke Token'), `${t('revoke_token_confirm', 'Are you sure you want to revoke token')} "${name}"?`))) return;
    try {
      await axios.delete(`/api/admin/tokens/${id}`);
      fetchTokens();
      showToast(t('token_revoked_success', 'Token revoked successfully'), 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || t('token_revoke_failed', 'Failed to revoke token'), 'error');
    }
  };

  const handleExtend = async (id: number, days: number) => {
    try {
      await axios.post(`/api/admin/tokens/${id}/extend`, { days });
      fetchTokens();
      showToast(t('token_extended_success', 'Token extended successfully'), 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || t('token_extend_failed', 'Failed to extend token'), 'error');
    }
  };

  const getTokenStatus = (pat: PAT) => {
    if (pat.revoked_at) return { label: t('status_revoked', 'REVOKED'), badge: 'badge-danger' };
    if (pat.expires_at && new Date(pat.expires_at) <= new Date()) return { label: t('status_expired', 'EXPIRED'), badge: 'badge-warning' };
    return { label: t('status_active', 'ACTIVE'), badge: 'badge-success' };
  };

  const getExpiresInText = (pat: PAT) => {
    if (pat.revoked_at) return t('revoked', 'Revoked');
    if (!pat.expires_at) return t('never', 'Never');
    const diff = new Date(pat.expires_at).getTime() - new Date().getTime();
    if (diff <= 0) return t('expired', 'Expired');
    const days = Math.floor(diff / (1000 * 60 * 60 * 24));
    const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
    return `in ${days}d ${hours}h`;
  };

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="mb-xl">
          <Skeleton width={180} height={28} />
        </div>
        <div className="card p-xl">
          <div className="search-row">
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col text-right"><Skeleton width={120} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="80%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell text-right"><Skeleton width="80%" height={32} /></td>
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
          <h3 className="page-header__title">{t('admin_tokens_title', 'All Personal Access Tokens')}</h3>
          <p className="page-header__desc">{t('admin_tokens_desc', 'Monitor, extend, and revoke authentication tokens for all users across the system.')}</p>
        </div>
      </div>

      <div className="card p-0">
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
          />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('user_id') && <th className="th-col th-col--sortable" onClick={() => requestSort('user_id')} aria-sort={getAriaSort('user_id')}>{t('owner', 'Owner')}{getSortIndicator('user_id')}</th>}
                {isColumnVisible('name') && <th className="th-col th-col--sortable" onClick={() => requestSort('name')} aria-sort={getAriaSort('name')}>{t('name', 'Name')}{getSortIndicator('name')}</th>}
                {isColumnVisible('token_prefix') && <th className="th-col th-col--sortable" onClick={() => requestSort('token_prefix')} aria-sort={getAriaSort('token_prefix')}>{t('prefix', 'Prefix')}{getSortIndicator('token_prefix')}</th>}
                {isColumnVisible('created_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>{t('created', 'Created')}{getSortIndicator('created_at')}</th>}
                {isColumnVisible('expires_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>{t('expires', 'Expires')}{getSortIndicator('expires_at')}</th>}
                <th className="th-col">{t('status', 'Status')}</th>
                <th className="th-col">{t('expires_in', 'Expires In')}</th>
                <th className="th-col text-right">{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedTokens.map((pat) => {
                const status = getTokenStatus(pat);
                const expiresIn = getExpiresInText(pat);
                return (
                  <tr key={pat.id} className="border-b hover:bg-white/5 transition-colors">
                    {isColumnVisible('user_id') && <td className="td-cell font-medium">{pat.user_id}</td>}
                    {isColumnVisible('name') && <td className="td-cell">{pat.name || <span className="text-muted text-xs italic">Unnamed</span>}</td>}
                    {isColumnVisible('token_prefix') && <td className="td-cell font-mono text-xs">{pat.token_prefix}...</td>}
                    {isColumnVisible('created_at') && <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>{formatDate(pat.created_at)}</td>}
                    {isColumnVisible('expires_at') && <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>{pat.expires_at ? formatDate(pat.expires_at) : 'Never'}</td>}
                    <td className="td-cell">
                      <span className={`badge ${status.badge} text-xs font-semibold`}>
                        {status.label}
                      </span>
                    </td>
                    <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>
                      {expiresIn}
                    </td>
                    <td className="td-cell text-right">
                      <div className="flex gap-xs justify-end">
                        {!pat.revoked_at && (
                          <>
                            <button
                              onClick={() => handleExtend(pat.id, 30)}
                              className="btn btn-secondary py-xs px-sm text-xs"
                              title={t('extend_30_days', 'Extend 30 Days')}
                            >
                              +30d
                            </button>
                            <button
                              onClick={() => handleRevoke(pat.id, pat.name || pat.token_prefix)}
                              className="btn btn-danger py-xs px-sm text-xs"
                            >
                              {t('revoke', 'Revoke')}
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
              {paginatedTokens.length === 0 && (
                <tr>
                  <td colSpan={8} className="td-cell text-center text-muted py-xl">
                    {t('no_tokens_found', 'No authentication tokens found.')}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <DataTablePagination
          currentPage={currentPage}
          totalPages={totalPages}
          totalItems={totalItems}
          pageSize={pageSize}
          onPageChange={setCurrentPage}
        />
      </div>
    </div>
  );
}
