import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

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

  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 15;

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
    } catch {
      showToast(t('failed_revoke_token', 'Failed to revoke token'), 'error');
    }
  };

  const handleExtend = async (id: number, name: string, days: number) => {
    const desc = days > 0 ? `${days} days` : 'Permanently';
    if (!(await showConfirm(t('extend_token_title', 'Extend Token'), `Extend token "${name}" by ${desc}?`))) return;
    try {
      await axios.post(`/api/admin/tokens/${id}/extend`, { days });
      fetchTokens();
      showToast(t('token_extended_success', 'Token extended successfully'), 'success');
    } catch {
      showToast(t('failed_extend_token', 'Failed to extend token'), 'error');
    }
  };

  const getTokenStatus = (pat: PAT) => {
    if (pat.revoked_at) return 'revoked';
    if (pat.expires_at && new Date(pat.expires_at) < new Date()) return 'expired';
    return 'active';
  };

  const { items: sortedTokens, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tokens, ['user_id', 'name', 'token_prefix']);

  const totalPages = Math.ceil(sortedTokens.length / ROWS_PER_PAGE);
  const paginatedTokens = sortedTokens.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE);

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
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <div className="mb-xl">
        <h1 className="page-header__title text-4xl">
          {t('admin_tokens_title', 'All Personal Access Tokens')}
        </h1>
        <p className="page-header__desc text-md">
          {t('admin_tokens_desc', 'Monitor, extend, and revoke authentication tokens for all users across the system.')}
        </p>
      </div>

      <div className="card p-xl">
        <div className="search-row">
          <input
            type="text"
            placeholder={t('search_tokens_placeholder', 'Search tokens...')}
            value={searchQuery}
            onChange={e => {
              setSearchQuery(e.target.value);
              setPage(0);
            }}
            className="search-input"
          />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="th-col th-col--sortable" onClick={() => requestSort('user_id')} aria-sort={getAriaSort('user_id')}>{t('owner', 'Owner')}{getSortIndicator('user_id')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('name')} aria-sort={getAriaSort('name')}>{t('name', 'Name')}{getSortIndicator('name')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('token_prefix')} aria-sort={getAriaSort('token_prefix')}>{t('prefix', 'Prefix')}{getSortIndicator('token_prefix')}</th>
                <th className="th-col">{t('expires', 'Expires')}</th>
                <th className="th-col">{t('status', 'Status')}</th>
                <th className="th-col text-right">{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedTokens.map((pat) => {
                const status = getTokenStatus(pat);
                return (
                  <tr key={pat.id} className="border-b">
                    <td className="td-cell">{pat.user_id}</td>
                    <td className="td-cell fw-semibold">{pat.name}</td>
                    <td className="td-cell--mono">{pat.token_prefix}...</td>
                    <td className="td-cell">{pat.expires_at ? formatDate(pat.expires_at) : t('never', 'Never')}</td>
                    <td className="td-cell">
                      <span className={`badge badge-${status}`}>
                        {status.toUpperCase()}
                      </span>
                    </td>
                    <td className="td-cell text-right">
                      <div className="flex gap-sm justify-end">
                        {status === 'active' && (
                          <>
                            <button
                              className="btn btn-outline py-xs px-sm text-xs"
                              onClick={() => handleExtend(pat.id, pat.name, 30)}
                            >
                              +30d
                            </button>
                            <button
                              className="btn btn-outline py-xs px-sm text-xs"
                              onClick={() => handleExtend(pat.id, pat.name, 90)}
                            >
                              +90d
                            </button>
                            <button
                              className="btn btn-outline py-xs px-sm text-xs"
                              onClick={() => handleExtend(pat.id, pat.name, 0)}
                            >
                              {t('perm', 'Perm')}
                            </button>
                            <button
                              className="btn btn-danger py-xs px-sm text-xs"
                              onClick={() => handleRevoke(pat.id, pat.name)}
                            >
                              {t('revoke', 'Revoke')}
                            </button>
                          </>
                        )}
                        {status !== 'active' && (
                          <button
                            className="btn btn-danger py-xs px-sm text-xs opacity-60"
                            disabled
                          >
                            {status === 'revoked' ? t('revoked', 'Revoked') : t('expired', 'Expired')}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
              {sortedTokens.length === 0 && (
                <tr>
                  <td colSpan={6} className="td-empty">
                    {t('no_tokens_found', 'No personal access tokens found.')}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {totalPages > 1 && (
          <div className="pagination-row">
            <span className="pagination-count">
              {t('showing_x_to_y_of_z', `Showing ${page * ROWS_PER_PAGE + 1} to ${Math.min((page + 1) * ROWS_PER_PAGE, sortedTokens.length)} of ${sortedTokens.length} tokens`)}
            </span>
            <div className="pagination-controls">
              <button className="btn btn-secondary py-xs px-sm text-xs" onClick={() => setPage(0)} disabled={page === 0}>«</button>
              <button className="btn btn-secondary py-xs px-sm text-xs" onClick={() => setPage(p => Math.max(0, p - 1))} disabled={page === 0}>{t('prev', 'Prev')}</button>
              <span className="pagination-page-label">{page + 1} / {totalPages}</span>
              <button className="btn btn-secondary py-xs px-sm text-xs" onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))} disabled={page >= totalPages - 1}>{t('next', 'Next')}</button>
              <button className="btn btn-secondary py-xs px-sm text-xs" onClick={() => setPage(totalPages - 1)} disabled={page >= totalPages - 1}>»</button>
            </div>
          </div>
        )}

      </div>
    </div>
  );
}
