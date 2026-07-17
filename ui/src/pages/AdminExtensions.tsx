import { useEffect, useState } from 'react';
import axios from 'axios';

interface ExtensionRequest {
  id: string;
  user_email: string;
  user_id: string;
  subdomain: string;
  domain: string;
  expires_at?: string;
}

export default function AdminExtensions() {
  const [requests, setRequests] = useState<ExtensionRequest[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchRequests = async () => {
    try {
      const res = await axios.get('/api/admin/reservations/extensions');
      setRequests(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchRequests();
  }, []);

  const approveExtension = async (id: string) => {
    const daysStr = prompt('Enter duration in days to extend (e.g. 30, 90). Leave blank for permanent:');
    if (daysStr === null) return; // cancelled
    
    let days = 0;
    if (daysStr.trim() !== '') {
      days = parseInt(daysStr, 10);
      if (isNaN(days) || days <= 0) {
        alert('Invalid number of days.');
        return;
      }
    }

    try {
      await axios.post(`/api/admin/reservations/${encodeURIComponent(id)}/approve-extension`, {
        duration_days: days
      });
      fetchRequests();
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to approve'}`);
    }
  };

  const demoteExtension = async (id: string) => {
    if (!confirm('Are you sure you want to reject this request and demote it back to a standard ephemeral lease?')) return;
    try {
      await axios.post(`/api/admin/reservations/${encodeURIComponent(id)}/demote`);
      fetchRequests();
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to reject'}`);
    }
  };

  if (loading) return <div>Loading extension requests...</div>;

  return (
    <div>
      <div style={{ marginBottom: '24px' }}>
        <h3>Subdomain Extension Requests</h3>
        <p style={{ color: 'var(--text-muted)' }}>Review user requests for long-lived or permanent subdomains.</p>
      </div>

      <div className="card" style={{ padding: 0 }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>User</th>
                <th>Subdomain</th>
                <th>Domain</th>
                <th>Current Expiry</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {requests.length === 0 ? (
                <tr>
                  <td colSpan={5} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>
                    No pending extension requests.
                  </td>
                </tr>
              ) : (
                requests.map(req => (
                  <tr key={req.id}>
                    <td style={{ fontWeight: 500 }}>{req.user_email || req.user_id}</td>
                    <td style={{ fontFamily: 'monospace' }}>{req.subdomain}</td>
                    <td style={{ fontFamily: 'monospace' }}>{req.domain}</td>
                    <td>{req.expires_at ? new Date(req.expires_at).toLocaleString() : 'Never'}</td>
                    <td>
                      <div style={{ display: 'flex', gap: '8px' }}>
                        <button className="btn btn-primary" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => approveExtension(req.id)}>
                          Approve
                        </button>
                        <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => demoteExtension(req.id)}>
                          Reject
                        </button>
                      </div>
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
