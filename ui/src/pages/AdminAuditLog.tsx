import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';

interface AuditEvent {
  event_id: string;
  actor: string;
  action: string;
  resource: string;
  ip_address: string;
  created_at: string;
  details: string;
}

export default function AdminAuditLog() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();

  const fetchEvents = async () => {
    try {
      const res = await axios.get('/api/admin/audit');
      setEvents(res.data.events || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchEvents();
  }, []);

  if (loading) return <div>Loading audit logs...</div>;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h3 style={{ margin: 0 }}>System Audit Log</h3>
          <p style={{ color: 'var(--text-muted)', marginTop: '4px' }}>Immutable record of administrative and security events.</p>
        </div>
        <a href="/api/admin/audit/export" className="btn btn-secondary">Export CSV</a>
      </div>
      
      <div className="card" style={{ padding: 0 }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Actor</th>
                <th>Action</th>
                <th>Resource</th>
                <th>IP Address</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {events.length === 0 ? (
                <tr>
                  <td colSpan={6} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>
                    No audit logs available.
                  </td>
                </tr>
              ) : (
                events.map(e => (
                  <tr key={e.event_id}>
                    <td style={{ whiteSpace: 'nowrap' }}>{formatDate(e.created_at)}</td>
                    <td>{e.actor}</td>
                    <td><span className="badge" style={{ background: 'var(--primary-dark)', color: 'white' }}>{e.action}</span></td>
                    <td>{e.resource}</td>
                    <td style={{ fontFamily: 'monospace' }}>{e.ip_address}</td>
                    <td style={{ fontSize: '11px', fontFamily: 'monospace', color: 'var(--text-muted)', maxWidth: '200px', overflowX: 'auto' }}>
                      {e.details}
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
