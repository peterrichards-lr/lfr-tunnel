import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface BlacklistEntry {
  ip: string;
  reason: string;
  created_at: string;
}

export default function AdminBlacklist() {
  const [entries, setEntries] = useState<BlacklistEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [ipInput, setIpInput] = useState('');
  const [reasonInput, setReasonInput] = useState('');
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();

  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 15;

  const fetchEntries = async () => {
    try {
      const res = await axios.get('/api/admin/blacklist');
      setEntries(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchEntries();
  }, []);

  const addEntry = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ipInput) return;
    try {
      await axios.post('/api/admin/blacklist', {
        ip: ipInput,
        reason: reasonInput || 'Manual ban'
      });
      setIpInput('');
      setReasonInput('');
      fetchEntries();
      showToast('IP blocked successfully', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || 'Failed to block IP', 'error');
    }
  };

  const removeEntry = async (ip: string) => {
    if (!(await showConfirm('Unblock IP', `Are you sure you want to unblock ${ip}?`))) return;
    try {
      await axios.delete(`/api/admin/blacklist/${encodeURIComponent(ip)}`);
      fetchEntries();
      showToast('IP unblocked successfully', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || 'Failed to unblock IP', 'error');
    }
  };

  const { items: sortedEntries, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(entries, ['ip', 'reason']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="page-header">
          <div>
            <Skeleton width={180} height={28} />
          </div>
        </div>

        <div className="card p-xl mb-xl">
          <div className="flex gap-md flex-wrap">
            <Skeleton width="100%" height={40} style={{ flex: '1', minWidth: '150px' }} />
            <Skeleton width="100%" height={40} style={{ flex: '2', minWidth: '200px' }} />
            <Skeleton width={120} height={40} />
          </div>
        </div>

        <div className="card p-xl">
          <div className="search-row">
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
          
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={200} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={28} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }

  const totalPages = Math.ceil(sortedEntries.length / ROWS_PER_PAGE);
  const paginatedEntries = sortedEntries.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE);

  return (
    <div>
      <div className="mb-xl">
        <h3 className="page-header__title">IP Blacklist</h3>
        <p className="page-header__desc">Manage explicitly blocked IP addresses.</p>
      </div>
      
      <div className="card mb-xl max-w-lg">
        <h4 className="text-md fw-semibold m-0 mb-lg">Add IP to Blacklist</h4>
        <form onSubmit={addEntry} className="flex gap-md flex-wrap items-start">
          <div style={{ flex: '1 1 200px' }}>
            <input 
              type="text" 
              className="input-field w-full" 
              placeholder={t('ip_address_eg_placeholder', 'IP Address (e.g. 192.168.1.1)')} 
              value={ipInput} 
              onChange={(e) => setIpInput(e.target.value)} 
            />
          </div>
          <div style={{ flex: '2 1 250px' }}>
            <input 
              type="text" 
              className="input-field w-full" 
              placeholder={t('reason_optional_placeholder', 'Reason (optional)')} 
              value={reasonInput} 
              onChange={(e) => setReasonInput(e.target.value)} 
            />
          </div>
          <button type="submit" className="btn btn-danger w-auto" style={{ whiteSpace: 'nowrap' }}>Block IP</button>
        </form>
      </div>

      <div className="search-row">
        <input 
          type="text" 
          placeholder={t('search_blacklist_placeholder', 'Search blacklist...')} 
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
                <th className="th-col th-col--sortable" onClick={() => requestSort('ip')} aria-sort={getAriaSort('ip')}>IP Address{getSortIndicator('ip')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('reason')} aria-sort={getAriaSort('reason')}>Reason{getSortIndicator('reason')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Blocked At{getSortIndicator('created_at')}</th>
                <th className="th-col text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {paginatedEntries.length === 0 ? (
                <tr>
                  <td colSpan={4} className="td-empty">
                    No IP addresses are currently blocked.
                  </td>
                </tr>
              ) : (
                paginatedEntries.map(entry => (
                  <tr key={entry.ip} className="border-b">
                    <td className="td-cell--mono fw-medium">{entry.ip}</td>
                    <td className="td-cell">{entry.reason}</td>
                    <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>{formatDate(entry.created_at)}</td>
                    <td className="td-cell text-right">
                      <button className="btn btn-secondary py-xs px-md text-xs w-auto" onClick={() => removeEntry(entry.ip)}>
                        Unblock
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        {totalPages > 1 && (
          <div className="pagination-row p-lg border-t">
            <div className="pagination-count">
              Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedEntries.length)} of {sortedEntries.length} IPs
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
                onClick={() => setPage(p => Math.max(0, p - 1))}
                disabled={page === 0}
              >
                Previous
              </button>
              <span className="pagination-page-label">Page {page + 1} of {totalPages}</span>
              <button 
                className="btn btn-secondary py-xs px-md text-xs w-auto" 
                onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
                disabled={page >= totalPages - 1}
              >
                Next
              </button>
              <button 
                className="btn btn-secondary py-xs px-md text-xs w-auto" 
                onClick={() => setPage(totalPages - 1)}
                disabled={page >= totalPages - 1}
              >
                Last
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
