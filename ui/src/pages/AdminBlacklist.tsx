import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';

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

  if (loading) return <div>Loading blacklist...</div>;

  return (
    <div>
      <div style={{ marginBottom: '24px' }}>
        <h3>IP Blacklist</h3>
        <p style={{ color: 'var(--text-muted)' }}>Manage explicitly blocked IP addresses.</p>
      </div>
      
      <div className="card" style={{ marginBottom: '24px' }}>
        <form onSubmit={addEntry} style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
          <input 
            type="text" 
            className="form-control" 
            placeholder="IP Address" 
            value={ipInput} 
            onChange={(e) => setIpInput(e.target.value)} 
            style={{ flex: 1, minWidth: '150px' }}
          />
          <input 
            type="text" 
            className="form-control" 
            placeholder="Reason (optional)" 
            value={reasonInput} 
            onChange={(e) => setReasonInput(e.target.value)} 
            style={{ flex: 2, minWidth: '200px' }}
          />
          <button type="submit" className="btn btn-danger">Block IP</button>
        </form>
      </div>

      <div className="card" style={{ padding: 0 }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>IP Address</th>
                <th>Reason</th>
                <th>Blocked At</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {entries.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>
                    No IP addresses are currently blocked.
                  </td>
                </tr>
              ) : (
                entries.map(entry => (
                  <tr key={entry.ip}>
                    <td style={{ fontFamily: 'monospace', fontWeight: 500 }}>{entry.ip}</td>
                    <td>{entry.reason}</td>
                    <td>{formatDate(entry.created_at)}</td>
                    <td>
                      <button className="btn btn-secondary" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => removeEntry(entry.ip)}>
                        Unblock
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
