import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Title,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js';
import { Line, Doughnut, Bar, Pie } from 'react-chartjs-2';

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Title,
  Tooltip,
  Legend,
  Filler,
);

const formatBytes = (bytes: number, decimals = 2) => {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
};

// Palette for per-gateway series. Fixed order rather than random, so a given gateway keeps
// the same colour between renders and between the two portals.
const NODE_COLOURS = [
  '#3b82f6',
  '#10b981',
  '#f59e0b',
  '#ef4444',
  '#8b5cf6',
  '#ec4899',
  '#14b8a6',
];

// Turns the flat [{date, node_id, sessions}] list into one dataset per gateway.
//
// A gateway that carried nothing on a day has no row for it, so a missing entry is
// filled with 0 rather than skipped. That gap IS the signal this chart exists for -- an
// edge dropping to zero is what a power window or a dead control channel looks like from
// outside (#1150). Left as a hole, Chart.js would join the surrounding points and draw a
// line straight over the outage.
function nodeSeries(
  nodeDaily: { date: string; node_id: string; sessions: number }[],
) {
  const dates = Array.from(new Set(nodeDaily.map((d) => d.date))).sort();
  const nodes = Array.from(new Set(nodeDaily.map((d) => d.node_id))).sort();
  const lookup = new Map(
    nodeDaily.map((d) => [`${d.date}|${d.node_id}`, d.sessions]),
  );
  return {
    labels: dates,
    datasets: nodes.map((node, i) => ({
      label: node.toUpperCase(),
      data: dates.map((date) => lookup.get(`${date}|${node}`) ?? 0),
      borderColor: NODE_COLOURS[i % NODE_COLOURS.length],
      backgroundColor: NODE_COLOURS[i % NODE_COLOURS.length] + '20',
      tension: 0.3,
      fill: false,
    })),
  };
}

