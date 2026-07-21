import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';

interface MagicLink {
  email: string;
  client_ip: string;
  expires_at: number | string;
  used_at: number | string | null;
}

export default function AdminMagicLinks() {
  const { formatDate } = useSettings();
  const [links, setLinks] = useState<MagicLink[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const fetchLinks = async () => {
      try {
        const res = await axios.get('/api/admin/magic-links');
        setLinks(res.data || []);
      } catch (err: any) {
        setError(err.response?.data?.error || err.message || 'Failed to load magic links');
      } finally {
        setLoading(false);
      }
    };
    fetchLinks();
  }, []);

  if (loading) return <div>Loading active magic links...</div>;

  const { items: sortedLinks, requestSort, getSortIndicator, searchQuery, setSearchQuery } = useTableSort(links, ['email', 'client_ip']);


  const renderDate = (val: number | string | null | undefined) => {
    if (!val) return 'Unused';
    return formatDate(typeof val === 'number' ? new Date(val * 1000) : val);
  };

  return (
    <div>
      <div style={{ marginBottom: '24px' }}>
        <h3>Magic Links</h3>
        <p style={{ color: 'var(--text-muted)' }}>Track pending and unredeemed magic links.</p>
      </div>

      {error ? (
        <div style={{ color: 'var(--danger)', marginBottom: '20px' }}>
          {error}
        </div>
      ) : (
        <>
          {links.length > 0 && (
            <div style={{ marginBottom: '16px' }}>
              <input 
                type="text" 
                placeholder="Search magic links..." 
                value={searchQuery} 
                onChange={e => setSearchQuery(e.target.value)}
                style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
              />
            </div>
          )}
          <div className="card table-responsive">
            <table className="table">
              <thead>
                <tr>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('email')}>Email{getSortIndicator('email')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('client_ip')}>IP Address{getSortIndicator('client_ip')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('expires_at')}>Expires{getSortIndicator('expires_at')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('used_at')}>Used At{getSortIndicator('used_at')}</th>
                </tr>
              </thead>
              <tbody>
              {links.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ textAlign: 'center', opacity: 0.6, padding: '16px' }}>
                    No magic links found
                  </td>
                </tr>
              ) : (
                sortedLinks.map((l, i) => (
                  <tr key={i}>
                    <td style={{ fontWeight: 500 }}>{l.email}</td>
                    <td>{l.client_ip}</td>
                    <td>{renderDate(l.expires_at)}</td>
                    <td>{renderDate(l.used_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        </>
      )}
    </div>
  );
}
