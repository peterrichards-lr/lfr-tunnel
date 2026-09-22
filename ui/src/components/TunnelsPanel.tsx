import { useMemo, useState } from 'react';
import { useI18n } from '../contexts/I18nContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import { useSettings } from '../contexts/SettingsContext';
import SectionHeading from './SectionHeading';

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

// One client's tunnels, as one row (#2143, extending #2129 to this table).
//
// A client that maps several ports gets one lease per port, so this panel showed the same
// client several times over, repeating its node, IP and status. V1's Active Tunnels, V1's
// Telemetry and V2's Telemetry were grouped in #2129; this one was missed, which is an arm
// difference of exactly the kind the A/B rule forbids.
//
// A group carries the SAME field names as a Tunnel so useDataTable keeps sorting, searching and
// paginating without knowing anything about grouping -- and crucially it paginates GROUPS, so a
// client's rows can never be split across a page boundary.
type TunnelGroup = Tunnel & { members: Tunnel[]; groupKey: string };

function groupTunnels(list: Tunnel[]): TunnelGroup[] {
  const byKey = new Map<string, TunnelGroup>();
  list.forEach((t, index) => {
    // No prefix, no grouping: an empty prefix is not a value to group ON, and treating it as
    // one merges unrelated clients that merely share a node (#2129).
    const key = t.subdomain_prefix
      ? `${t.subdomain_prefix}|${t.node_id || ''}`
      : `ungrouped:${index}`;
    const existing = byKey.get(key);
    if (!existing) {
      byKey.set(key, { ...t, members: [t], groupKey: key });
      return;
    }
    existing.members.push(t);
    existing.bytes_in = (existing.bytes_in || 0) + (t.bytes_in || 0);
    existing.bytes_out = (existing.bytes_out || 0) + (t.bytes_out || 0);
    // The host column describes a single tunnel, and a group has several. Blanked rather than
    // showing whichever one happened to arrive first.
    existing.full_host = '';
  });

  const groups = Array.from(byKey.values());
  groups.forEach((g) =>
    g.members.sort((a, b) =>
      (a.full_host || '').localeCompare(b.full_host || ''),
    ),
  );
  return groups;
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

  const columns: ColumnDef<Tunnel>[] = useMemo(
    () => [
      {
        key: 'subdomain_prefix',
        label: t('subdomain', 'Subdomain'),
        sortable: true,
      },
      {
        key: 'full_host',
        label: t('target_host', 'Target Host'),
        sortable: true,
      },
      { key: 'status', label: t('status', 'Status'), sortable: true },
      { key: 'node_id', label: t('node', 'Node'), sortable: true },
      { key: 'client_ip', label: t('client_ip', 'Client IP'), sortable: true },
      { key: 'bytes_in', label: t('bytes_in', '↓ In'), sortable: true },
      { key: 'bytes_out', label: t('bytes_out', '↑ Out'), sortable: true },
      {
        key: 'created_at',
        label: t('created_at', 'Created Date'),
        sortable: true,
      },
    ],
    [t],
  );

  // Grouped BEFORE the table sees them, so pagination counts clients rather than ports and a
  // client's rows cannot straddle a page boundary.
  const grouped = useMemo(() => groupTunnels(tunnels), [tunnels]);

  const [expanded, setExpanded] = useState<Set<string>>(new Set());

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
    getAriaSort,
  } = useDataTable<TunnelGroup>(
    'dashboard_tunnels',
    grouped,
    ['subdomain_prefix', 'full_host', 'status', 'node_id', 'client_ip'],
    columns,
    10,
    ['created_at'], // Default unselected
  );

  return (
    <div id="tour-tunnels-panel" className="card mb-xl p-0">
      <div className="p-xl border-b">
        <div className="section-header">
          <div>
            <SectionHeading
              anchor="active-tunnels"
              className="section-title"
              label={t('active_tunnels', 'Active Tunnels')}
            />
            <p className="section-desc">
              {t(
                'active_tunnels_desc',
                'These are your currently active CLI connections routing traffic to your local machine.',
              )}
            </p>
          </div>
        </div>

        {isClientOutdated && (
          <div className="alert-banner alert-banner--warning mt-md">
            <span className="mr-sm">⚠️</span>
            <span>
              {t('update_available', 'Update Available')}:{' '}
              {t('update_available_desc', 'You are using CLI version')}{' '}
              <strong>v{clientVer}</strong>.{' '}
              {t('update_available_action', 'Please update to')}{' '}
              <strong>v{serverVer}</strong>.
            </span>
          </div>
        )}
      </div>

      {tunnels.length > 0 && (
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t(
              'search_active_tunnels_placeholder',
              'Search active tunnels...',
            )}
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
          <div className="empty-state__text">
            {t(
              'no_active_tunnels',
              'No active tunnels found. Connect your CLI to see tunnels here.',
            )}
          </div>
        </div>
      ) : (
        <>
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  {isColumnVisible('subdomain_prefix') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('subdomain_prefix')}
                      aria-sort={getAriaSort('subdomain_prefix')}
                    >
                      {t('subdomain', 'Subdomain')}
                      {getSortIndicator('subdomain_prefix')}
                    </th>
                  )}
                  {isColumnVisible('full_host') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('full_host')}
                      aria-sort={getAriaSort('full_host')}
                    >
                      {t('target_host', 'Target Host')}
                      {getSortIndicator('full_host')}
                    </th>
                  )}
                  {isColumnVisible('status') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('status')}
                      aria-sort={getAriaSort('status')}
                    >
                      {t('status', 'Status')}
                      {getSortIndicator('status')}
                    </th>
                  )}
                  {isColumnVisible('node_id') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('node_id')}
                      aria-sort={getAriaSort('node_id')}
                    >
                      {t('node', 'Node')}
                      {getSortIndicator('node_id')}
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
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('created_at')}
                      aria-sort={getAriaSort('created_at')}
                    >
                      {t('created_at', 'Created Date')}
                      {getSortIndicator('created_at')}
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {paginatedItems.flatMap((tItem: TunnelGroup, idx: number) => {
                  const isGroup = tItem.members.length > 1;
                  const isOpen = expanded.has(tItem.groupKey);
                  const toggle = () =>
                    setExpanded((prev) => {
                      const next = new Set(prev);
                      if (next.has(tItem.groupKey)) next.delete(tItem.groupKey);
                      else next.add(tItem.groupKey);
                      return next;
                    });

                  const children =
                    isGroup && isOpen
                      ? tItem.members.map((m, i) => (
                          <tr
                            key={`${tItem.groupKey}|${m.full_host}|${i}`}
                            className="border-b"
                          >
                            {isColumnVisible('subdomain_prefix') && (
                              <td className="td-cell" />
                            )}
                            {isColumnVisible('full_host') && (
                              <td className="td-cell pl-lg">
                                <a
                                  href={`https://${m.full_host}`}
                                  target="_blank"
                                  rel="noreferrer"
                                  className="text-primary fw-medium no-underline"
                                >
                                  {m.full_host}
                                </a>
                              </td>
                            )}
                            {/* Node, status and client IP describe the CLIENT, so they are
                                absent here rather than repeated -- status especially, since
                                one down port marks the whole session down. */}
                            {isColumnVisible('status') && (
                              <td className="td-cell" />
                            )}
                            {isColumnVisible('node_id') && (
                              <td className="td-cell" />
                            )}
                            {isColumnVisible('client_ip') && (
                              <td className="td-cell" />
                            )}
                            {isColumnVisible('bytes_in') && (
                              <td className="td-cell text-xs text-muted">
                                {formatBytes(m.bytes_in)}
                              </td>
                            )}
                            {isColumnVisible('bytes_out') && (
                              <td className="td-cell text-xs text-muted">
                                {formatBytes(m.bytes_out)}
                              </td>
                            )}
                            {isColumnVisible('created_at') && (
                              <td className="td-cell" />
                            )}
                          </tr>
                        ))
                      : [];

                  return [
                    <tr key={tItem.groupKey || idx} className="border-b">
                      {isColumnVisible('subdomain_prefix') && (
                        <td className="td-cell fw-semibold">
                          {isGroup && (
                            <button
                              type="button"
                              onClick={toggle}
                              aria-expanded={isOpen}
                              aria-label={`${isOpen ? 'Collapse' : 'Expand'} ${tItem.subdomain_prefix}`}
                              className="tunnel-group-toggle"
                            >
                              {isOpen ? '▾' : '▸'}
                            </button>
                          )}
                          {tItem.subdomain_prefix}
                          {isGroup && (
                            <span className="text-muted text-xs ml-sm">
                              {tItem.members.length} {t('tunnels', 'tunnels')}
                            </span>
                          )}
                        </td>
                      )}
                      {isColumnVisible('full_host') && (
                        <td className="td-cell">
                          <a
                            href={`https://${tItem.full_host}`}
                            target="_blank"
                            rel="noreferrer"
                            className="text-primary fw-medium no-underline"
                          >
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
                        <td className="td-cell font-mono text-xs">
                          {tItem.node_id || 'primary'}
                        </td>
                      )}
                      {isColumnVisible('client_ip') && (
                        <td className="td-cell font-mono text-xs">
                          {tItem.client_ip || '—'}
                        </td>
                      )}
                      {isColumnVisible('bytes_in') && (
                        <td className="td-cell text-xs text-muted">
                          {formatBytes(tItem.bytes_in)}
                        </td>
                      )}
                      {isColumnVisible('bytes_out') && (
                        <td className="td-cell text-xs text-muted">
                          {formatBytes(tItem.bytes_out)}
                        </td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell text-xs text-muted whitespace-nowrap">
                          {tItem.created_at
                            ? formatDate(tItem.created_at)
                            : '—'}
                        </td>
                      )}
                    </tr>,
                    ...children,
                  ];
                })}
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
