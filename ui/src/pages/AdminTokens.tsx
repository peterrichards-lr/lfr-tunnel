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
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <Skeleton width={180} height={28} />
        </div>
        <div className="card" style={{ padding: '24px' }}>
          <Skeleton width="100%" height={40} style={{ maxWidth: '300px', marginBottom: '16px' }} />
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px', textAlign: 'right' }}><Skeleton width={120} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}><Skeleton width="80%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="50%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px', textAlign: 'right' }}><Skeleton width="80%" height={32} /></td>
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
      <div style={{ marginBottom: '24px' }}>
        <h1 style={{ fontSize: '32px', fontWeight: 800, letterSpacing: '-1px', margin: 0 }}>
          {t('admin_tokens_title', 'All Personal Access Tokens')}
        </h1>
        <p style={{ color: 'var(--text-muted)', margin: '8px 0 0 0', fontSize: '16px' }}>
          {t('admin_tokens_desc', 'Monitor, extend, and revoke authentication tokens for all users across the system.')}
        </p>
      </div>

      <div className="card" style={{ padding: '24px' }}>
        <div style={{ marginBottom: '16px' }}>
          <input
            type="text"
            placeholder={t('search_tokens_placeholder', 'Search tokens...')}
            value={searchQuery}
            onChange={e => {
              setSearchQuery(e.target.value);
              setPage(0);
            }}
            style={{
              padding: '8px 12px',
              width: '100%',
              maxWidth: '300px',
              background: 'var(--input-bg)',
              color: 'var(--text-main)',
              border: '1px solid var(--border)',
              borderRadius: '6px'
            }}
          />
        </div>

        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('user_id')} aria-sort={getAriaSort('user_id')}>{t('owner', 'Owner')}{getSortIndicator('user_id')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('name')} aria-sort={getAriaSort('name')}>{t('name', 'Name')}{getSortIndicator('name')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('token_prefix')} aria-sort={getAriaSort('token_prefix')}>{t('prefix', 'Prefix')}{getSortIndicator('token_prefix')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>{t('expires', 'Expires')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>{t('status', 'Status')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', textAlign: 'right' }}>{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedTokens.map((pat) => {
                const status = getTokenStatus(pat);
                return (
                  <tr key={pat.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}>{pat.user_id}</td>
                    <td style={{ padding: '16px', fontWeight: 600 }}>{pat.name}</td>
                    <td style={{ padding: '16px', fontFamily: 'monospace' }}>{pat.token_prefix}...</td>
                    <td style={{ padding: '16px' }}>{pat.expires_at ? formatDate(pat.expires_at) : t('never', 'Never')}</td>
                    <td style={{ padding: '16px' }}>
                      <span className={`badge badge-${status}`}>
                        {status.toUpperCase()}
                      </span>
                    </td>
                    <td style={{ padding: '16px', textAlign: 'right' }}>
                      <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                        {status === 'active' && (
                          <>
                            <button
                              className="btn btn-outline"
                              style={{ padding: '4px 8px', fontSize: '12px' }}
                              onClick={() => handleExtend(pat.id, pat.name, 30)}
                            >
                              +30d
                            </button>
                            <button
                              className="btn btn-outline"
                              style={{ padding: '4px 8px', fontSize: '12px' }}
                              onClick={() => handleExtend(pat.id, pat.name, 90)}
                            >
                              +90d
                            </button>
                            <button
                              className="btn btn-outline"
                              style={{ padding: '4px 8px', fontSize: '12px' }}
                              onClick={() => handleExtend(pat.id, pat.name, 0)}
                            >
                              {t('perm', 'Perm')}
                            </button>
                            <button
                              className="btn btn-danger"
                              style={{ padding: '4px 8px', fontSize: '12px' }}
                              onClick={() => handleRevoke(pat.id, pat.name)}
                            >
                              {t('revoke', 'Revoke')}
                            </button>
                          </>
                        )}
                        {status !== 'active' && (
                          <button
                            className="btn btn-danger"
                            disabled
                            style={{ padding: '4px 8px', fontSize: '12px', opacity: 0.5 }}
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
                  <td colSpan={6} style={{ textAlign: 'center', padding: '40px 20px', color: 'var(--text-muted)' }}>
                    {t('no_tokens_found', 'No personal access tokens found.')}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {totalPages > 1 && (
          <div style={{ display: 'flex', justifyContent: 'center', gap: '8px', marginTop: '24px' }}>
            <button
              className="btn btn-outline"
              disabled={page === 0}
              onClick={() => setPage(p => Math.max(0, p - 1))}
            >
              &larr; {t('prev', 'Prev')}
            </button>
            <span style={{ display: 'flex', alignItems: 'center', padding: '0 8px', color: 'var(--text-muted)' }}>
              {page + 1} / {totalPages}
            </span>
            <button
              className="btn btn-outline"
              disabled={page === totalPages - 1}
              onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
            >
              {t('next', 'Next')} &rarr;
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
