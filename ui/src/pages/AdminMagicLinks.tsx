import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

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

  const { items: sortedLinks, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(links, ['email', 'client_ip']);
  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ marginBottom: '24px' }}>
          <Skeleton width={180} height={28} />
          <Skeleton width={280} height={16} style={{ marginTop: '8px' }} />
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
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}><Skeleton width="90%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="85%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }



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
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('email')} aria-sort={getAriaSort('email')}>Email{getSortIndicator('email')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>IP Address{getSortIndicator('client_ip')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>Expires{getSortIndicator('expires_at')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('used_at')} aria-sort={getAriaSort('used_at')}>Used At{getSortIndicator('used_at')}</th>
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
