import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';

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
  serverConfig?: any;
  user?: any;
}

export default function TunnelsPanel({ tunnels, serverConfig, user }: Props) {
  const { t } = useI18n();
  
  // Clean version strings (e.g. "v1.7.5" -> "1.7.5")
  const serverVer = serverConfig?.version?.replace('v', '') || '';
  const clientVer = user?.last_client_version?.replace('v', '') || '';
  const isClientOutdated = serverVer && clientVer && clientVer !== serverVer;

  const { items: sortedTunnels, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tunnels, ['subdomain_prefix', 'full_host', 'status', 'node_id']);


  return (
    <div id="tour-tunnels-panel" className="card" style={{ marginBottom: '24px', animationDelay: '0.1s' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>{t('active_tunnels', 'Active Tunnels')}</h3>
          <p style={{ color: 'var(--text-muted)', margin: '4px 0 0 0', fontSize: '14px' }}>
            {t('active_tunnels_desc', 'These are your currently active CLI connections routing traffic to your local machine.')}
          </p>
        </div>
      </div>

      {isClientOutdated && (
        <div style={{ padding: '12px 16px', background: 'rgba(234, 179, 8, 0.1)', border: '1px solid rgba(234, 179, 8, 0.3)', borderRadius: '8px', color: '#eab308', marginBottom: '16px', display: 'flex', alignItems: 'center', fontSize: '14px' }}>
          <span style={{ marginRight: '8px' }}>⚠️</span>
          <span>
            {t('update_available', 'Update Available:')} {t('update_available_desc', 'You are using CLI version')} <strong>v{clientVer}</strong>. {t('update_available_action', 'Please update to')} <strong>v{serverVer}</strong>.
          </span>
        </div>
      )}
      {tunnels.length > 0 && (
        <div style={{ marginBottom: '16px' }}>
          <input 
            type="text" 
            placeholder={t('search_active_tunnels_placeholder', 'Search active tunnels...')} 
            value={searchQuery} 
            onChange={e => setSearchQuery(e.target.value)}
            style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
          />
        </div>
      )}

      {tunnels.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>{t('no_active_tunnels', 'No active tunnels found. Connect your CLI to see tunnels here.')}</div>
        </div>
      ) : (
        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('subdomain_prefix')} aria-sort={getAriaSort('subdomain_prefix')}>{t('subdomain', 'Subdomain')}{getSortIndicator('subdomain_prefix')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>{t('target_host', 'Target Host')}{getSortIndicator('full_host')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>{t('status', 'Status')}{getSortIndicator('status')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>{t('node', 'Node')}{getSortIndicator('node_id')}</th>
              </tr>
            </thead>
            <tbody>
              {sortedTunnels.map((tItem, idx) => (
                <tr key={idx} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s', cursor: 'pointer' }} onMouseOver={e => e.currentTarget.style.background = 'rgba(255,255,255,0.03)'} onMouseOut={e => e.currentTarget.style.background = 'transparent'}>
                  <td style={{ padding: '16px', fontWeight: 600, fontSize: '14px' }}>{tItem.subdomain_prefix}</td>
                  <td style={{ padding: '16px', fontSize: '14px' }}>
                    <a href={`https://${tItem.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none', fontWeight: 500 }}>
                      {tItem.full_host}
                    </a>
                  </td>
                  <td style={{ padding: '16px' }}>
                    <span style={{ 
                      padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, 
                      background: 'var(--status-success-bg)', color: 'var(--status-success-text)', border: '1px solid var(--status-success-border)' 
                    }}>
                      {tItem.status ? tItem.status.toUpperCase() : 'UP'}
                    </span>
                  </td>
                  <td style={{ padding: '16px' }}>
                    {tItem.node_id && tItem.node_id !== 'control' ? (
                      <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'var(--status-node-bg)', color: 'var(--status-node-text)', border: '1px solid var(--status-node-border)' }}>
                        🌍 {tItem.node_id}
                      </span>
                    ) : (
                      <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                        🇬🇧 {t('control_node', 'Control')}
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
