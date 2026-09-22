import { useEffect, useState, useRef } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';
import { useUI } from '../contexts/UIContext';

interface Tunnel {
  user_id?: string;
  subdomain_prefix: string;
  full_host: string;
  status: string;
  bytes_in: number;
  bytes_out: number;
  client_ip: string;
  node_id: string;
  visitor_ips: string[];
}

// One client's tunnels, as one group.
//
// A client that maps several ports gets one lease per port -- Registry.Register gives the first
// the bare subdomain and every one after it "<subdomain>-<localPort>" -- so a three-port client
// occupies three rows repeating everything that describes the CLIENT rather than the port
// (#2129).
//
// Keyed on user, prefix and node rather than on a session identifier. Register already sets
// every lease in a session to the shared base prefix, so this needs nothing the payload does not
// already carry -- and the session token, which would have been the obvious key, is a credential
// that must never reach a browser (#2137).
type TunnelGroup = {
  key: string;
  subdomain: string;
  nodeId: string;
  clientIp: string;
  status: string;
  bytesIn: number;
  bytesOut: number;
  visitors: number;
  members: Tunnel[];
};

// No prefix, no grouping. An empty subdomain_prefix is not a value to group ON -- treating it as
// one collapsed every lease sharing a node into a single bogus group, including clients with
// different IPs that have nothing to do with each other.
const groupKeyFor = (t: Tunnel, index: number) =>
  t.subdomain_prefix
    ? `${t.user_id || ''}|${t.subdomain_prefix}|${t.node_id || ''}`
    : `ungrouped:${index}`;

