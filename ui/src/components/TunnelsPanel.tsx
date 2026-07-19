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
    <div className="card" style={{ marginBottom: '24px', animationDelay: '0.1s' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>Active Tunnels</h3>
          <p style={{ color: 'var(--text-muted)', margin: '4px 0 0 0', fontSize: '14px' }}>
            These are your currently active CLI connections routing traffic to your local machine.
          </p>
        </div>
      </div>

      {tunnels.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>No active tunnels found. Connect your CLI to see tunnels here.</div>
        </div>
      ) : (
        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Subdomain</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Target Host</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Status</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Node</th>
              </tr>
            </thead>
            <tbody>
              {tunnels.map((t, idx) => (
                <tr key={idx} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s', cursor: 'pointer' }} onMouseOver={e => e.currentTarget.style.background = 'rgba(255,255,255,0.03)'} onMouseOut={e => e.currentTarget.style.background = 'transparent'}>
                  <td style={{ padding: '16px', fontWeight: 600, fontSize: '14px' }}>{t.subdomain_prefix}</td>
                  <td style={{ padding: '16px', fontSize: '14px' }}>
                    <a href={`https://${t.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none', fontWeight: 500 }}>
                      {t.full_host}
                    </a>
                  </td>
                  <td style={{ padding: '16px' }}>
                    <span style={{ 
                      padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, 
                      background: 'rgba(16, 185, 129, 0.15)', color: '#34d399', border: '1px solid rgba(16, 185, 129, 0.3)' 
                    }}>
                      {t.status ? t.status.toUpperCase() : 'UP'}
                    </span>
                  </td>
                  <td style={{ padding: '16px' }}>
                    {t.node_id && t.node_id !== 'control' ? (
                      <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                        🌍 {t.node_id}
                      </span>
                    ) : (
                      <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
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
