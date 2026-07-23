import { useEffect, useState, useRef } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';
import { useUI } from '../contexts/UIContext';

interface Tunnel {
  subdomain_prefix: string;
  full_host: string;
  status: string;
  bytes_in: number;
  bytes_out: number;
  client_ip: string;
  node_id: string;
  visitor_ips: string[];
}

const formatBytes = (bytes: number, decimals = 2) => {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
};

export default function AdminTelemetry() {
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();
  const [telemetryData, setTelemetryData] = useState<any>(null);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<any>(null);

  const connect = () => {
    if (wsRef.current) {
      try { wsRef.current.close(); } catch(e) {}
    }
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }

    setStatus('connecting');
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/portal/telemetry/ws`;

    console.log("[Telemetry V2] Connecting to WebSocket:", wsUrl);
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      console.log("[Telemetry V2] WebSocket connected.");
      setStatus('connected');
    };

    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (payload.type === 'telemetry') {
          setTelemetryData(payload.data);
        }
      } catch (e) {
        console.error("[Telemetry V2] Failed to parse message:", e);
      }
    };

    ws.onclose = (event) => {
      console.log("[Telemetry V2] WebSocket closed:", event.reason);
      setStatus('disconnected');
      reconnectTimeoutRef.current = setTimeout(connect, 5000);
    };

    ws.onerror = (err) => {
      console.error("[Telemetry V2] WebSocket error:", err);
      ws.close();
    };
  };

  useEffect(() => {
    connect();
    return () => {
      if (wsRef.current) {
        wsRef.current.close();
      }
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
    };
  }, []);

  const tunnels: Tunnel[] = telemetryData?.tunnels || [];
  const { items: sortedTunnels, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(tunnels, ['subdomain_prefix', 'full_host', 'status', 'node_id', 'client_ip']);

  const handleKick = async (subdomain: string) => {
    if (await showConfirm('Kick Lease', `Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`)) {
      try {
        await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
        showToast(`Kicked tunnel subdomain "${subdomain}" successfully.`, 'success');
      } catch (err: any) {
        showToast('Failed to kick tunnel: ' + (err.response?.data?.error || err.message || 'Unknown error'), 'error');
      }
    }
  };

  const activeTunnelsCount = tunnels.length;
  const activeNodesCount = new Set(tunnels.map(t => t.node_id).filter(Boolean)).size;
  const totalBytesIn = tunnels.reduce((acc, t) => acc + (t.bytes_in || 0), 0);
  const totalBytesOut = tunnels.reduce((acc, t) => acc + (t.bytes_out || 0), 0);
  const totalBandwidth = totalBytesIn + totalBytesOut;

  return (
    <div id="telemetry-page" style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <div className="page-header flex-wrap gap-lg">
        <div>
          <h2 className="page-header__title">{t('telemetry_title', 'Real-time Telemetry')}</h2>
          <p className="page-header__desc">
            {t('telemetry_desc', 'Monitor active tunnels, bandwidth consumption, and visitor traffic in real-time.')}
          </p>
        </div>
        <div>
          <div className="flex items-center gap-sm px-lg py-xs rounded-full text-xs fw-semibold border" style={{ background: 'rgba(255,255,255,0.05)' }}>
            <span className={`status-dot ${status === 'connected' ? 'status-dot--online' : status === 'connecting' ? 'status-dot--warning' : 'status-dot--offline'}`}></span>
            <span>
              {status === 'connected' && t('telemetry_connected', 'Live Feed Connected')}
              {status === 'connecting' && t('telemetry_connecting', 'Connecting to Gateway...')}
              {status === 'disconnected' && t('telemetry_disconnected', 'Live Feed Disconnected (Retrying...)')}
            </span>
          </div>
        </div>
      </div>

      <div className="auto-grid-md mb-2xl">
        <div id="stat-active-tunnels" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_active_tunnels', 'Active Tunnels')}
          </div>
          <div className="stat-value text-main">
            {activeTunnelsCount}
          </div>
        </div>
        <div id="stat-total-bandwidth" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_total_bandwidth', 'Total Live Traffic')}
          </div>
          <div className="stat-value text-main">
            {formatBytes(totalBandwidth)}
          </div>
          <div className="stat-subtext text-muted mt-xs">
            📥 {formatBytes(totalBytesIn)} In | 📤 {formatBytes(totalBytesOut)} Out
          </div>
        </div>
        <div id="stat-active-gateways" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_active_nodes', 'Active Gateways')}
          </div>
          <div className="stat-value text-main">
            {activeNodesCount}
          </div>
        </div>
      </div>

      <div id="telemetry-tunnels-table" className="card p-xl">
        <div className="page-header flex-wrap gap-md mb-lg">
          <h3 className="m-0 text-base fw-bold">{t('telemetry_tunnels_list', 'Real-Time Tunnel Connections')}</h3>
          <input 
            type="text" 
            placeholder={t('search_active_tunnels_placeholder', 'Search active tunnels...')}
            value={searchQuery} 
            onChange={e => setSearchQuery(e.target.value)}
            className="search-input"
          />
        </div>

        {tunnels.length === 0 ? (
          <div className="card text-center p-2xl border-dashed">
            <div className="text-muted text-base">
              {t('telemetry_no_tunnels', 'No active tunnels monitored on the gateway.')}
            </div>
          </div>
        ) : (
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col th-col--sortable" onClick={() => requestSort('subdomain_prefix')} aria-sort={getAriaSort('subdomain_prefix')}>{t('subdomain', 'Subdomain')}{getSortIndicator('subdomain_prefix')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>{t('target_host', 'Target Host')}{getSortIndicator('full_host')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('node_id')} aria-sort={getAriaSort('node_id')}>{t('node', 'Node')}{getSortIndicator('node_id')}</th>
                  <th className="th-col">{t('client_ip', 'Client IP')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_in')} aria-sort={getAriaSort('bytes_in')}>{t('data_in', 'Data In')}{getSortIndicator('bytes_in')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('bytes_out')} aria-sort={getAriaSort('bytes_out')}>{t('data_out', 'Data Out')}{getSortIndicator('bytes_out')}</th>
                  <th className="th-col">{t('visitors', 'Visitors')}</th>
                  <th className="th-col">{t('status', 'Status')}</th>
                  <th className="th-col text-right">{t('actions', 'Actions')}</th>
                </tr>
              </thead>
              <tbody>
                {sortedTunnels.map((tItem, idx) => (
                  <tr key={idx} className="border-b">
                    <td className="td-cell fw-semibold text-sm">{tItem.subdomain_prefix}</td>
                    <td className="td-cell text-sm">
                      <a href={`https://${tItem.full_host}`} target="_blank" rel="noreferrer" className="text-primary no-underline fw-medium">
                        {tItem.full_host}
                      </a>
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
                    <td className="td-cell--mono text-sm">{tItem.client_ip || '-'}</td>
                    <td className="td-cell text-sm">{formatBytes(tItem.bytes_in || 0)}</td>
                    <td className="td-cell text-sm">{formatBytes(tItem.bytes_out || 0)}</td>
                    <td className="td-cell text-sm">
                      <span className={tItem.visitor_ips?.length > 0 ? 'text-primary fw-bold' : 'text-muted fw-bold'}>
                        {tItem.visitor_ips?.length || 0}
                      </span>
                    </td>
                    <td className="td-cell">
                      <span className="badge badge-success">
                        {tItem.status ? tItem.status.toUpperCase() : 'UP'}
                      </span>
                    </td>
                    <td className="td-cell text-right">
                      <button 
                        className="btn btn-danger py-xs px-md text-xs w-auto" 
                        onClick={() => handleKick(tItem.subdomain_prefix)}
                      >
                        {t('kick', 'Kick')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