const groupTunnels = (list: Tunnel[]): TunnelGroup[] => {
  const byKey = new Map<string, TunnelGroup>();
  list.forEach((t, index) => {
    const key = groupKeyFor(t, index);
    let g = byKey.get(key);
    if (!g) {
      g = {
        key,
        subdomain: t.subdomain_prefix,
        nodeId: t.node_id,
        clientIp: t.client_ip,
        // One value for the whole session: the client dials every target port and reports
        // "down" if ANY of them fails, so this describes the client, not the port. Shown once
        // on the group rather than repeated onto rows it is not true of.
        status: t.status,
        bytesIn: 0,
        bytesOut: 0,
        visitors: 0,
        members: [],
      };
      byKey.set(key, g);
    }
    g.bytesIn += t.bytes_in || 0;
    g.bytesOut += t.bytes_out || 0;
    g.visitors += t.visitor_ips?.length || 0;
    g.members.push(t);
  });
  // Alphabetical, and members alphabetical within a group. The server now returns leases in a
  // stable order too (#2143), but stable is not the same as MEANINGFUL.
  const groups = Array.from(byKey.values());
  groups.forEach((g) =>
    g.members.sort((a, b) =>
      (a.full_host || '').localeCompare(b.full_host || ''),
    ),
  );
  groups.sort(
    (a, b) =>
      a.subdomain.localeCompare(b.subdomain) ||
      (a.members[0].full_host || '').localeCompare(
        b.members[0].full_host || '',
      ),
  );
  return groups;
};

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
  const [status, setStatus] = useState<
    'connecting' | 'connected' | 'disconnected'
  >('connecting');
  // Which groups are open. Collapsed by default -- except a group that is not up, which is
  // opened below: hiding the row someone needs to see defeats the point of the table.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<any>(null);

  const connect = () => {
    if (wsRef.current) {
      try {
        wsRef.current.close();
      } catch (e) {}
    }
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }

    setStatus('connecting');
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/portal/telemetry/ws`;

    console.log('[Telemetry V2] Connecting to WebSocket:', wsUrl);
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      console.log('[Telemetry V2] WebSocket connected.');
      setStatus('connected');
    };

    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (payload.type === 'telemetry') {
          setTelemetryData(payload.data);
        }
      } catch (e) {
        // load-failure-gate: one malformed WebSocket frame, not a page load. Blanking the screen
        // for a single unparseable message would be a worse failure than dropping it; the next
        // frame replaces the data anyway.
        console.error('[Telemetry V2] Failed to parse message:', e);
      }
    };

    ws.onclose = (event) => {
      console.log('[Telemetry V2] WebSocket closed:', event.reason);
      setStatus('disconnected');
      reconnectTimeoutRef.current = setTimeout(connect, 5000);
    };

    ws.onerror = (err) => {
      console.error('[Telemetry V2] WebSocket error:', err);
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
  const {
    items: sortedTunnels,
    requestSort,
    getSortIndicator,
    searchQuery,
    setSearchQuery,
    getAriaSort,
  } = useTableSort(tunnels, [
    'subdomain_prefix',
    'full_host',
    'status',
    'node_id',
    'client_ip',
  ]);

  const handleKick = async (subdomain: string) => {
    if (
      await showConfirm(
        'Kick Lease',
        `Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`,
      )
    ) {
      try {
        await axios.delete(
          `/api/admin/leases/${encodeURIComponent(subdomain)}`,
        );
        showToast(
          `Kicked tunnel subdomain "${subdomain}" successfully.`,
          'success',
        );
      } catch (err: any) {
        showToast(
          'Failed to kick tunnel: ' +
            (err.response?.data?.error || err.message || 'Unknown error'),
          'error',
        );
      }
    }
  };

  const activeTunnelsCount = tunnels.length;
  const activeNodesCount = new Set(
    tunnels.map((t) => t.node_id).filter(Boolean),
  ).size;
  const totalBytesIn = tunnels.reduce((acc, t) => acc + (t.bytes_in || 0), 0);
  const totalBytesOut = tunnels.reduce((acc, t) => acc + (t.bytes_out || 0), 0);
  const totalBandwidth = totalBytesIn + totalBytesOut;

  return (
    <div id="telemetry-page" className="animate-fade-in">
      <div className="page-header flex-wrap gap-lg">
        <div>
          <h1 className="page-header__title">
            {t('telemetry_title', 'Real-time Telemetry')}
          </h1>
          <p className="page-header__desc">
            {t(
              'telemetry_desc',
              'Monitor active tunnels, bandwidth consumption, and visitor traffic in real-time.',
            )}
          </p>
        </div>
        <div>
          <div className="flex items-center gap-sm px-lg py-xs rounded-full text-xs fw-semibold border surface-subtle">
            <span
              className={`status-dot ${status === 'connected' ? 'status-dot--online' : status === 'connecting' ? 'status-dot--warning' : 'status-dot--offline'}`}
            ></span>
            <span>
              {status === 'connected' &&
                t('telemetry_connected', 'Live Feed Connected')}
              {status === 'connecting' &&
                t('telemetry_connecting', 'Connecting to Gateway...')}
              {status === 'disconnected' &&
                t(
                  'telemetry_disconnected',
                  'Live Feed Disconnected (Retrying...)',
                )}
            </span>
          </div>
        </div>
      </div>

      <div className="auto-grid-md mb-2xl">
        <div id="stat-active-tunnels" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_active_tunnels', 'Active Tunnels')}
          </div>
          <div className="stat-value text-main">{activeTunnelsCount}</div>
        </div>
        <div id="stat-total-bandwidth" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_total_bandwidth', 'Total Live Traffic')}
          </div>
          <div className="stat-value text-main">
            {formatBytes(totalBandwidth)}
          </div>
          <div className="stat-sub text-muted mt-xs">
            📥 {formatBytes(totalBytesIn)} In | 📤 {formatBytes(totalBytesOut)}{' '}
            Out
          </div>
        </div>
        <div id="stat-active-gateways" className="card p-lg">
          <div className="stat-label">
            {t('telemetry_active_nodes', 'Active Gateways')}
          </div>
          <div className="stat-value text-main">{activeNodesCount}</div>
        </div>
      </div>

      <div id="telemetry-tunnels-table" className="card p-xl">
        <div className="page-header flex-wrap gap-md mb-lg">
          <h3 className="m-0 text-base fw-bold">
            {t('telemetry_tunnels_list', 'Real-Time Tunnel Connections')}
          </h3>
          <input
            type="text"
            placeholder={t(
              'search_active_tunnels_placeholder',
              'Search active tunnels...',
            )}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="search-input"
            aria-label={t('search_active_tunnels', 'Search active tunnels')}
          />
        </div>

        {tunnels.length === 0 ? (
          <div className="card text-center p-2xl border-dashed">
            <div className="text-muted text-base">
              {t(
                'telemetry_no_tunnels',
                'No active tunnels monitored on the gateway.',
              )}
            </div>
          </div>
        ) : (
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('subdomain_prefix')}
                    aria-sort={getAriaSort('subdomain_prefix')}
                  >
                    {t('subdomain', 'Subdomain')}
                    {getSortIndicator('subdomain_prefix')}
                  </th>
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('full_host')}
                    aria-sort={getAriaSort('full_host')}
                  >
                    {t('target_host', 'Target Host')}
                    {getSortIndicator('full_host')}
                  </th>
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('node_id')}
                    aria-sort={getAriaSort('node_id')}
                  >
                    {t('node', 'Node')}
                    {getSortIndicator('node_id')}
                  </th>
                  <th className="th-col">{t('client_ip', 'Client IP')}</th>
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('bytes_in')}
                    aria-sort={getAriaSort('bytes_in')}
                  >
                    {t('data_in', 'Data In')}
                    {getSortIndicator('bytes_in')}
                  </th>
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('bytes_out')}
                    aria-sort={getAriaSort('bytes_out')}
                  >
                    {t('data_out', 'Data Out')}
                    {getSortIndicator('bytes_out')}
                  </th>
                  <th className="th-col">{t('visitors', 'Visitors')}</th>
                  <th className="th-col">{t('status', 'Status')}</th>
                  <th className="th-col text-right">
                    {t('actions', 'Actions')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {groupTunnels(sortedTunnels).flatMap((group) => {
                  // A single-port client is not a group. Rendering a parent plus one child
                  // would repeat itself and hand the common case an expander that reveals
                  // nothing, so it stays the plain row it is today.
                  const isGroup = group.members.length > 1;
                  const unhealthy =
                    (group.status || 'up').toLowerCase() !== 'up';
                  const isOpen = expanded.has(group.key) || unhealthy;

                  const toggle = () =>
                    setExpanded((prev) => {
                      const next = new Set(prev);
                      if (next.has(group.key)) next.delete(group.key);
                      else next.add(group.key);
                      return next;
                    });

                  const nodeBadge =
                    group.nodeId && group.nodeId !== 'control' ? (
                      <span className="badge badge-node">
                        🌍 {group.nodeId}
                      </span>
                    ) : (
                      <span className="badge badge-control">
                        🇬🇧 {t('control_node', 'Control')}
                      </span>
                    );

                  if (!isGroup) {
                    const only = group.members[0];
                    return [
                      <tr key={group.key} className="border-b">
                        <td className="td-cell fw-semibold text-sm">
                          {group.subdomain}
                        </td>
                        <td className="td-cell text-sm">
                          <a
                            href={`https://${only.full_host}`}
                            target="_blank"
                            rel="noreferrer"
                            className="text-primary no-underline fw-medium"
                          >
                            {only.full_host}
                          </a>
                        </td>
                        <td className="td-cell">{nodeBadge}</td>
                        <td className="td-cell--mono text-sm">
                          {group.clientIp || '-'}
                        </td>
                        <td className="td-cell text-sm">
                          {formatBytes(group.bytesIn)}
                        </td>
                        <td className="td-cell text-sm">
                          {formatBytes(group.bytesOut)}
                        </td>
                        <td className="td-cell text-sm">
                          <span
                            className={
                              group.visitors > 0
                                ? 'text-primary fw-bold'
                                : 'text-muted fw-bold'
                            }
                          >
                            {group.visitors}
                          </span>
                        </td>
                        <td className="td-cell">
                          <span className="badge badge-success">
                            {(group.status || 'up').toUpperCase()}
                          </span>
                        </td>
                        <td className="td-cell text-right">
                          <button
                            className="btn btn-danger py-xs px-md text-xs w-auto"
                            onClick={() => handleKick(group.subdomain)}
                          >
                            {t('kick', 'Kick')}
                          </button>
                        </td>
                      </tr>,
                    ];
                  }

                  const parent = (
                    <tr key={group.key} className="border-b">
                      <td className="td-cell fw-semibold text-sm">
                        <button
                          type="button"
                          onClick={toggle}
                          aria-expanded={isOpen}
                          className="btn btn-secondary py-xs px-sm text-xs whitespace-nowrap"
                        >
                          {isOpen ? '▾' : '▸'} {group.subdomain}
                        </button>
                        <span className="text-muted text-xs ml-sm">
                          {group.members.length} {t('tunnels', 'tunnels')}
                        </span>
                      </td>
                      <td className="td-cell text-sm text-muted">—</td>
                      <td className="td-cell">{nodeBadge}</td>
                      <td className="td-cell--mono text-sm">
                        {group.clientIp || '-'}
                      </td>
                      <td className="td-cell text-sm fw-semibold">
                        {formatBytes(group.bytesIn)}
                      </td>
                      <td className="td-cell text-sm fw-semibold">
                        {formatBytes(group.bytesOut)}
                      </td>
                      <td className="td-cell text-sm">
                        <span
                          className={
                            group.visitors > 0
                              ? 'text-primary fw-bold'
                              : 'text-muted fw-bold'
                          }
                        >
                          {group.visitors}
                        </span>
                      </td>
                      <td className="td-cell">
                        <span className="badge badge-success">
                          {(group.status || 'up').toUpperCase()}
                        </span>
                      </td>
                      <td className="td-cell text-right">
                        {/* Kick resolves a session token and drops EVERY port of this client,
                            whichever row it is clicked on -- so it belongs here, where its
                            blast radius matches where the button sits (#2129). */}
                        <button
                          className="btn btn-danger py-xs px-md text-xs w-auto"
                          onClick={() => handleKick(group.subdomain)}
                        >
                          {t('kick', 'Kick')}
                        </button>
                      </td>
                    </tr>
                  );

                  if (!isOpen) return [parent];

                  // Node, client IP and status are absent from a child, not repeated in a
                  // lighter shade. Repeating them is the duplication this removes, and status
                  // in particular would be reprinted onto rows it is not true of.
                  const children = group.members.map((m, i) => (
                    <tr
                      key={`${group.key}|${m.full_host}|${i}`}
                      className="border-b"
                    >
                      <td className="td-cell" />
                      <td className="td-cell text-sm pl-lg">
                        <a
                          href={`https://${m.full_host}`}
                          target="_blank"
                          rel="noreferrer"
                          className="text-primary no-underline fw-medium"
                        >
                          {m.full_host}
                        </a>
                      </td>
                      <td className="td-cell" />
                      <td className="td-cell" />
                      <td className="td-cell text-sm">
                        {formatBytes(m.bytes_in || 0)}
                      </td>
                      <td className="td-cell text-sm">
                        {formatBytes(m.bytes_out || 0)}
                      </td>
                      <td className="td-cell text-sm">
                        <span
                          className={
                            m.visitor_ips?.length > 0
                              ? 'text-primary fw-bold'
                              : 'text-muted fw-bold'
                          }
                        >
                          {m.visitor_ips?.length || 0}
                        </span>
                      </td>
                      <td className="td-cell" />
                      <td className="td-cell" />
                    </tr>
                  ));

                  return [parent, ...children];
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
