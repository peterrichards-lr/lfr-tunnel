import { useMemo } from 'react';
import { useI18n } from '../contexts/I18nContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import { useSettings } from '../contexts/SettingsContext';

interface Tunnel {
  subdomain_prefix: string;
  full_host: string;
  status: string;
  node_id?: string;
  client_ip?: string;
  local_port?: number;
  bytes_in?: number;
  bytes_out?: number;
  created_at?: string;
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
  const { formatDate } = useSettings();

  const serverVer = serverConfig?.version?.replace('v', '') || '';
  const clientVer = user?.last_client_version?.replace('v', '') || '';
  const isClientOutdated = serverVer && clientVer && clientVer !== serverVer;

  const columns: ColumnDef<Tunnel>[] = useMemo(() => [
    { key: 'subdomain_prefix', label: t('subdomain', 'Subdomain'), sortable: true },
    { key: 'full_host', label: t('target_host', 'Target Host'), sortable: true },
    { key: 'status', label: t('status', 'Status'), sortable: true },
    { key: 'node_id', label: t('node', 'Node'), sortable: true },
    { key: 'client_ip', label: t('client_ip', 'Client IP'), sortable: true },
    { key: 'bytes_in', label: t('bytes_in', '↓ In'), sortable: true },
    { key: 'bytes_out', label: t('bytes_out', '↑ Out'), sortable: true },
    { key: 'created_at', label: t('created_at', 'Created Date'), sortable: true }
  ], [t]);

  const {
    paginatedItems,
    searchQuery,
    setSearchQuery,
    pageSize,
    setPageSize,
    currentPage,
    setCurrentPage,
    totalPages,
    totalItems,
    isColumnVisible,
    toggleColumn,
    requestSort,
    getSortIndicator,
    getAriaSort
  } = useDataTable<Tunnel>(
    'dashboard_tunnels',
    tunnels,
    ['subdomain_prefix', 'full_host', 'status', 'node_id', 'client_ip'],
    columns,
    10,
    ['created_at'] // Default unselected
  );

  return (
    <div id="tour-tunnels-panel" className="card mb-xl p-0">
      <div className="p-xl border-b">
        <div className="section-header">
          <div>
            <h3 className="section-title">{t('active_tunnels', 'Active Tunnels')}</h3>
            <p className="section-desc">
              {t('active_tunnels_desc', 'These are your currently active CLI connections routing traffic to your local machine.')}
            </p>
          </div>
        </div>

        {isClientOutdated && (
          <div className="alert-banner alert-banner--warning mt-md">
            <span className="mr-sm">⚠️</span>
            <span>
              {t('update_available', 'Update Available:')} {t('update_available_desc', 'You are using CLI version')} <strong>v{clientVer}</strong>. {t('update_available_action', 'Please update to')} <strong>v{serverVer}</strong>.
            </span>
          </div>
        )}
      </div>

      {tunnels.length > 0 && (
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t('search_active_tunnels_placeholder', 'Search active tunnels...')}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={columns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
          />
        </div>
      )}

      {tunnels.length === 0 ? (
        <div className="empty-state p-xl">
          <div className="empty-state__text">{t('no_active_tunnels', 'No active tunnels found. Connect your CLI to see tunnels here.')}</div>
        </div>
      ) : (
        <>
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  {isColumnVisible('subdomain_prefix') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain_prefix')} aria-sort={getAriaSort('subdomain_prefix')}>
                      {t('subdomain', 'Subdomain')}{getSortIndicator('subdomain_prefix')}
                    </th>
                  )}
                  {isColumnVisible('full_host') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>
                      {t('target_host', 'Target Host')}{getSortIndicator('full_host')}
                    </th>
                  )}
                  {isColumnVisible('status') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>
                      {t('status', 'Status')}{getSortIndicator('status')}
                    </th>
                  )}
                  {isColumnVisible('node_id') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>
                      {t('node', 'Node')}{getSortIndicator('node_id')}
                    </th>
                  )}
                  {isColumnVisible('client_ip') && (
                    <th className="th-col">{t('client_ip', 'Client IP')}</th>
                  )}
                  {isColumnVisible('bytes_in') && (
                    <th className="th-col">{t('bytes_in', '↓ In')}</th>
                  )}
                  {isColumnVisible('bytes_out') && (
                    <th className="th-col">{t('bytes_out', '↑ Out')}</th>
                  )}
                  {isColumnVisible('created_at') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>
                      {t('created_at', 'Created Date')}{getSortIndicator('created_at')}
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {paginatedItems.map((tItem: Tunnel, idx: number) => (
                  <tr key={idx} className="border-b">
                    {isColumnVisible('subdomain_prefix') && (
                      <td className="td-cell fw-semibold">{tItem.subdomain_prefix}</td>
                    )}
                    {isColumnVisible('full_host') && (
                      <td className="td-cell">
                        <a href={`https://${tItem.full_host}`} target="_blank" rel="noreferrer" className="text-primary fw-medium no-underline">
                          {tItem.full_host}
                        </a>
                      </td>
                    )}
                    {isColumnVisible('status') && (
                      <td className="td-cell">
                        <span className="badge badge-success">
                          {tItem.status ? tItem.status.toUpperCase() : 'UP'}
                        </span>
                      </td>
                    )}
                    {isColumnVisible('node_id') && (
                      <td className="td-cell font-mono text-xs">{tItem.node_id || 'primary'}</td>
                    )}
                    {isColumnVisible('client_ip') && (
                      <td className="td-cell font-mono text-xs">{tItem.client_ip || '—'}</td>
                    )}
                    {isColumnVisible('bytes_in') && (
                      <td className="td-cell text-xs text-muted">{formatBytes(tItem.bytes_in)}</td>
                    )}
                    {isColumnVisible('bytes_out') && (
                      <td className="td-cell text-xs text-muted">{formatBytes(tItem.bytes_out)}</td>
                    )}
                    {isColumnVisible('created_at') && (
                      <td className="td-cell text-xs text-muted" style={{ whiteSpace: 'nowrap' }}>
                        {tItem.created_at ? formatDate(tItem.created_at) : '—'}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <DataTablePagination
            currentPage={currentPage}
            totalPages={totalPages}
            pageSize={pageSize}
            totalItems={totalItems}
            onPageChange={setCurrentPage}
          />
        </>
      )}
    </div>
  );
}
