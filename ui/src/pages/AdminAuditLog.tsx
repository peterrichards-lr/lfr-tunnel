import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

interface AuditEvent {
  event_id: string;
  actor: string;
  action: string;
  resource: string;
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

  const { items: sortedEvents, requestSort, getSortIndicator, searchQuery, setSearchQuery } = useTableSort(events, ['actor', 'action', 'resource', 'ip_address', 'details']);

  const totalPages = Math.ceil(sortedEvents.length / ROWS_PER_PAGE);
  const paginatedEvents = sortedEvents.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <div>
            <Skeleton width={180} height={28} />
            <Skeleton width={280} height={16} style={{ marginTop: '8px' }} />
          </div>
          <Skeleton width={100} height={40} />
        </div>
        
        <div className="card" style={{ padding: '24px', marginBottom: '24px' }}>
          <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card" style={{ padding: '24px' }}>
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={90} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={180} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={60} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}><Skeleton width="90%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="85%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="80%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="50%" height={16} /></td>
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h3 style={{ margin: 0 }}>System Audit Log</h3>
          <p style={{ color: 'var(--text-muted)', marginTop: '4px' }}>Immutable record of administrative and security events.</p>
        </div>
        <a href="/api/admin/audit/export" className="btn btn-secondary">Export CSV</a>
      </div>
      <div style={{ marginBottom: '16px' }}>
        <input 
          type="text" 
          placeholder="Search audit logs..." 
          value={searchQuery} 
          onChange={e => { setSearchQuery(e.target.value); setPage(0); }}
          style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
        />
      </div>

      <div className="card" style={{ padding: 0 }}>
        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('created_at')}>Time{getSortIndicator('created_at')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('actor')}>Actor{getSortIndicator('actor')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('action')}>Action{getSortIndicator('action')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('resource')}>Resource{getSortIndicator('resource')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('ip_address')}>IP Address{getSortIndicator('ip_address')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('details')}>Details{getSortIndicator('details')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedEvents.length === 0 ? (
                <tr>
                  <td colSpan={6} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>
                    No audit logs available.
                  </td>
                </tr>
              ) : (
                paginatedEvents.map((e, idx) => (
                  <tr key={e.event_id || idx} style={{ borderBottom: '1px solid var(--border)' }}>
                    <td style={{ padding: '16px', whiteSpace: 'nowrap' }}>{formatDate(e.created_at)}</td>
                    <td style={{ padding: '16px' }}>{e.actor}</td>
                    <td style={{ padding: '16px' }}><span className="badge" style={{ background: 'var(--status-info-bg)', color: 'var(--status-info-text)', border: '1px solid var(--status-info-border)' }}>{e.action}</span></td>
                    <td style={{ padding: '16px' }}>{e.resource}</td>
                    <td style={{ padding: '16px', fontFamily: 'monospace' }}>{e.ip_address}</td>
                    <td style={{ padding: '16px', fontSize: '11px', fontFamily: 'monospace', color: 'var(--text-muted)', maxWidth: '200px', overflowX: 'auto' }}>
                      {e.details}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        {totalPages > 1 && (
          <div style={{ padding: '16px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderTop: '1px solid var(--border)' }}>
            <div style={{ color: 'var(--text-muted)', fontSize: '14px' }}>
              Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedEvents.length)} of {sortedEvents.length} logs
            </div>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(0)}
                disabled={page === 0}
                style={{ padding: '4px 12px', fontSize: '13px' }}
              >
                First
              </button>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(p => Math.max(0, p - 1))}
                disabled={page === 0}
                style={{ padding: '4px 12px', fontSize: '13px' }}
              >
                Previous
              </button>
              <span style={{ padding: '4px 8px', fontSize: '14px' }}>Page {page + 1} of {totalPages}</span>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
                disabled={page >= totalPages - 1}
                style={{ padding: '4px 12px', fontSize: '13px' }}
              >
                Next
              </button>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(totalPages - 1)}
                disabled={page >= totalPages - 1}
                style={{ padding: '4px 12px', fontSize: '13px' }}
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
