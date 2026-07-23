import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';

interface AuditEvent {
  id: number;
  actor_id: string;
  action: string;
  target_type: string;
  target_id: string;
  ip_address: string;
  created_at: string;
  details: string;
}

export default function AdminAuditLog() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 15;
  const { formatDate } = useSettings();
  const { t } = useI18n();

  const fetchEvents = async () => {
    try {
      const res = await axios.get('/api/admin/audit');
      setEvents(Array.isArray(res.data) ? res.data : (res.data.events || []));
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchEvents();
  }, []);

  const { items: sortedEvents, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(events, ['actor_id', 'action', 'target_type', 'target_id', 'ip_address', 'details']);

  const totalPages = Math.ceil(sortedEvents.length / ROWS_PER_PAGE);
  const paginatedEvents = sortedEvents.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="page-header">
          <div>
            <Skeleton width={180} height={28} />
            <Skeleton width={280} height={16} className="mt-sm" />
          </div>
          <Skeleton width={100} height={40} />
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
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={90} /></th>
                  <th className="th-col"><Skeleton width={180} /></th>
                  <th className="th-col"><Skeleton width={60} /></th>
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
        <div>
          <h3 className="page-header__title">System Audit Log</h3>
          <p className="page-header__desc">Immutable record of administrative and security events.</p>
        </div>
        <a 
          href="/api/admin/audit/export" 
          className="btn btn-secondary w-auto inline-flex items-center gap-sm" 
          style={{ whiteSpace: 'nowrap' }}
        >
          📥 {t('export_csv', 'Export CSV')}
        </a>
      </div>
      <div className="search-row">
        <input 
          type="text" 
          placeholder={t('search_audit_logs_placeholder', 'Search audit logs...')} 
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
                <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Time{getSortIndicator('created_at')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('actor_id')} aria-sort={getAriaSort('actor_id')}>Actor{getSortIndicator('actor_id')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('action')} aria-sort={getAriaSort('action')}>Action{getSortIndicator('action')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('target_id')} aria-sort={getAriaSort('target_id')}>Resource{getSortIndicator('target_id')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('ip_address')} aria-sort={getAriaSort('ip_address')}>IP Address{getSortIndicator('ip_address')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('details')} aria-sort={getAriaSort('details')}>Details{getSortIndicator('details')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedEvents.length === 0 ? (
                <tr>
                  <td colSpan={6} className="td-empty">
                    No audit logs available.
                  </td>
                </tr>
              ) : (
                paginatedEvents.map((e, idx) => (
                  <tr key={e.id || idx} className="border-b">
                    <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>{formatDate(e.created_at)}</td>
                    <td className="td-cell">{e.actor_id}</td>
                    <td className="td-cell"><span className="badge badge-info">{e.action}</span></td>
                    <td className="td-cell">
                      {e.target_type && e.target_id ? (
                        <span className="text-sm">
                          <strong className="opacity-60">{e.target_type}:</strong> {e.target_id}
                        </span>
                      ) : e.target_id || '-'}
                    </td>
                    <td className="td-cell--mono">{e.ip_address}</td>
                    <td className="td-cell--mono text-2xs text-muted overflow-auto" style={{ maxWidth: '200px' }}>
                      {e.details}
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
              Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedEvents.length)} of {sortedEvents.length} logs
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
