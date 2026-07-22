import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface ExtRequest {
  id: string;
  user_email: string;
  subdomain: string;
  domain: string;
  expires_at: string;
}

export default function AdminExtensions() {
  const [requests, setRequests] = useState<ExtRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast } = useUI();

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

  const { items: sortedRequests, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(requests, ['user_email', 'subdomain', 'domain']);
  if (loading) {
    return (
      <div className="card" style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <h3 style={{ marginTop: 0, marginBottom: '24px', fontSize: '20px', fontWeight: 700 }}>
          <Skeleton width={180} height={24} />
        </h3>
        
        <div style={{ marginBottom: '16px' }}>
          <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
        </div>

        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
              </tr>
            </thead>
            <tbody>
              {[...Array(3)].map((_, i) => (
                <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '16px' }}><Skeleton width="90%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width="80%" height={16} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    );
  }



  return (
    <div className="card" style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <h3 style={{ marginTop: 0, marginBottom: '24px', fontSize: '20px', fontWeight: 700 }}>Extension Requests</h3>
      {requests.length > 0 && (
        <div style={{ marginBottom: '16px' }}>
          <input 
            type="text" 
            placeholder={t('search_extensions_placeholder', 'Search extensions...')} 
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
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>Email{getSortIndicator('user_email')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>Subdomain{getSortIndicator('subdomain')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', cursor: 'pointer' }} onClick={() => requestSort('domain')} aria-sort={getAriaSort('domain')}>Domain{getSortIndicator('domain')}</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>Expires</th>
              <th style={{ padding: '12px 16px', color: 'var(--text-muted)', textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {sortedRequests.map((req) => (
              <tr key={req.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '16px' }}>{req.user_email}</td>
                <td style={{ padding: '16px', fontFamily: 'monospace' }}>{req.subdomain}</td>
                <td style={{ padding: '16px', fontFamily: 'monospace' }}>{req.domain}</td>
                <td style={{ padding: '16px' }}>{req.expires_at ? formatDate(req.expires_at) : 'Never'}</td>
                <td style={{ padding: '16px', textAlign: 'right' }}>
                  <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                    <button className="btn btn-primary" style={{ padding: '4px 12px', fontSize: '12px' }} onClick={() => handleAction(req.id, 'approve')}>Approve</button>
                    <button className="btn btn-secondary" style={{ padding: '4px 12px', fontSize: '12px' }} onClick={() => handleAction(req.id, 'reject')}>Reject</button>
                  </div>
                </td>
              </tr>
            ))}
            {requests.length === 0 && (
              <tr>
                <td colSpan={5} style={{ textAlign: 'center', padding: '40px 20px', color: 'var(--text-muted)' }}>
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
