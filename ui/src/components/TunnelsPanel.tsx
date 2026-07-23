import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';

interface Tunnel {
  subdomain_prefix: string;
  full_host: string;
  status: string;
  node_id?: string;
  client_ip?: string;
  local_port?: number;
  bytes_in?: number;
  bytes_out?: number;
}

interface Props {
  tunnels: Tunnel[];
  serverConfig?: any;
  user?: any;
}

function formatBytes(bytes?: number): string {
  if (bytes === undefined || bytes === null) return '—';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function TunnelsPanel({ tunnels, serverConfig, user }: Props) {
  const { t } = useI18n();

  // Clean version strings (e.g. "v1.7.5" -> "1.7.5")
  const serverVer = serverConfig?.version?.replace('v', '') || '';
  const clientVer = user?.last_client_version?.replace('v', '') || '';
  const isClientOutdated = serverVer && clientVer && clientVer !== serverVer;

  const { items: sortedTunnels, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tunnels, ['subdomain_prefix', 'full_host', 'status', 'node_id']);

  return (
    <div id="tour-tunnels-panel" className="card mb-xl">
      <div className="section-header">
        <div>
          <h3 className="section-title">{t('active_tunnels', 'Active Tunnels')}</h3>
          <p className="section-desc">
            {t('active_tunnels_desc', 'These are your currently active CLI connections routing traffic to your local machine.')}
          </p>
        </div>
      </div>

      {isClientOutdated && (
        <div className="alert-banner alert-banner--warning">
          <span className="mr-sm">⚠️</span>
          <span>
            {t('update_available', 'Update Available:')} {t('update_available_desc', 'You are using CLI version')} <strong>v{clientVer}</strong>. {t('update_available_action', 'Please update to')} <strong>v{serverVer}</strong>.
          </span>
        </div>
      )}

      {tunnels.length > 0 && (
        <div className="search-row">
          <input
            type="text"
            placeholder={t('search_active_tunnels_placeholder', 'Search active tunnels...')}
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="search-input"
          />
        </div>
      )}

      {tunnels.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state__text">{t('no_active_tunnels', 'No active tunnels found. Connect your CLI to see tunnels here.')}</div>
        </div>
      ) : (
        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain_prefix')} aria-sort={getAriaSort('subdomain_prefix')}>{t('subdomain', 'Subdomain')}{getSortIndicator('subdomain_prefix')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>{t('target_host', 'Target Host')}{getSortIndicator('full_host')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>{t('status', 'Status')}{getSortIndicator('status')}</th>
                <th className="th-col th-col--sortable" onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>{t('node', 'Node')}{getSortIndicator('node_id')}</th>
                <th className="th-col">{t('client_ip', 'Client IP')}</th>
                <th className="th-col">{t('bytes_in', '↓ In')}</th>
                <th className="th-col">{t('bytes_out', '↑ Out')}</th>
              </tr>
            </thead>
            <tbody>
              {sortedTunnels.map((tItem, idx) => (
                <tr key={idx} className="border-b">
                  <td className="td-cell fw-semibold">{tItem.subdomain_prefix}</td>
                  <td className="td-cell">
                    <a href={`https://${tItem.full_host}`} target="_blank" rel="noreferrer" className="text-primary fw-medium no-underline">
                      {tItem.full_host}
                    </a>
                  </td>
                  <td className="td-cell">
                    <span className="badge badge-success">
                      {tItem.status ? tItem.status.toUpperCase() : 'UP'}
                    </span>
                  </td>
                  <td className="td-cell">
                    {tItem.node_id && tItem.node_id !== 'control' ? (
                      <span className="badge badge-node">
                        🌍 {tItem.node_id}
                      </span>
                    ) : (
                      <span className="badge badge-control">
                        🇬🇧 {t('control_node', 'Control')}
                      </span>
                    )}
                  </td>
                  <td className="td-cell--mono text-muted text-sm">
                    <span title={tItem.client_ip}>{tItem.client_ip || '—'}</span>
                    {tItem.local_port && (
                      <span className="text-2xs text-muted ml-xs">:{tItem.local_port}</span>
                    )}
                  </td>
                  <td className="td-cell text-muted text-sm">{formatBytes(tItem.bytes_in)}</td>
                  <td className="td-cell text-muted text-sm">{formatBytes(tItem.bytes_out)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
