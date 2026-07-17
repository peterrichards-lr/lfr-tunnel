import React, { useEffect, useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import axios from 'axios';

export default function Dashboard() {
  const { user } = useOutletContext<{ user: any }>();
  const [tokens, setTokens] = useState<any[]>([]);

  useEffect(() => {
    // We could fetch tokens here if we need
    // For now, let's just mock or leave it empty, or fetch from /api/me tokens if included.
    // wait, /api/me does not include tokens in this backend?
    // Let's check: the vanilla JS hits loadTokens() which calls /api/me and reads currentUser.tokens? 
    // Yes, the Go API /api/me returns the User object which includes Tokens: user.Tokens
    setTokens(user.tokens || []);
  }, [user]);

  return (
    <div>
      <div className="glass" style={{ padding: '24px', borderRadius: '8px', marginBottom: '24px' }}>
        <h3>Your Personal Access Tokens</h3>
        <p style={{ color: 'var(--text-muted)', marginBottom: '16px' }}>
          Use these tokens to authenticate your CLI client.
        </p>
        
        {tokens.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '20px', color: 'var(--text-muted)' }}>
            No active tokens found. Create one using the CLI.
          </div>
        ) : (
          <table style={{ width: '100%', textAlign: 'left', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid rgba(255,255,255,0.1)' }}>
                <th style={{ padding: '12px 8px' }}>Token</th>
                <th style={{ padding: '12px 8px' }}>Created</th>
                <th style={{ padding: '12px 8px' }}>Expires</th>
                <th style={{ padding: '12px 8px' }}>Status</th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((t, idx) => (
                <tr key={idx} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '12px 8px', fontFamily: 'monospace' }}>{t.token}</td>
                  <td style={{ padding: '12px 8px' }}>{new Date(t.created_at).toLocaleDateString()}</td>
                  <td style={{ padding: '12px 8px' }}>{new Date(t.expires_at).toLocaleDateString()}</td>
                  <td style={{ padding: '12px 8px' }}>
                    <span className={`badge ${t.status === 'active' ? 'badge-success' : 'badge-danger'}`}>
                      {t.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
