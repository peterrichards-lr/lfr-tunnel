import { useEffect, useState } from 'react';
import axios from 'axios';

interface Reservation {
  id: string;
  subdomain: string;
  domain: string;
  status: string;
  expires_at?: string;
}

export default function ReservationsPanel() {
  const [reservations, setReservations] = useState<Reservation[]>([]);
  const [limit, setLimit] = useState(0);
  const [used, setUsed] = useState(0);
  const [loading, setLoading] = useState(true);

  const [domains, setDomains] = useState<string[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [subdomainInput, setSubdomainInput] = useState('');

  const fetchData = async () => {
    try {
      const [domRes, resRes] = await Promise.all([
        axios.get('/api/domains'),
        axios.get('/api/portal/reservations')
      ]);

      setDomains(domRes.data || []);
      if (domRes.data && domRes.data.length > 0 && !selectedDomain) {
        setSelectedDomain(domRes.data[0]);
      }

      setReservations(resRes.data.reservations || []);
      setLimit(resRes.data.limit || 0);
      setUsed(resRes.data.used || 0);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const generateSubdomain = async () => {
    try {
      const res = await axios.get('/api/portal/generate-subdomain');
      setSubdomainInput(res.data.subdomain);
    } catch {
      alert('Failed to generate subdomain');
    }
  };

  const createReservation = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!subdomainInput) {
      alert('Please enter or generate a subdomain');
      return;
    }
    try {
      await axios.post('/api/portal/reservations', {
        subdomain: subdomainInput.toLowerCase(),
        domain: selectedDomain
      });
      setSubdomainInput('');
      fetchData();
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to create reservation'}`);
    }
  };

  const deleteReservation = async (id: string) => {
    if (!confirm('Are you sure you want to release this subdomain?')) return;
    try {
      await axios.delete(`/api/portal/reservations/${encodeURIComponent(id)}`);
      fetchData();
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.error || 'Failed to delete'}`);
    }
  };

  if (loading) return <div>Loading reservations...</div>;

  const percent = limit > 0 ? (used / limit) * 100 : 0;
  const isAtLimit = limit >= 0 && used >= limit;

  return (
    <div className="card" style={{ marginBottom: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <h3 style={{ margin: 0 }}>Subdomain Reservations</h3>
      </div>
      
      <div style={{ marginBottom: '24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px', marginBottom: '8px' }}>
          <span>Reservation Quota</span>
          <span>{limit < 0 ? `${used} / ∞` : `${used} / ${limit}`} reserved</span>
        </div>
        <div style={{ height: '8px', background: 'rgba(255,255,255,0.1)', borderRadius: '4px', overflow: 'hidden' }}>
          <div style={{ height: '100%', width: `${Math.min(percent, 100)}%`, background: isAtLimit ? 'var(--danger)' : 'var(--primary)', transition: 'width 0.3s' }}></div>
        </div>
        {isAtLimit && limit >= 0 && (
          <div style={{ marginTop: '8px', fontSize: '12px', color: 'var(--warning)' }}>
            ⚠️ You have reached your reservation limit. Release a subdomain to register a new one.
          </div>
        )}
      </div>

      {!isAtLimit && (
        <form onSubmit={createReservation} style={{ display: 'flex', gap: '8px', marginBottom: '24px', flexWrap: 'wrap' }}>
          <div style={{ flex: '1', minWidth: '150px' }}>
            <input 
              type="text" 
              className="form-control" 
              placeholder="subdomain" 
              value={subdomainInput} 
              onChange={(e) => setSubdomainInput(e.target.value)} 
            />
          </div>
          <div style={{ flex: '1', minWidth: '150px' }}>
            <select className="form-control" value={selectedDomain} onChange={(e) => setSelectedDomain(e.target.value)}>
              {domains.map(d => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
          </div>
          <button type="button" className="btn btn-secondary" onClick={generateSubdomain}>Generate</button>
          <button type="submit" className="btn btn-primary">Reserve</button>
        </form>
      )}

      {reservations.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '20px', color: 'var(--text-muted)', border: '1px solid var(--border-color)', borderRadius: '4px' }}>
          No subdomains reserved.
        </div>
      ) : (
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>Subdomain</th>
                <th>Domain</th>
                <th>Status</th>
                <th>Expires</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {reservations.map(r => (
                <tr key={r.id}>
                  <td style={{ fontWeight: 500 }}>{r.subdomain}</td>
                  <td style={{ color: 'var(--text-muted)' }}>{r.domain}</td>
                  <td>
                    <span className={`badge ${r.status === 'active' ? 'success' : 'warning'}`}>
                      {r.status}
                    </span>
                  </td>
                  <td>{r.expires_at ? new Date(r.expires_at).toLocaleDateString() : 'Never'}</td>
                  <td>
                    <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => deleteReservation(r.id)}>
                      Release
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
