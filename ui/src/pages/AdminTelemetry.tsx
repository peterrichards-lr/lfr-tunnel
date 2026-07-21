import { useEffect, useState, useRef } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';

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
  const { items: sortedTunnels, requestSort, getSortIndicator, searchQuery, setSearchQuery } = useTableSort(tunnels, ['subdomain_prefix', 'full_host', 'status', 'node_id', 'client_ip']);

  const handleKick = async (subdomain: string) => {
    if (window.confirm(`Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`)) {
      try {
        await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
        alert(`Kicked tunnel subdomain "${subdomain}" successfully.`);
      } catch (err: any) {
        alert('Failed to kick tunnel: ' + (err.response?.data?.error || err.message || 'Unknown error'));
      }
    }
  };

  const activeTunnelsCount = tunnels.length;
  const activeNodesCount = new Set(tunnels.map(t => t.node_id).filter(Boolean)).size;
  const totalBytesIn = tunnels.reduce((acc, t) => acc + (t.bytes_in || 0), 0);
  const totalBytesOut = tunnels.reduce((acc, t) => acc + (t.bytes_out || 0), 0);
  const totalBandwidth = totalBytesIn + totalBytesOut;

  return (
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px', flexWrap: 'wrap', gap: '16px' }}>
        <div>
          <h2 style={{ fontSize: '24px', fontWeight: 'bold', margin: 0 }}>{t('telemetry_title', 'Real-time Telemetry')}</h2>
          <p style={{ color: 'var(--text-muted)', fontSize: '14px', marginTop: '4px' }}>
            {t('telemetry_desc', 'Monitor active tunnels, bandwidth consumption, and visitor traffic in real-time.')}
          </p>
        </div>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '6px 16px', borderRadius: '20px', fontSize: '13px', fontWeight: 600, background: 'rgba(255,255,255,0.05)', border: '1px solid var(--border)' }}>
            <span style={{ 
              width: '8px', 
              height: '8px', 
              borderRadius: '50%', 
              backgroundColor: status === 'connected' ? '#10b981' : status === 'connecting' ? '#f59e0b' : '#ef4444',
              display: 'inline-block',
              boxShadow: status === 'connected' ? '0 0 8px #10b981' : 'none'
            }}></span>
            <span>
              {status === 'connected' && t('telemetry_connected', 'Live Feed Connected')}
              {status === 'connecting' && t('telemetry_connecting', 'Connecting to Gateway...')}
              {status === 'disconnected' && t('telemetry_disconnected', 'Live Feed Disconnected (Retrying...)')}
            </span>
          </div>
        </div>
      </div>

      <div className="responsive-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))', gap: '24px', marginBottom: '24px' }}>
        <div className="card" style={{ padding: '20px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '13px', fontWeight: 600, textTransform: 'uppercase', marginBottom: '8px' }}>
            {t('telemetry_active_tunnels', 'Active Tunnels')}
          </div>
          <div style={{ fontSize: '28px', fontWeight: '800', color: 'var(--text-color)' }}>
            {activeTunnelsCount}
          </div>
        </div>
        <div className="card" style={{ padding: '20px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '13px', fontWeight: 600, textTransform: 'uppercase', marginBottom: '8px' }}>
            {t('telemetry_total_bandwidth', 'Total Live Traffic')}
          </div>
          <div style={{ fontSize: '28px', fontWeight: '800', color: 'var(--text-color)' }}>
            {formatBytes(totalBandwidth)}
          </div>
          <div style={{ color: 'var(--text-muted)', fontSize: '11px', marginTop: '4px' }}>
            📥 {formatBytes(totalBytesIn)} In | 📤 {formatBytes(totalBytesOut)} Out
          </div>
        </div>
        <div className="card" style={{ padding: '20px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '13px', fontWeight: 600, textTransform: 'uppercase', marginBottom: '8px' }}>
            {t('telemetry_active_nodes', 'Active Gateways')}
          </div>
          <div style={{ fontSize: '28px', fontWeight: '800', color: 'var(--text-color)' }}>
            {activeNodesCount}
          </div>
        </div>
      </div>

      <div className="card" style={{ padding: '24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px', flexWrap: 'wrap', gap: '12px' }}>
          <h3 style={{ margin: 0, fontSize: '18px', fontWeight: 700 }}>{t('telemetry_tunnels_list', 'Real-Time Tunnel Connections')}</h3>
          <input 
            type="text" 
            placeholder={t('search_active_tunnels_placeholder', 'Search active tunnels...')}
            value={searchQuery} 
            onChange={e => setSearchQuery(e.target.value)}
            style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
          />
        </div>

        {tunnels.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
            <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>
              {t('telemetry_no_tunnels', 'No active tunnels monitored on the gateway.')}
            </div>
          </div>
        ) : (
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('subdomain_prefix')}>{t('subdomain', 'Subdomain')}{getSortIndicator('subdomain_prefix')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('full_host')}>{t('target_host', 'Target Host')}{getSortIndicator('full_host')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('node_id')}>{t('node', 'Node')}{getSortIndicator('node_id')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('client_ip', 'Client IP')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('bytes_in')}>{t('data_in', 'Data In')}{getSortIndicator('bytes_in')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('bytes_out')}>{t('data_out', 'Data Out')}{getSortIndicator('bytes_out')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('visitors', 'Visitors')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('status', 'Status')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', textAlign: 'right' }}>{t('actions', 'Actions')}</th>
                </tr>
              </thead>
              <tbody>
                {sortedTunnels.map((tItem, idx) => (
                  <tr key={idx} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s' }} onMouseOver={e => e.currentTarget.style.background = 'rgba(255,255,255,0.03)'} onMouseOut={e => e.currentTarget.style.background = 'transparent'}>
                    <td style={{ padding: '16px', fontWeight: 600, fontSize: '14px' }}>{tItem.subdomain_prefix}</td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>
                      <a href={`https://${tItem.full_host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none', fontWeight: 500 }}>
                        {tItem.full_host}
                      </a>
                    </td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>
                      {tItem.node_id && tItem.node_id !== 'control' ? (
                        <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)' }}>
                          🌍 {tItem.node_id}
                        </span>
                      ) : (
                        <span style={{ padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)' }}>
                          🇬🇧 {t('control_node', 'Control')}
                        </span>
                      )}
                    </td>
                    <td style={{ padding: '16px', fontSize: '14px', fontFamily: 'monospace' }}>{tItem.client_ip || '-'}</td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>{formatBytes(tItem.bytes_in || 0)}</td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>{formatBytes(tItem.bytes_out || 0)}</td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>
                      <span style={{ fontWeight: 'bold', color: tItem.visitor_ips?.length > 0 ? 'var(--primary)' : 'var(--text-muted)' }}>
                        {tItem.visitor_ips?.length || 0}
                      </span>
                    </td>
                    <td style={{ padding: '16px' }}>
                      <span style={{ 
                        padding: '4px 12px', borderRadius: '20px', fontSize: '12px', fontWeight: 600, 
                        background: 'rgba(16, 185, 129, 0.15)', color: '#34d399', border: '1px solid rgba(16, 185, 129, 0.3)' 
                      }}>
                        {tItem.status ? tItem.status.toUpperCase() : 'UP'}
                      </span>
                    </td>
                    <td style={{ padding: '16px', textAlign: 'right' }}>
                      <button 
                        className="btn btn-secondary" 
                        style={{ padding: '6px 12px', fontSize: '13px', color: 'var(--danger)', borderColor: 'rgba(239, 68, 68, 0.2)', background: 'transparent' }}
                        onClick={() => handleKick(tItem.subdomain_prefix)}
                        onMouseOver={e => { e.currentTarget.style.background = 'rgba(239, 68, 68, 0.1)'; e.currentTarget.style.color = '#ef4444'; }}
                        onMouseOut={e => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = 'var(--danger)'; }}
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
