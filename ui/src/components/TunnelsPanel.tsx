interface Tunnel {
  subdomain_prefix: string;
  full_host: string;
  status: string;
  node_id?: string;
  client_ip?: string;
  local_port?: number;
}

interface Props {
  tunnels: Tunnel[];
}

export default function TunnelsPanel({ tunnels }: Props) {
  return (
    <div className="card" style={{ marginBottom: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <h3 style={{ margin: 0 }}>Active Tunnels</h3>
      </div>
      <p style={{ color: 'var(--text-muted)', marginBottom: '16px' }}>
        These are your currently active CLI connections routing traffic to your local machine.
      </p>

      {tunnels.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '20px', color: 'var(--text-muted)', border: '1px solid var(--border-color)', borderRadius: '4px' }}>
          No active tunnels found. Connect your CLI to see tunnels here.
        </div>
      ) : (
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>Subdomain</th>
                <th>Target Host</th>
                <th>Status</th>
                <th>Node</th>
              </tr>
            </thead>
            <tbody>
              {tunnels.map((t, idx) => (
                <tr key={idx}>
                  <td style={{ fontWeight: 500 }}>{t.subdomain_prefix}</td>
                  <td>
                    <a href={`https://${t.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none' }}>
                      {t.full_host}
                    </a>
                  </td>
                  <td>
                    <span className="badge success">{t.status || 'up'}</span>
                  </td>
                  <td>
                    {t.node_id && t.node_id !== 'control' ? (
                      <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                        🌍 {t.node_id}
                      </span>
                    ) : (
                      <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                        🇬🇧 Control
                      </span>
                    )}
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
