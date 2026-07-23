import { useEffect, useState } from 'react';
import axios from 'axios';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface EdgeNode {
  status: string;
  resolved_ip: string;
  latency_ms: number;
  last_check_at: number;
  error_message: string;
  version: string;
}

export default function AdminEdgeHealth() {
  const [nodes, setNodes] = useState<Record<string, EdgeNode>>({});
  const [outboundOk, setOutboundOk] = useState<boolean>(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const { t } = useI18n();
  const { showToast, showConfirm, showPrompt } = useUI();
  
  // Track open menus and their viewport position
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [menuPos, setMenuPos] = useState<{ top: number; right: number } | null>(null);

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
    
    // Close menus when clicking outside
    const handleGlobalClick = () => setOpenMenu(null);
    document.addEventListener('click', handleGlobalClick);
    
    return () => {
      clearInterval(interval);
      document.removeEventListener('click', handleGlobalClick);
    };
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
    setOpenMenu(null);
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

  const disableEdgeMaintenance = async (nodeId: string) => {
    if (await showConfirm('Disable Maintenance', `Are you sure you want to disable maintenance on ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "maintenance_disable");
    }
  };

  const kickEdgeTunnels = async (nodeId: string) => {
    if (await showConfirm('Kick All Tunnels', `Are you sure you want to kick ALL active tunnels on edge node ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "kick_tunnels");
    }
  };

  const toggleMenu = (e: React.MouseEvent, id: string) => {
    e.stopPropagation();
    if (openMenu === id) {
      setOpenMenu(null);
      setMenuPos(null);
    } else {
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
      setMenuPos({ top: rect.bottom + window.scrollY + 4, right: window.innerWidth - rect.right });
      setOpenMenu(id);
    }
  };

  const nodeArray = Object.keys(nodes).map(id => ({ id, ...nodes[id] }));
  const { items: sortedNodes, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(nodeArray, ['id', 'status', 'resolved_ip', 'version']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={320} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl mb-xl">
          <div className="flex gap-md items-center">
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                    <td className="td-cell"><Skeleton width={50} height={20} borderRadius={10} /></td>
                    <td className="td-cell"><Skeleton width="80%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width={40} height={24} /></td>
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
      <div className="mb-xl">
        <h3 className="page-header__title">Network Health</h3>
        <p className="page-header__desc">Monitor the real-time status, latency, and uptime of regional stateless edge nodes.</p>
      </div>

      {!outboundOk && (
        <div className="alert-banner alert-banner--danger mb-xl">
          ⚠️ <strong>Gateway Network Error:</strong> The central gateway has lost outbound internet connectivity. Regional health checks are suspended.
        </div>
      )}

      {error ? (
        <div className="text-danger mb-lg">
          {error}
        </div>
      ) : (
        <>
          {nodeArray.length > 0 && (
            <div className="search-row">
              <input 
                type="text" 
                placeholder={t('search_nodes_placeholder', 'Search nodes...')} 
                value={searchQuery} 
                onChange={e => setSearchQuery(e.target.value)}
                className="search-input"
              />
            </div>
          )}
          <div className="card table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col th-col--sortable" onClick={() => requestSort('id')} aria-sort={getAriaSort('id')}>Node ID{getSortIndicator('id')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('resolved_ip')} aria-sort={getAriaSort('resolved_ip')}>Resolved IP{getSortIndicator('resolved_ip')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>Status{getSortIndicator('status')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('latency_ms')} aria-sort={getAriaSort('latency_ms')}>Latency{getSortIndicator('latency_ms')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('last_check_at')} aria-sort={getAriaSort('last_check_at')}>Last Check{getSortIndicator('last_check_at')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('version')} aria-sort={getAriaSort('version')}>Version{getSortIndicator('version')}</th>
                  <th className="th-col">Error</th>
                  <th className="th-col text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {sortedNodes.length === 0 ? (
                <tr>
                  <td colSpan={8} className="td-empty opacity-60">
                    No edge nodes configured
                  </td>
                </tr>
              ) : (
                sortedNodes.map(h => {
                  const id = h.id;
                  const isOnline = h.status === 'Online';
                  
                  const resolvedIP = h.resolved_ip || '-';
                  const latText = isOnline ? `${h.latency_ms} ms` : '-';
                  const timeSince = h.last_check_at ? `${Math.max(0, Math.floor((Date.now() / 1000) - h.last_check_at))}s ago` : 'Never';
                  const verText = h.version || '-';

                  return (
                    <tr key={id} className="border-b">
                      <td className="td-cell fw-bold">{id}</td>
                      <td className="td-cell">
                        <code className="font-mono text-xs px-sm py-xs rounded-xs" style={{ background: 'rgba(255, 255, 255, 0.05)' }}>
                          {resolvedIP}
                        </code>
                      </td>
                      <td className="td-cell">
                        <span className="inline-flex items-center gap-xs">
                          <span className={`status-dot ${isOnline ? 'status-dot--online' : 'status-dot--offline'}`}></span>
                          {h.status}
                        </span>
                      </td>
                      <td className="td-cell">{latText}</td>
                      <td className="td-cell">{timeSince}</td>
                      <td className="td-cell"><code className="font-mono text-2xs">{verText}</code></td>
                      <td className="td-cell">
                        {h.error_message && (
                          <span className="text-danger text-xs">{h.error_message}</span>
                        )}
                      </td>
                      <td className="td-cell text-right whitespace-nowrap">
                        <div className="flex gap-xs justify-end items-center">
                          <button className="btn btn-secondary py-xs px-sm text-xs" title="Restart Daemon" onClick={() => restartEdgeDaemon(id)}>Restart</button>
                          <button className="btn btn-secondary py-xs px-sm text-xs" title="Enable Soft Maintenance" onClick={() => enableEdgeMaintenance(id)}>Maintenance</button>
                          <button className="btn btn-danger py-xs px-sm text-xs" title="Kick All Active Tunnels" onClick={() => kickEdgeTunnels(id)}>Kick</button>
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
        </>
      )}
    </div>
  );
}
