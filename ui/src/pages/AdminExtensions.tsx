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
        <h3 className="section-title mb-lg">
          <Skeleton width={180} height={24} />
        </h3>
        
        <div className="search-row">
          <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="th-col"><Skeleton width={120} /></th>
                <th className="th-col"><Skeleton width={80} /></th>
                <th className="th-col"><Skeleton width={80} /></th>
                <th className="th-col"><Skeleton width={80} /></th>
                <th className="th-col"><Skeleton width={100} /></th>
              </tr>
            </thead>
            <tbody>
              {[...Array(3)].map((_, i) => (
                <tr key={i} className="border-b">
                  <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                  <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                  <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                  <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                  <td className="td-cell"><Skeleton width="80%" height={16} /></td>
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
      <h3 className="section-title mb-lg">Extension Requests</h3>
      <div className="search-row">
        <input 
          type="text" 
          placeholder={t('search_extensions_placeholder', 'Search extensions...')} 
          value={searchQuery} 
          onChange={e => setSearchQuery(e.target.value)}
          className="search-input"
        />
      </div>

      <div className="table-responsive">
        <table className="w-full">
          <thead>
            <tr className="border-b text-left">
              <th className="th-col th-col--sortable" onClick={() => requestSort('user_email')} aria-sort={getAriaSort('user_email')}>
                Email{getSortIndicator('user_email')}
              </th>
              <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>
                Subdomain{getSortIndicator('subdomain')}
              </th>
              <th className="th-col th-col--sortable" onClick={() => requestSort('domain')} aria-sort={getAriaSort('domain')}>
                Domain{getSortIndicator('domain')}
              </th>
              <th className="th-col">Expires</th>
              <th className="th-col text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {sortedRequests.map((req) => (
              <tr key={req.id} className="border-b">
                <td className="td-cell">{req.user_email}</td>
                <td className="td-cell--mono">{req.subdomain}</td>
                <td className="td-cell--mono">{req.domain}</td>
                <td className="td-cell">{req.expires_at ? formatDate(req.expires_at) : 'Never'}</td>
                <td className="td-cell text-right">
                  <div className="flex gap-sm justify-end">
                    <button className="btn btn-primary px-md py-xs text-xs" onClick={() => handleAction(req.id, 'approve')}>Approve</button>
                    <button className="btn btn-secondary px-md py-xs text-xs" onClick={() => handleAction(req.id, 'reject')}>Reject</button>
                  </div>
                </td>
              </tr>
            ))}
            {sortedRequests.length === 0 && (
              <tr>
                <td colSpan={5} className="td-empty">
                  {searchQuery
                    ? t('no_extensions_match', 'No extension requests match your search.')
                    : t('no_extensions_found', 'No extension requests found.')}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
