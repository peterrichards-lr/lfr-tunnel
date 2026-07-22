import { useEffect, useState } from 'react';
import axios from 'axios';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

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
  
  // Track open menus
  const [openMenu, setOpenMenu] = useState<string | null>(null);

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
  }, []);

  const triggerEdgeAction = async (nodeId: string, action: string, reason = '', duration = 0) => {
    try {
      await axios.post('/api/portal/edge-action', {
        node_id: nodeId,
        action,
        reason,
        duration: parseInt(duration.toString(), 10) || 0
      });
      alert('Action executed successfully.');
      fetchHealth();
    } catch (e: any) {
      alert(`Error: ${e.response?.data?.error || 'Action failed.'}`);
    }
    setOpenMenu(null);
  };

  const restartEdgeDaemon = (nodeId: string) => {
    if (confirm(`Are you sure you want to restart the edge daemon for ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "restart");
    }
  };

  const enableEdgeMaintenance = (nodeId: string) => {
    const reason = prompt(`Enter a reason for enabling soft maintenance on ${nodeId}:`, "Edge Server Maintenance");
    if (reason === null) return;
    const durationStr = prompt(`Enter duration in minutes for maintenance on ${nodeId}:`, "30");
    if (durationStr === null) return;
    const duration = parseInt(durationStr, 10);
    if (isNaN(duration) || duration <= 0) {
      alert("Invalid duration.");
      return;
    }
    triggerEdgeAction(nodeId, "maintenance_enable", reason, duration);
  };

  const disableEdgeMaintenance = (nodeId: string) => {
    if (confirm(`Are you sure you want to disable maintenance on ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "maintenance_disable");
    }
  };

  const kickEdgeTunnels = (nodeId: string) => {
    if (confirm(`Are you sure you want to kick ALL active tunnels on edge node ${nodeId}?`)) {
      triggerEdgeAction(nodeId, "kick_tunnels");
    }
  };

  const toggleMenu = (e: React.MouseEvent, id: string) => {
    e.stopPropagation();
    setOpenMenu(openMenu === id ? null : id);
  };

  const nodeArray = Object.keys(nodes).map(id => ({ id, ...nodes[id] }));
  const { items: sortedNodes, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(nodeArray, ['id', 'status', 'resolved_ip', 'version']);

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ marginBottom: '24px' }}>
          <Skeleton width={180} height={28} />
          <Skeleton width={320} height={16} style={{ marginTop: '8px' }} />
        </div>

        <div className="card" style={{ padding: '24px', marginBottom: '24px' }}>
          <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card" style={{ padding: '24px' }}>
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={100} /></th>
                  <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width={50} height={20} borderRadius={10} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="80%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="50%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: '16px' }}><Skeleton width={40} height={24} /></td>
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
      <div style={{ marginBottom: '24px' }}>
        <h3>Network Health</h3>
        <p style={{ color: 'var(--text-muted)' }}>Monitor the real-time status, latency, and uptime of regional stateless edge nodes.</p>
      </div>

      {!outboundOk && (
        <div className="glass" style={{
          padding: '16px', background: 'rgba(239, 68, 68, 0.1)', borderLeft: '4px solid var(--danger)',
          color: 'var(--danger)', borderRadius: '8px', marginBottom: '24px', fontSize: '13px',
          fontWeight: 500, display: 'flex', alignItems: 'center', gap: '8px'
        }}>
          ⚠️ <strong>Gateway Network Error:</strong> The central gateway has lost outbound internet connectivity. Regional health checks are suspended.
        </div>
      )}

      {error ? (
        <div style={{ color: 'var(--danger)', marginBottom: '20px' }}>
          {error}
        </div>
      ) : (
        <>
          {nodeArray.length > 0 && (
            <div style={{ marginBottom: '16px' }}>
              <input 
                type="text" 
                placeholder="Search nodes..." 
                value={searchQuery} 
                onChange={e => setSearchQuery(e.target.value)}
                style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
              />
            </div>
          )}
          <div className="card table-responsive">
            <table className="table" style={{ overflow: 'visible' }}>
              <thead>
                <tr>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('id')} aria-sort={getAriaSort('id')}>Node ID{getSortIndicator('id')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('resolved_ip')} aria-sort={getAriaSort('resolved_ip')}>Resolved IP{getSortIndicator('resolved_ip')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>Status{getSortIndicator('status')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('latency_ms')} aria-sort={getAriaSort('latency_ms')}>Latency{getSortIndicator('latency_ms')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('last_check_at')} aria-sort={getAriaSort('last_check_at')}>Last Check{getSortIndicator('last_check_at')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => requestSort('version')} aria-sort={getAriaSort('version')}>Version{getSortIndicator('version')}</th>
                  <th>Error</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sortedNodes.length === 0 ? (
                <tr>
                  <td colSpan={8} style={{ textAlign: 'center', opacity: 0.6, padding: '16px' }}>
                    No edge nodes configured
                  </td>
                </tr>
              ) : (
                sortedNodes.map(h => {
                  const id = h.id;
                  const isOnline = h.status === 'Online';
                  const dotColor = isOnline ? '#10b981' : 'var(--danger)';
                  
                  const resolvedIP = h.resolved_ip || '-';
                  const latText = isOnline ? `${h.latency_ms} ms` : '-';
                  const timeSince = h.last_check_at ? `${Math.max(0, Math.floor((Date.now() / 1000) - h.last_check_at))}s ago` : 'Never';
                  const verText = h.version || '-';

                  return (
                    <tr key={id}>
                      <td><strong>{id}</strong></td>
                      <td>
                        <code style={{ fontFamily: 'monospace', fontSize: '12px', background: 'rgba(255, 255, 255, 0.05)', padding: '2px 6px', borderRadius: '4px' }}>
                          {resolvedIP}
                        </code>
                      </td>
                      <td>
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
                          <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: dotColor }}></span>
                          {h.status}
                        </span>
                      </td>
                      <td>{latText}</td>
                      <td>{timeSince}</td>
                      <td><code style={{ fontFamily: 'monospace', fontSize: '11px' }}>{verText}</code></td>
                      <td>
                        {h.error_message && (
                          <span style={{ color: 'var(--danger)', fontSize: '12px' }}>{h.error_message}</span>
                        )}
                      </td>
                      <td style={{ textAlign: 'right', position: 'relative' }}>
                        <div className="action-menu" style={{ display: 'inline-block' }}>
                          <button 
                            className="btn btn-secondary" 
                            style={{ padding: '2px 8px' }}
                            onClick={(e) => toggleMenu(e, id)}
                          >
                            ⋮
                          </button>
                          {openMenu === id && (
                            <div className="action-menu-dropdown" style={{
                              position: 'absolute', right: 0, top: '100%', zIndex: 100, 
                              background: 'var(--card-bg)', border: '1px solid var(--border)',
                              borderRadius: '4px', boxShadow: '0 4px 12px rgba(0,0,0,0.2)',
                              minWidth: '180px', display: 'flex', flexDirection: 'column'
                            }}>
                              <button className="action-menu-item" style={{ padding: '8px 12px', textAlign: 'left', background: 'none', border: 'none', color: 'var(--text-main)', cursor: 'pointer', borderBottom: '1px solid var(--border)' }} onClick={() => restartEdgeDaemon(id)}>Restart Daemon</button>
                              <button className="action-menu-item" style={{ padding: '8px 12px', textAlign: 'left', background: 'none', border: 'none', color: 'var(--text-main)', cursor: 'pointer', borderBottom: '1px solid var(--border)' }} onClick={() => enableEdgeMaintenance(id)}>Enable Maintenance</button>
                              <button className="action-menu-item" style={{ padding: '8px 12px', textAlign: 'left', background: 'none', border: 'none', color: 'var(--text-main)', cursor: 'pointer', borderBottom: '1px solid var(--border)' }} onClick={() => disableEdgeMaintenance(id)}>Disable Maintenance</button>
                              <button className="action-menu-item" style={{ padding: '8px 12px', textAlign: 'left', background: 'none', border: 'none', color: 'var(--danger)', cursor: 'pointer' }} onClick={() => kickEdgeTunnels(id)}>Kick All Tunnels</button>
                            </div>
                          )}
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
