import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import EdgeScheduleModal from '../components/EdgeScheduleModal';
import EdgeDetailsModal from '../components/EdgeDetailsModal';
import ActionMenu from '../components/ActionMenu';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import { useSettings } from '../contexts/SettingsContext';

interface EdgeNode {
  id?: string;
  status: string;
  resolved_ip: string;
  resolved_ipv4?: string;
  resolved_ipv6?: string;
  latency_ms: number;
  last_check_at: number;
  error_message: string;
  version: string;
  created_at?: string;
  online_since?: number;
  timezone?: string;
}

// Uptime since a node's status last transitioned to Online (0/absent means
// not currently online) -- server-tracked in EdgeHealthStatus.OnlineSince,
// see server_edge.go's updateEdgeHealth.
function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (days > 0) parts.push(`${days}d`);
  if (hours > 0) parts.push(`${hours}h`);
  if (minutes > 0 || parts.length === 0) parts.push(`${minutes}m`);
  return parts.join(' ');
}

// The node's own local time (its configured schedule timezone), not the
// viewer's -- distinct from useSettings().formatDate, which always renders
// in the viewer's timezone/UTC preference.
function formatNodeLocalTime(tz?: string): string {
  if (!tz) return '—';
  try {
    return new Intl.DateTimeFormat(undefined, { timeZone: tz, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date());
  } catch {
    return '—';
  }
}

export default function AdminEdgeHealth() {
  const [nodes, setNodes] = useState<Record<string, EdgeNode>>({});
  const [outboundOk, setOutboundOk] = useState<boolean>(true);
  const [powerActionsEnabled, setPowerActionsEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [scheduleModalNodeId, setScheduleModalNodeId] = useState<string | null>(null);
  const [detailsNodeId, setDetailsNodeId] = useState<string | null>(null);
  const { t } = useI18n();
  const { formatDate } = useSettings();
  const { showToast, showConfirm, showPrompt } = useUI();

  const fetchHealth = async () => {
    try {
      const res = await axios.get('/api/portal/edge-health');
      setNodes(res.data.nodes || res.data || {});
      setOutboundOk(res.data.outbound_ok !== false);
      setPowerActionsEnabled(!!res.data.edge_power_actions_enabled);
      setError('');
    } catch (e: any) {
      setError(e.response?.data?.error || e.message || 'Failed to load network health');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchHealth();
    const interval = setInterval(fetchHealth, 30000);
    return () => clearInterval(interval);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const triggerEdgeAction = async (nodeId: string, action: string, reason = '', duration = 0) => {
    try {
      await axios.post('/api/portal/edge-action', {
        node_id: nodeId,
        action,
        reason,
        duration: parseInt(duration.toString(), 10) || 0
      });
      showToast('Action executed successfully.', 'success');
      fetchHealth();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Action failed.', 'error');
    }
  };

  const restartEdgeDaemon = async (nodeId: string) => {
    if (await showConfirm('Restart Edge Daemon', `Are you sure you want to restart the edge daemon for ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "restart");
    }
  };

  const enableEdgeMaintenance = async (nodeId: string) => {
    const reason = await showPrompt('Soft Maintenance Reason', `Enter a reason for enabling soft maintenance on ${nodeId}:`, "Edge Server Maintenance");
    if (reason === null) return;
    const durationStr = await showPrompt('Soft Maintenance Duration', `Enter duration in minutes for maintenance on ${nodeId}:`, "30");
    if (durationStr === null) return;
    const duration = parseInt(durationStr, 10);
    if (isNaN(duration) || duration <= 0) {
      showToast("Invalid duration.", 'error');
      return;
    }
    triggerEdgeAction(nodeId, "maintenance_enable", reason, duration);
  };

  const kickEdgeTunnels = async (nodeId: string) => {
    if (await showConfirm('Kick All Tunnels', `Are you sure you want to kick ALL active tunnels on edge node ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "kick_tunnels");
    }
  };

  // Power actions (start/stop/restart the underlying instance via the
  // optional edge-provisioner sidecar, #883/#888) -- distinct from
  // restartEdgeDaemon above, which restarts the already-running lfr-tunneld
  // process over its WebSocket control channel and can't do anything for a
  // stopped/unreachable instance.
  const triggerPowerAction = async (nodeId: string, action: 'start' | 'stop' | 'restart') => {
    try {
      await axios.post(`/api/admin/edge/${encodeURIComponent(nodeId)}/${action}`);
      showToast(`${action} requested for ${nodeId}.`, 'success');
      setTimeout(fetchHealth, 1500);
    } catch (e: any) {
      showToast(e.response?.data?.error || `${action} failed.`, 'error');
    }
  };

  const startNode = async (nodeId: string) => {
    if (await showConfirm('Start Node', `Start the ${nodeId} instance?`)) triggerPowerAction(nodeId, 'start');
  };
  const stopNode = async (nodeId: string) => {
    if (await showConfirm('Stop Node', `Stop the ${nodeId} instance? Any active tunnels through it will drop.`)) triggerPowerAction(nodeId, 'stop');
  };
  const restartNodeInstance = async (nodeId: string) => {
    if (await showConfirm('Restart Node', `Restart the ${nodeId} instance (stop, wait, start)? This can take a minute or more.`)) triggerPowerAction(nodeId, 'restart');
  };

  const toggleSelected = (nodeId: string, checked: boolean) => {
    setSelectedIds(prev => {
      const next = new Set(prev);
      if (checked) next.add(nodeId); else next.delete(nodeId);
      return next;
    });
  };

  const toggleSelectAll = (checked: boolean, ids: string[]) => {
    setSelectedIds(checked ? new Set(ids) : new Set());
  };

  const bulkAction = async (action: 'start' | 'stop' | 'restart') => {
    const nodeIds = Array.from(selectedIds);
    if (nodeIds.length === 0) return;
    if (!(await showConfirm(`${action[0].toUpperCase()}${action.slice(1)} selected`, `${action[0].toUpperCase()}${action.slice(1)} ${nodeIds.length} selected node(s)?`))) return;

    try {
      const res = await axios.post('/api/admin/edge/bulk', { node_ids: nodeIds, action });
      const results = res.data.results || {};
      const failed = Object.keys(results).filter(id => !results[id].ok);
      if (failed.length === 0) {
        showToast(`${action} requested for ${nodeIds.length} node(s).`, 'success');
      } else {
        showToast(`${action} failed for: ${failed.join(', ')}`, 'error');
      }
      setSelectedIds(new Set());
      setTimeout(fetchHealth, 1500);
    } catch (e: any) {
      showToast(e.response?.data?.error || `Bulk ${action} failed.`, 'error');
    }
  };

  const nodeArray = useMemo(() => {
    return Object.keys(nodes).map(id => {
      const rawStatus = nodes[id].status || 'offline';
      const normStatus = rawStatus.toLowerCase();
      return {
        id,
        ...nodes[id],
        computed_status: normStatus
      };
    });
  }, [nodes]);

  const statusOptions = useMemo(() => [
    { value: 'online', label: t('status_online', 'online') },
    { value: 'offline', label: t('status_offline', 'offline') },
    { value: 'disabled', label: t('status_disabled', 'disabled') }
  ], [t]);

  const columns: ColumnDef<EdgeNode>[] = useMemo(() => [
    { key: 'id', label: t('node', 'Node ID'), sortable: true },
    { key: 'status', label: t('status', 'Status'), sortable: true },
    { key: 'resolved_ip', label: t('resolved_ip', 'IP Address'), sortable: true },
    { key: 'latency_ms', label: t('latency', 'Latency'), sortable: true },
    { key: 'timezone', label: t('local_time', 'Local Time'), sortable: false },
    { key: 'online_since', label: t('uptime', 'Uptime'), sortable: true },
    { key: 'version', label: t('version', 'Version'), sortable: true },
    { key: 'created_at', label: t('created_at', 'Created Date'), sortable: true }
  ], [t]);

  const {
    paginatedItems,
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
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
  } = useDataTable<EdgeNode & { computed_status: string }>(
    'admin_edge_health',
    nodeArray,
    ['id', 'status', 'resolved_ip', 'version'],
    columns as any,
    10,
    ['resolved_ip', 'created_at'],
    'computed_status',
    statusOptions,
    'all'
  );

  if (loading) {
    return (
      <div className="animate-fade-in">
        <div className="mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={320} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h3 className="page-header__title">{t('network_edge_health', 'Network & Edge Health')}</h3>
          <p className="page-header__desc">{t('network_edge_health_desc', 'Global routing nodes, latency, and edge node actions.')}</p>
        </div>
      </div>

      {!outboundOk && (
        <div className="alert-banner alert-banner--warning mb-xl">
          ⚠️ {t('outbound_network_degraded', 'Outbound network connectivity is degraded.')}
        </div>
      )}

      {scheduleModalNodeId && (
        <EdgeScheduleModal
          nodeId={scheduleModalNodeId}
          onClose={() => setScheduleModalNodeId(null)}
          onSaved={fetchHealth}
        />
      )}

      {detailsNodeId && nodes[detailsNodeId] && (
        <EdgeDetailsModal
          nodeId={detailsNodeId}
          status={nodes[detailsNodeId].status}
          resolvedIPv4={nodes[detailsNodeId].resolved_ipv4}
          resolvedIPv6={nodes[detailsNodeId].resolved_ipv6}
          latencyMs={nodes[detailsNodeId].latency_ms}
          lastCheckAt={nodes[detailsNodeId].last_check_at}
          version={nodes[detailsNodeId].version}
          errorMessage={nodes[detailsNodeId].error_message}
          powerActionsEnabled={powerActionsEnabled}
          onClose={() => setDetailsNodeId(null)}
        />
      )}

      {powerActionsEnabled && selectedIds.size > 0 && (
        <div className="alert-banner mb-xl flex items-center gap-md">
          <span className="text-xs">{selectedIds.size} selected</span>
          <button className="btn btn-secondary text-xs py-xs px-sm" onClick={() => bulkAction('start')}>Start Selected</button>
          <button className="btn btn-secondary text-xs py-xs px-sm" onClick={() => bulkAction('stop')}>Stop Selected</button>
          <button className="btn btn-secondary text-xs py-xs px-sm" onClick={() => bulkAction('restart')}>Restart Selected</button>
        </div>
      )}

      {error ? (
        <div className="alert-banner alert-banner--danger mb-xl">
          {error}
        </div>
      ) : (
        <div className="card p-0">
          <div className="p-md border-b">
            <DataTableToolbar
              searchQuery={searchQuery}
              onSearchChange={setSearchQuery}
              searchPlaceholder={t('search_nodes_placeholder', 'Search edge nodes...')}
              pageSize={pageSize}
              onPageSizeChange={setPageSize}
              columns={columns}
              isColumnVisible={isColumnVisible}
              onToggleColumn={toggleColumn}
              statusFilter={statusFilter}
              onStatusFilterChange={setStatusFilter}
              statusOptions={statusOptions}
            />
          </div>

          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  {powerActionsEnabled && (
                    <th className="th-col" style={{ width: 32 }}>
                      <input
                        type="checkbox"
                        checked={paginatedItems.length > 0 && paginatedItems.every((n: EdgeNode) => n.id && selectedIds.has(n.id))}
                        onChange={e => toggleSelectAll(e.target.checked, paginatedItems.map((n: EdgeNode) => n.id).filter(Boolean) as string[])}
                      />
                    </th>
                  )}
                  {isColumnVisible('id') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('id')} aria-sort={getAriaSort('id')}>
                      {t('node', 'Node ID')}{getSortIndicator('id')}
                    </th>
                  )}
                  {isColumnVisible('status') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>
                      {t('status', 'Status')}{getSortIndicator('status')}
                    </th>
                  )}
                  {isColumnVisible('resolved_ip') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('resolved_ip')} aria-sort={getAriaSort('resolved_ip')}>
                      {t('resolved_ip', 'IP Address')}{getSortIndicator('resolved_ip')}
                    </th>
                  )}
                  {isColumnVisible('latency_ms') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('latency_ms')} aria-sort={getAriaSort('latency_ms')}>
                      {t('latency', 'Latency')}{getSortIndicator('latency_ms')}
                    </th>
                  )}
                  {isColumnVisible('timezone') && (
                    <th className="th-col">
                      {t('local_time', 'Local Time')}
                    </th>
                  )}
                  {isColumnVisible('online_since') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('online_since')} aria-sort={getAriaSort('online_since')}>
                      {t('uptime', 'Uptime')}{getSortIndicator('online_since')}
                    </th>
                  )}
                  {isColumnVisible('version') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('version')} aria-sort={getAriaSort('version')}>
                      {t('version', 'Version')}{getSortIndicator('version')}
                    </th>
                  )}
                  {isColumnVisible('created_at') && (
                    <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>
                      {t('created_at', 'Created Date')}{getSortIndicator('created_at')}
                    </th>
                  )}
                  <th className="th-col text-right">{t('actions', 'Actions')}</th>
                </tr>
              </thead>
              <tbody>
                {paginatedItems.length === 0 ? (
                  <tr>
                    <td colSpan={10} className="td-empty">
                      {t('no_nodes_found', 'No edge nodes detected.')}
                    </td>
                  </tr>
                ) : (
                  paginatedItems.map((n: EdgeNode) => (
                    <tr key={n.id} className="border-b">
                      {powerActionsEnabled && (
                        <td className="td-cell">
                          <input
                            type="checkbox"
                            checked={!!(n.id && selectedIds.has(n.id))}
                            onChange={e => n.id && toggleSelected(n.id, e.target.checked)}
                          />
                        </td>
                      )}
                      {isColumnVisible('id') && (
                        <td className="td-cell fw-bold">{n.id}</td>
                      )}
                      {isColumnVisible('status') && (
                        <td className="td-cell">
                          <span className={`badge ${n.status?.toLowerCase() === 'online' ? 'badge-success' : n.status?.toLowerCase() === 'disabled' ? 'badge-neutral' : 'badge-danger'}`}>
                            {n.status ? n.status.toLowerCase() : 'offline'}
                          </span>
                        </td>
                      )}
                      {isColumnVisible('resolved_ip') && (
                        <td className="td-cell--mono">{n.resolved_ip || '—'}</td>
                      )}
                      {isColumnVisible('latency_ms') && (
                        <td className="td-cell">{n.latency_ms ? `${n.latency_ms}ms` : '—'}</td>
                      )}
                      {isColumnVisible('timezone') && (
                        <td className="td-cell whitespace-nowrap">{formatNodeLocalTime(n.timezone)}</td>
                      )}
                      {isColumnVisible('online_since') && (
                        <td className="td-cell whitespace-nowrap">
                          {n.status?.toLowerCase() === 'online' && n.online_since
                            ? formatUptime(Math.max(0, Math.floor(Date.now() / 1000) - n.online_since))
                            : '—'}
                        </td>
                      )}
                      {isColumnVisible('version') && (
                        <td className="td-cell--mono">{n.version || '—'}</td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell whitespace-nowrap">
                          {n.created_at ? formatDate(n.created_at) : '—'}
                        </td>
                      )}
                      <td className="td-cell text-right">
                        <div className="flex gap-xs justify-end">
                          <button
                            className="btn btn-secondary text-xs py-xs px-sm"
                            title="View Details"
                            onClick={() => n.id && setDetailsNodeId(n.id)}
                          >
                            ℹ️
                          </button>
                          <button
                            className="btn btn-secondary text-xs py-xs px-sm"
                            title="Restart Daemon"
                            onClick={() => n.id && restartEdgeDaemon(n.id)}
                          >
                            🔄
                          </button>
                          <button
                            className="btn btn-secondary text-xs py-xs px-sm"
                            title="Soft Maintenance"
                            onClick={() => n.id && enableEdgeMaintenance(n.id)}
                          >
                            🚧
                          </button>
                          <button
                            className="btn btn-secondary text-xs text-danger py-xs px-sm"
                            title="Kick All Tunnels"
                            onClick={() => n.id && kickEdgeTunnels(n.id)}
                          >
                            ⚡
                          </button>
                          {powerActionsEnabled && n.id && (
                            <ActionMenu buttonTitle="Power actions">
                              {(close) => (
                                <>
                                  <button
                                    className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                    onClick={() => { close(); startNode(n.id!); }}
                                  >
                                    Start
                                  </button>
                                  <button
                                    className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                    onClick={() => { close(); stopNode(n.id!); }}
                                  >
                                    Stop
                                  </button>
                                  <button
                                    className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                    onClick={() => { close(); restartNodeInstance(n.id!); }}
                                  >
                                    Restart Instance
                                  </button>
                                  <div className="dropdown-menu-divider" />
                                  <button
                                    className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                    onClick={() => { close(); setScheduleModalNodeId(n.id!); }}
                                  >
                                    Edit Schedule
                                  </button>
                                </>
                              )}
                            </ActionMenu>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))
                )}
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
        </div>
      )}
    </div>
  );
}
