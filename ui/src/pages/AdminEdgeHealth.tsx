import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import { useSettings } from '../contexts/SettingsContext';

interface EdgeNode {
  id?: string;
  status: string;
  resolved_ip: string;
  latency_ms: number;
  last_check_at: number;
  error_message: string;
  version: string;
  created_at?: string;
}

export default function AdminEdgeHealth() {
  const [nodes, setNodes] = useState<Record<string, EdgeNode>>({});
  const [outboundOk, setOutboundOk] = useState<boolean>(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const { t } = useI18n();
  const { formatDate } = useSettings();
  const { showToast, showConfirm, showPrompt } = useUI();
  
  const fetchHealth = async () => {
    try {
      const res = await axios.get('/api/portal/edge-health');
      setNodes(res.data.nodes || res.data || {});
      setOutboundOk(res.data.outbound_ok !== false);
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

  const nodeArray = useMemo(() => {
    return Object.keys(nodes).map(id => ({ id, ...nodes[id] }));
  }, [nodes]);

  const columns: ColumnDef<EdgeNode>[] = useMemo(() => [
    { key: 'id', label: t('node', 'Node ID'), sortable: true },
    { key: 'status', label: t('status', 'Status'), sortable: true },
    { key: 'resolved_ip', label: t('resolved_ip', 'IP Address'), sortable: true },
    { key: 'latency_ms', label: t('latency', 'Latency'), sortable: true },
    { key: 'version', label: t('version', 'Version'), sortable: true },
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
  } = useDataTable<EdgeNode>(
    'admin_edge_health',
    nodeArray,
    ['id', 'status', 'resolved_ip', 'version'],
    columns,
    10,
    ['created_at'] // Default unselected
  );

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
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
            />
          </div>

          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
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
                    <td colSpan={7} className="td-empty">
                      {t('no_nodes_found', 'No edge nodes detected.')}
                    </td>
                  </tr>
                ) : (
                  paginatedItems.map((n: EdgeNode) => (
                    <tr key={n.id} className="border-b">
                      {isColumnVisible('id') && (
                        <td className="td-cell fw-bold">{n.id}</td>
                      )}
                      {isColumnVisible('status') && (
                        <td className="td-cell">
                          <span className={`badge ${n.status === 'online' ? 'badge-success' : 'badge-danger'}`}>
                            {n.status ? n.status.toUpperCase() : 'UNKNOWN'}
                          </span>
                        </td>
                      )}
                      {isColumnVisible('resolved_ip') && (
                        <td className="td-cell--mono">{n.resolved_ip || '—'}</td>
                      )}
                      {isColumnVisible('latency_ms') && (
                        <td className="td-cell">{n.latency_ms ? `${n.latency_ms}ms` : '—'}</td>
                      )}
                      {isColumnVisible('version') && (
                        <td className="td-cell--mono">{n.version || '—'}</td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>
                          {n.created_at ? formatDate(n.created_at) : '—'}
                        </td>
                      )}
                      <td className="td-cell text-right">
                        <div className="flex gap-xs justify-end">
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
