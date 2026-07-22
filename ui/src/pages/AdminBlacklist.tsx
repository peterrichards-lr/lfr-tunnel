import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

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
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to block IP'}`);
    }
  };

  const removeEntry = async (ip: string) => {
    if (!confirm(`Are you sure you want to unblock ${ip}?`)) return;
    try {
      await axios.delete(`/api/admin/blacklist/${encodeURIComponent(ip)}`);
      fetchEntries();
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to unblock IP'}`);
    }
  };

  const { items: sortedEntries, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(entries, ['ip', 'reason']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <div>
            <Skeleton width={180} height={28} />
          </div>
        </div>

        <div className="card" style={{ padding: '24px', marginBottom: '24px' }}>
          <div style={{ display: 'flex', gap: '12px', flexWrap: 'wrap' }}>
            <Skeleton width="100%" height={40} style={{ flex: '1', minWidth: '150px' }} />
            <Skeleton width="100%" height={40} style={{ flex: '2', minWidth: '200px' }} />
            <Skeleton width={120} height={40} />
          </div>
        </div>

        <div className="card" style={{ padding: '24px' }}>
          <div style={{ marginBottom: '16px' }}>
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
          
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={200} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}><Skeleton width="90%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="85%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="50%" height={28} /></td>
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
      <div style={{ marginBottom: '24px' }}>
        <h3>IP Blacklist</h3>
        <p style={{ color: 'var(--text-muted)' }}>Manage explicitly blocked IP addresses.</p>
      </div>
      
      <div className="card" style={{ marginBottom: '24px', maxWidth: '600px' }}>
        <h4 style={{ margin: '0 0 16px 0', fontSize: '16px' }}>Add IP to Blacklist</h4>
        <form onSubmit={addEntry} style={{ display: 'flex', gap: '12px', flexWrap: 'wrap', alignItems: 'flex-start' }}>
          <div style={{ flex: '1 1 200px' }}>
            <input 
              type="text" 
              className="input-field" 
              placeholder="IP Address (e.g. 192.168.1.1)" 
              value={ipInput} 
              onChange={(e) => setIpInput(e.target.value)} 
              style={{ width: '100%' }}
            />
          </div>
          <div style={{ flex: '2 1 250px' }}>
            <input 
              type="text" 
              className="input-field" 
              placeholder="Reason (optional)" 
              value={reasonInput} 
              onChange={(e) => setReasonInput(e.target.value)} 
              style={{ width: '100%' }}
            />
          </div>
          <button type="submit" className="btn btn-danger" style={{ whiteSpace: 'nowrap', width: 'auto' }}>Block IP</button>
        </form>
      </div>

      <div style={{ marginBottom: '16px' }}>
        <input 
          type="text" 
          placeholder="Search blacklist..." 
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
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('ip')} aria-sort={getAriaSort('ip')}>IP Address{getSortIndicator('ip')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('reason')} aria-sort={getAriaSort('reason')}>Reason{getSortIndicator('reason')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', cursor: 'pointer' }} onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Blocked At{getSortIndicator('created_at')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {paginatedEntries.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>
                    No IP addresses are currently blocked.
                  </td>
                </tr>
              ) : (
                paginatedEntries.map(entry => (
                  <tr key={entry.ip} style={{ borderBottom: '1px solid var(--border)' }}>
                    <td style={{ padding: '16px', fontFamily: 'monospace', fontWeight: 500 }}>{entry.ip}</td>
                    <td style={{ padding: '16px' }}>{entry.reason}</td>
                    <td style={{ padding: '16px', whiteSpace: 'nowrap' }}>{formatDate(entry.created_at)}</td>
                    <td style={{ padding: '16px', textAlign: 'right' }}>
                      <button className="btn btn-secondary" style={{ padding: '6px 12px', fontSize: '12px', width: 'auto' }} onClick={() => removeEntry(entry.ip)}>
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
          <div style={{ padding: '16px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderTop: '1px solid var(--border)' }}>
            <div style={{ color: 'var(--text-muted)', fontSize: '14px' }}>
              Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedEntries.length)} of {sortedEntries.length} IPs
            </div>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(0)}
                disabled={page === 0}
                style={{ padding: '4px 12px', fontSize: '13px', width: 'auto' }}
              >
                First
              </button>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(p => Math.max(0, p - 1))}
                disabled={page === 0}
                style={{ padding: '4px 12px', fontSize: '13px', width: 'auto' }}
              >
                Previous
              </button>
              <span style={{ padding: '4px 8px', fontSize: '14px' }}>Page {page + 1} of {totalPages}</span>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
                disabled={page >= totalPages - 1}
                style={{ padding: '4px 12px', fontSize: '13px', width: 'auto' }}
              >
                Next
              </button>
              <button 
                className="btn btn-secondary" 
                onClick={() => setPage(totalPages - 1)}
                disabled={page >= totalPages - 1}
                style={{ padding: '4px 12px', fontSize: '13px', width: 'auto' }}
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
