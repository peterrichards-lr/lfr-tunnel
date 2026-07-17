import { useEffect, useState } from 'react';
import axios from 'axios';

interface TunnelLease {
  user_id: string;
  subdomain_prefix: string;
  full_host: string;
  local_port: number;
  client_ip: string;
  status: string;
  bytes_in: number;
  bytes_out: number;
  created_at: string;
  node_id?: string;
  rate_limit?: number;
}

export default function AdminSubdomains() {
  const [leases, setLeases] = useState<TunnelLease[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchLeases = async () => {
    try {
      const res = await axios.get('/api/admin/leases');
      setLeases(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchLeases();
    const interval = setInterval(fetchLeases, 5000);
    return () => clearInterval(interval);
  }, []);

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const kickLease = async (subdomain: string) => {
    if (!confirm(`Are you sure you want to kick the tunnel for ${subdomain}?`)) return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
      fetchLeases();
    } catch {
      alert('Failed to kick lease');
    }
  };

  if (loading) return <div>Loading subdomains...</div>;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h3>Active Global Tunnels</h3>
      </div>
      <div className="card" style={{ padding: '0' }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>Subdomain</th>
                <th>Target Host</th>
                <th>Node</th>
                <th>Client IP</th>
                <th>Bytes In</th>
                <th>Bytes Out</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {leases.length === 0 ? (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', padding: '24px' }}>No active tunnels</td>
                </tr>
              ) : (
                leases.map((lease) => (
                  <tr key={lease.subdomain_prefix}>
                    <td style={{ fontWeight: 500 }}>{lease.subdomain_prefix}</td>
                    <td>
                      <a href={`https://${lease.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none' }}>
                        {lease.full_host}
                      </a>
                    </td>
                    <td>
                      {lease.node_id && lease.node_id !== 'control' ? (
                        <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                          🌍 {lease.node_id}
                        </span>
                      ) : (
                        <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                          🇬🇧 Control
                        </span>
                      )}
                    </td>
                    <td>{lease.client_ip}</td>
                    <td>{formatBytes(lease.bytes_in || 0)}</td>
                    <td>{formatBytes(lease.bytes_out || 0)}</td>
                    <td>
                      <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => kickLease(lease.subdomain_prefix)}>
                        Kick
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
