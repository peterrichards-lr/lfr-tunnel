import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';

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
        <div className="card table-responsive">
          <table className="table">
            <thead>
              <tr>
                <th>Email</th>
                <th>IP Address</th>
                <th>Expires</th>
                <th>Used At</th>
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
                links.map((l, i) => (
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
      )}
    </div>
  );
}