export default function AdminAnalytics() {
  const { t } = useI18n();
  const { theme } = useSettings();

  const [data, setData] = useState<any>(null);
  const [clientStats, setClientStats] = useState<any[]>([]);
  // Where the next edge should go (#1151). The data path shipped without a reader; this is
  // the panel the epic (#1149) was left open for.
  const [regionLatency, setRegionLatency] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [timeRange, setTimeRange] = useState('30'); // Default to 30 days

  const {
    items: sortedClientStats,
    requestSort,
    getSortIndicator,
    getAriaSort,
  } = useTableSort(clientStats, ['version', 'os', 'count']);

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      try {
        const query = timeRange !== '0' ? `?days=${timeRange}` : '';

        // /api/analytics answers every authenticated user: `personal` always, `global` only
        // for an admin. So its response is what decides whether the admin-only endpoints are
        // worth calling -- asked of the SERVER rather than guessed from a client-side role,
        // which cannot disagree with it (#1512).
        //
        // Fetched in sequence for that reason. It costs an admin one extra round trip and
        // saves every non-admin two guaranteed 403s on every page load and every time-range
        // change -- traffic that would otherwise show up in the audit trail and count toward
        // the rate limiter for people doing nothing wrong.
        const analyticsRes = await axios.get(`/api/analytics${query}`);
        setData(analyticsRes.data);

        if (!analyticsRes.data?.global) {
          setClientStats([]);
          setRegionLatency(null);
          return;
        }

        const [clientsRes, latencyRes] = await Promise.all([
          axios.get('/api/admin/analytics/clients').catch(() => ({ data: [] })),
          // Its own days param rather than the shared one: the endpoint rejects 0, which
          // is what the "All time" option sends.
          axios
            .get(
              `/api/admin/analytics/region-latency?days=${timeRange === '0' ? 365 : timeRange}`,
            )
            .catch(() => ({ data: null })),
        ]);
        setClientStats(clientsRes.data || []);
        setRegionLatency(latencyRes.data);
      } catch (err) {
        console.error('Failed to load analytics', err);
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [timeRange]);

  const isLight = theme === 'light';
  // Resolved here rather than passed to Chart.js as var(--...): a chart draws to canvas,
  // which has no CSS cascade, so a custom property reaches it as an unparseable string and
  // Chart.js falls back to its default near-black. Three legends did that and were unreadable
  // on the dark and liferay themes -- and --text-color was not a defined token either way.
  const textColor = isLight ? '#475569' : '#94a3b8';
  const gridColor = isLight ? '#e2e8f0' : '#334155';

  const chartOptions = () => ({
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        position: 'top' as const,
        labels: {
          color: textColor,
          font: { family: 'Inter, system-ui, sans-serif' },
        },
      },
      tooltip: {
        callbacks: {
          label: (context: any) =>
            `${context.dataset.label}: ${formatBytes(context.raw)}`,
        },
      },
    },
    scales: {
      x: {
        grid: { color: gridColor },
        ticks: {
          color: textColor,
          font: { family: 'Inter, system-ui, sans-serif' },
        },
      },
      y: {
        grid: { color: gridColor },
        ticks: {
          color: textColor,
          font: { family: 'Inter, system-ui, sans-serif' },
          callback: (value: any) => formatBytes(value),
        },
      },
    },
  });

  const doughnutOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        position: 'right' as const,
        labels: {
          color: textColor,
          font: { family: 'Inter, system-ui, sans-serif' },
        },
      },
      tooltip: {
        callbacks: {
          label: (context: any) => formatBytes(context.raw),
        },
      },
    },
  };

  const handlePrint = () => {
    window.print();
  };

  if (loading) {
    return (
      <div className="card text-center p-2xl">
        <p>{t('loading_analytics', 'Loading analytics...')}</p>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="card text-center p-2xl">
        <p>{t('error_loading_analytics', 'Failed to load analytics data.')}</p>
      </div>
    );
  }

  return (
    <div className="analytics-page">
      <div className="page-header no-print">
        <h1 className="page-header__title">
          {/* "System Analytics" describes a page a non-admin is not being shown: they get
              their own usage and nothing else. Keyed off data.global, the same signal the
              admin sections use, so the title cannot say one thing while the body shows
              another (#1512). */}
          {data.global
            ? t('system_analytics', 'System Analytics')
            : t('my_usage', 'My Usage')}
        </h1>
        <div className="flex gap-md">
          <select
            className="input-field w-auto px-md"
            value={timeRange}
            onChange={(e) => setTimeRange(e.target.value)}
            aria-label={t('time_range', 'Time range')}
          >
            <option value="7">Last 7 Days</option>
            <option value="14">Last 14 Days</option>
            <option value="30">Last 30 Days</option>
            <option value="0">All Time</option>
          </select>
          <button
            className="btn btn-secondary w-auto inline-flex items-center gap-sm"
            onClick={handlePrint}
          >
            📄 {t('export_pdf', 'Export PDF')}
          </button>
        </div>
      </div>

      {data.personal && (
        <div className="print-section">
          <h3 className="text-lg fw-bold mb-lg">
            {t('personal_usage', 'Personal Usage')}
          </h3>
          <div className="auto-grid-lg mb-2xl">
            {data.personal.daily && data.personal.daily.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">
                  {t('bandwidth_over_time', 'Bandwidth Over Time')}
                </h4>
                <div className="chart-container">
                  <Line
                    data={{
                      labels: data.personal.daily.map((d: any) => d.date),
                      datasets: [
                        {
                          label: 'Data In',
                          data: data.personal.daily.map((d: any) => d.bytes_in),
                          borderColor: '#3b82f6',
                          backgroundColor: '#3b82f620',
                          fill: true,
                          tension: 0.4,
                        },
                        {
                          label: 'Data Out',
                          data: data.personal.daily.map(
                            (d: any) => d.bytes_out,
                          ),
                          borderColor: '#10b981',
                          backgroundColor: '#10b98120',
                          fill: true,
                          tension: 0.4,
                        },
                      ],
                    }}
                    options={chartOptions()}
                  />
                </div>
              </div>
            )}

            {data.personal.tunnels && data.personal.tunnels.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">
                  {t('bandwidth_by_tunnel', 'Bandwidth by Tunnel')}
                </h4>
                <div className="chart-container">
                  <Doughnut
                    data={{
                      labels: data.personal.tunnels.map(
                        (t: any) => t.full_host,
                      ),
                      datasets: [
                        {
                          label: 'Total Bandwidth',
                          data: data.personal.tunnels.map(
                            (t: any) => t.bytes_in + t.bytes_out,
                          ),
                          backgroundColor: [
                            '#3b82f6',
                            '#10b981',
                            '#f59e0b',
                            '#ef4444',
                            '#8b5cf6',
                            '#ec4899',
                          ],
                          borderWidth: 0,
                        },
                      ],
                    }}
                    options={doughnutOptions}
                  />
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {data.global && (
        <div className="print-section">
          <h3 className="text-lg fw-bold mb-lg">
            {t('global_statistics', 'Global Statistics')}
          </h3>
          <div className="auto-grid-lg mb-2xl">
            {data.global.daily && data.global.daily.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">
                  {t('global_bandwidth', 'Global Bandwidth')}
                </h4>
                <div className="chart-container">
                  <Line
                    data={{
                      labels: data.global.daily.map((d: any) => d.date),
                      datasets: [
                        {
                          label: 'Total Data In',
                          data: data.global.daily.map((d: any) => d.bytes_in),
                          borderColor: '#6366f1',
                          backgroundColor: '#6366f120',
                          fill: true,
                          tension: 0.4,
                        },
                        {
                          label: 'Total Data Out',
                          data: data.global.daily.map((d: any) => d.bytes_out),
                          borderColor: '#f43f5e',
                          backgroundColor: '#f43f5e20',
                          fill: true,
                          tension: 0.4,
                        },
                      ],
                    }}
                    options={chartOptions()}
                  />
                </div>
              </div>
            )}

            {data.global.top_users && data.global.top_users.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">
                  {t('top_users_bandwidth', 'Top Users by Bandwidth')}
                </h4>
                <div className="chart-container">
                  <Bar
                    data={{
                      labels: data.global.top_users.map(
                        (u: any) => (u.email || 'Anonymous').split('@')[0],
                      ),
                      datasets: [
                        {
                          label: 'Total Bandwidth',
                          data: data.global.top_users.map(
                            (u: any) => u.bytes_in + u.bytes_out,
                          ),
                          backgroundColor: '#8b5cf6',
                          borderRadius: 4,
                        },
                      ],
                    }}
                    options={chartOptions()}
                  />
                </div>
              </div>
            )}

            {data.global.top_tunnels && data.global.top_tunnels.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">
                  {t('top_tunnels_bandwidth', 'Top Tunnels by Bandwidth')}
                </h4>
                <div className="chart-container">
                  <Doughnut
                    data={{
                      labels: data.global.top_tunnels.map(
                        (tItem: any) => tItem.full_host,
                      ),
                      datasets: [
                        {
                          label: 'Total Bandwidth',
                          data: data.global.top_tunnels.map(
                            (tItem: any) => tItem.bytes_in + tItem.bytes_out,
                          ),
                          backgroundColor: [
                            '#3b82f6',
                            '#10b981',
                            '#f59e0b',
                            '#ef4444',
                            '#8b5cf6',
                            '#ec4899',
                            '#f43f5e',
                            '#14b8a6',
                            '#6366f1',
                            '#a855f7',
                          ],
                          borderWidth: 0,
                        },
                      ],
                    }}
                    options={doughnutOptions}
                  />
                </div>
              </div>
            )}

            {data.global.portal_stats &&
              data.global.portal_stats.length > 0 && (
                <div className="card p-xl">
                  <h4 className="text-muted text-base mb-lg">Portal Usage</h4>
                  <div className="chart-container">
                    <Doughnut
                      data={{
                        labels: data.global.portal_stats.map((s: any) =>
                          s.version.toUpperCase(),
                        ),
                        datasets: [
                          {
                            data: data.global.portal_stats.map(
                              (s: any) => s.count,
                            ),
                            backgroundColor: [
                              '#0b5fff',
                              '#10b981',
                              '#f59e0b',
                              '#8b5cf6',
                            ],
                            borderWidth: 0,
                          },
                        ],
                      }}
                      options={{
                        responsive: true,
                        maintainAspectRatio: false,
                        plugins: {
                          legend: {
                            position: 'bottom',
                            labels: { color: textColor },
                          },
                        },
                        cutout: '70%',
                      }}
                    />
                  </div>
                </div>
              )}

            {data.global.node_distribution &&
              Object.keys(data.global.node_distribution).length > 0 && (
                <div className="card p-xl">
                  <h4 className="text-muted text-base mb-lg">
                    Tunnel Distribution (Active Nodes)
                  </h4>
                  <div className="chart-container">
                    <Pie
                      data={{
                        labels: Object.keys(data.global.node_distribution).map(
                          (k) => k.toUpperCase(),
                        ),
                        datasets: [
                          {
                            data: Object.values(data.global.node_distribution),
                            backgroundColor: [
                              '#3b82f6',
                              '#10b981',
                              '#f59e0b',
                              '#ef4444',
                              '#8b5cf6',
                            ],
                            borderWidth: 0,
                          },
                        ],
                      }}
                      options={{
                        responsive: true,
                        maintainAspectRatio: false,
                        plugins: {
                          legend: {
                            position: 'bottom',
                            labels: { color: textColor },
                          },
                        },
                      }}
                    />
                  </div>
                </div>
              )}
          </div>

          {/* Sessions per gateway over time (#1150). The pie above is the live snapshot;
              this is the history beside it, because a snapshot cannot distinguish a
              scheduled power window from a dead control channel -- both just show an edge
              with no sessions right now. */}
          {data.global && (
            <div className="card p-xl mb-xl">
              <h4 className="text-muted text-base mb-lg">
                {t('sessions_per_edge', 'Sessions per Gateway')}
              </h4>
              <p className="text-muted text-sm mt-0 mb-lg">
                {t(
                  'sessions_per_edge_desc',
                  'Distinct tunnel sessions each gateway carried per day. A line falling to zero means that gateway stopped receiving sessions.',
                )}
              </p>
              {/* Rendered even with nothing to plot. An admin opening this to ask which
                  gateways are carrying sessions learns nothing from an absent panel --
                  "no sessions recorded yet" is an answer, and hiding it recreates exactly
                  the gap this closes. */}
              {!data.global.node_daily ||
              data.global.node_daily.length === 0 ? (
                <p className="text-muted text-sm m-0">
                  {t(
                    'sessions_per_edge_empty',
                    'No session data recorded yet for this period.',
                  )}
                </p>
              ) : (
                <div className="chart-container">
                  <Line
                    data={nodeSeries(data.global.node_daily)}
                    options={{
                      responsive: true,
                      maintainAspectRatio: false,
                      plugins: {
                        legend: {
                          position: 'bottom',
                          labels: { color: textColor },
                        },
                      },
                      scales: {
                        y: {
                          beginAtZero: true,
                          // Sessions are whole tunnels; a "2.5 sessions" gridline is
                          // meaningless and Chart.js will produce one on small ranges.
                          ticks: { precision: 0 },
                        },
                      },
                    }}
                  />
                </div>
              )}
            </div>
          )}

          {/* Region latency (#1151). The data path merged in #1501 with no reader; the
              epic was held open for exactly this panel.

              Leads with poorly_served_users because that is the figure the placement
              decision turns on -- a region can look healthy on its own median while the
              people using it have no good option anywhere, and only this number shows
              that. */}
          <div className="card p-xl mb-xl">
            <h4 className="text-muted text-base mb-lg">
              {t('region_latency', 'Region Latency')}
            </h4>
            {!regionLatency || !regionLatency.regions?.length ? (
              <p className="text-muted text-sm m-0">
                {t(
                  'region_latency_empty',
                  'No region probes recorded yet. Clients report these when they pick a gateway.',
                )}
              </p>
            ) : (
              <>
                <p
                  className={`text-sm mb-lg ${regionLatency.poorly_served_users > 0 ? 'text-warning' : 'text-muted'}`}
                >
                  <strong>{regionLatency.poorly_served_users}</strong>{' '}
                  {t(
                    'region_latency_poorly_served',
                    'users had no region faster than',
                  )}{' '}
                  {regionLatency.threshold_ms}ms
                </p>
                <div className="table-responsive">
                  <table className="w-full">
                    <thead>
                      <tr className="border-b text-left">
                        <th className="th-col">{t('region', 'Region')}</th>
                        <th className="th-col">{t('users', 'Users')}</th>
                        <th className="th-col">{t('median', 'Median')}</th>
                        <th className="th-col">{t('p90', 'p90')}</th>
                        <th className="th-col">
                          {t('unreachable', 'Unreachable')}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {regionLatency.regions.map((r: any) => (
                        <tr key={r.region} className="border-b">
                          <td className="p-md fw-semibold">
                            {r.region.toUpperCase()}
                          </td>
                          <td className="p-md">{r.users}</td>
                          <td className="p-md">{r.median_ms}ms</td>
                          <td className="p-md">{r.p90_ms}ms</td>
                          {/* A region nobody can reach is a placement fact, not missing
                              data, so a non-zero count is called out rather than shown
                              as a plain 0. */}
                          <td
                            className={`p-md ${r.unreachable_users > 0 ? 'text-danger fw-semibold' : 'text-muted'}`}
                          >
                            {r.unreachable_users}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>

          <div className="card overflow-hidden">
            <div className="p-md px-lg border-b">
              <h4 className="m-0 text-base fw-semibold">
                {t('client_versions', 'Client Versions')}
              </h4>
            </div>
            <div className="table-responsive">
              <table className="w-full">
                <thead>
                  <tr className="border-b text-left">
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('version')}
                      aria-sort={getAriaSort('version')}
                    >
                      Version{getSortIndicator('version')}
                    </th>
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('os')}
                      aria-sort={getAriaSort('os')}
                    >
                      OS Platform{getSortIndicator('os')}
                    </th>
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('count')}
                      aria-sort={getAriaSort('count')}
                    >
                      Active Tunnels{getSortIndicator('count')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {clientStats.length === 0 ? (
                    <tr>
                      <td colSpan={3} className="td-empty">
                        {t('no_client_stats', 'No client statistics available')}
                      </td>
                    </tr>
                  ) : (
                    sortedClientStats.map((stat, idx) => (
                      <tr key={idx} className="border-b">
                        <td className="td-cell">
                          <span className="badge admin">
                            {stat.version || 'Unknown'}
                          </span>
                        </td>
                        <td className="td-cell">{stat.os || 'Unknown'}</td>
                        <td className="td-cell fw-bold">{stat.count || 0}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
