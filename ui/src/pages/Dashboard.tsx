import { useEffect, useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import TunnelsPanel from '../components/TunnelsPanel';
import ReservationsPanel from '../components/ReservationsPanel';

export default function Dashboard() {
  const { user } = useOutletContext<{ user: any }>();
  const [tokens, setTokens] = useState<any[]>([]);

  useEffect(() => {
    setTokens(user.tokens || []);
  }, [user]);

  return (
    <div>
      <TunnelsPanel tunnels={user.tunnels || []} />
      
      <ReservationsPanel />

      <div className="card" style={{ marginBottom: '24px' }}>
        <h3 style={{ margin: 0, marginBottom: '16px' }}>Your Personal Access Tokens</h3>
        <p style={{ color: 'var(--text-muted)', marginBottom: '16px' }}>
          Use these tokens to authenticate your CLI client.
        </p>
        
        {tokens.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '20px', color: 'var(--text-muted)', border: '1px solid var(--border-color)', borderRadius: '4px' }}>
            No active tokens found. Create one using the CLI.
          </div>
        ) : (
          <div className="table-responsive">
            <table>
              <thead>
                <tr>
                  <th>Token</th>
                  <th>Created</th>
                  <th>Expires</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((t, idx) => (
                  <tr key={idx}>
                    <td style={{ fontFamily: 'monospace', fontWeight: 500 }}>{t.token}</td>
                    <td>{new Date(t.created_at).toLocaleDateString()}</td>
                    <td>{new Date(t.expires_at).toLocaleDateString()}</td>
                    <td>
                      <span className={`badge ${t.status === 'active' ? 'success' : 'danger'}`}>
                        {t.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
