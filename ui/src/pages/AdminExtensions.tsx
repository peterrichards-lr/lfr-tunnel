import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';

interface ExtRequest {
  id: string;
  email: string;
  subdomain: string;
  port: number;
  status: string;
  expires_at: string;
}

export default function AdminExtensions() {
  const [requests, setRequests] = useState<ExtRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();

  const fetchRequests = async () => {
    try {
      const res = await axios.get('/api/admin/extensions');
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
      await axios.post(`/api/admin/extensions/${id}`, { action });
      fetchRequests();
    } catch (err) {
      console.error(err);
      alert('Action failed');
    }
  };

  if (loading) return <div>Loading...</div>;

  const { items: sortedRequests, requestSort, getSortIndicator, searchQuery, setSearchQuery } = useTableSort(requests, ['email', 'subdomain', 'status']);


  return (
    <div className="card" style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <h3 style={{ marginTop: 0, marginBottom: '24px', fontSize: '20px', fontWeight: 700 }}>Extension Requests</h3>
      {requests.length > 0 && (
        <div style={{ marginBottom: '16px' }}>
          <input 
            type="text" 
            placeholder="Search extensions..." 
            value={searchQuery} 
            onChange={e => setSearchQuery(e.target.value)}
            style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
          />
        </div>
      )}
      
      <div className="table-responsive">
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('email')}>Email{getSortIndicator('email')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('subdomain')}>Subdomain{getSortIndicator('subdomain')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>Port</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>Expires</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('status')}>Status{getSortIndicator('status')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {sortedRequests.map((req) => (
              <tr key={req.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '16px' }}>{req.email}</td>
                <td style={{ padding: '16px' }}>{req.subdomain}</td>
                <td style={{ padding: '16px' }}>{req.port}</td>
                <td style={{ padding: '16px' }}>{req.expires_at ? formatDate(req.expires_at) : 'Never'}</td>
                <td style={{ padding: '16px' }}>
                  <span className="badge">{req.status}</span>
                </td>
                <td style={{ padding: '16px', textAlign: 'right' }}>
                  {req.status === 'pending' && (
                    <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                      <button className="btn btn-primary" style={{ padding: '4px 12px', fontSize: '12px' }} onClick={() => handleAction(req.id, 'approve')}>Approve</button>
                      <button className="btn btn-secondary" style={{ padding: '4px 12px', fontSize: '12px' }} onClick={() => handleAction(req.id, 'reject')}>Reject</button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
            {requests.length === 0 && (
              <tr>
                <td colSpan={6} style={{ textAlign: 'center', padding: '40px 20px', color: 'var(--text-muted)' }}>
                  No extension requests found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
