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
  Filler
);

const formatBytes = (bytes: number, decimals = 2) => {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
};

export default function AdminAnalytics() {
  const { t } = useI18n();
  const { theme } = useSettings();
  
  const [data, setData] = useState<any>(null);
  const [clientStats, setClientStats] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [timeRange, setTimeRange] = useState('30'); // Default to 30 days

  const { items: sortedClientStats, requestSort, getSortIndicator, getAriaSort } = useTableSort(clientStats, ['version', 'os', 'count']);

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      try {
        const query = timeRange !== '0' ? `?days=${timeRange}` : '';
        const [analyticsRes, clientsRes] = await Promise.all([
          axios.get(`/api/analytics${query}`),
          axios.get('/api/admin/analytics/clients').catch(() => ({ data: [] }))
        ]);
        setData(analyticsRes.data);
        setClientStats(clientsRes.data || []);
      } catch (err) {
        console.error('Failed to load analytics', err);
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [timeRange]);

  const isLight = theme === 'light';
  const textColor = isLight ? '#475569' : '#94a3b8';
  const gridColor = isLight ? '#e2e8f0' : '#334155';

  const chartOptions = () => ({
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: { 
        position: 'top' as const, 
        labels: { color: textColor, font: { family: 'Inter, system-ui, sans-serif' } } 
      },
      tooltip: {
        callbacks: {
          label: (context: any) => `${context.dataset.label}: ${formatBytes(context.raw)}`
        }
      }
    },
    scales: {
      x: { 
        grid: { color: gridColor }, 
        ticks: { color: textColor, font: { family: 'Inter, system-ui, sans-serif' } } 
      },
      y: { 
        grid: { color: gridColor }, 
        ticks: { 
          color: textColor, 
          font: { family: 'Inter, system-ui, sans-serif' },
          callback: (value: any) => formatBytes(value) 
        } 
      }
    }
  });

  const doughnutOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: { 
        position: 'right' as const, 
        labels: { color: textColor, font: { family: 'Inter, system-ui, sans-serif' } } 
      },
      tooltip: {
        callbacks: {
          label: (context: any) => formatBytes(context.raw)
        }
      }
    }
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
        <h2 className="page-header__title">{t('system_analytics', 'System Analytics')}</h2>
        <div className="flex gap-md">
          <select 
            className="input-field w-auto" 
            style={{ height: '38px', boxSizing: 'border-box', padding: '0 12px' }}
            value={timeRange} 
            onChange={(e) => setTimeRange(e.target.value)}
          >
            <option value="7">Last 7 Days</option>
            <option value="14">Last 14 Days</option>
            <option value="30">Last 30 Days</option>
            <option value="0">All Time</option>
          </select>
          <button className="btn btn-secondary w-auto inline-flex items-center gap-sm" onClick={handlePrint} style={{ whiteSpace: 'nowrap', height: '38px', boxSizing: 'border-box', padding: '0 16px' }}>
            📄 {t('export_pdf', 'Export PDF')}
          </button>
        </div>
      </div>

      {data.personal && (
        <div className="print-section">
          <h3 className="text-lg fw-bold mb-lg">{t('personal_usage', 'Personal Usage')}</h3>
          <div className="auto-grid-lg mb-2xl">
            
            {data.personal.daily && data.personal.daily.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">{t('bandwidth_over_time', 'Bandwidth Over Time')}</h4>
                <div style={{ height: '300px' }}>
                  <Line 
                    data={{
                      labels: data.personal.daily.map((d: any) => d.date),
                      datasets: [
                        { label: 'Data In', data: data.personal.daily.map((d: any) => d.bytes_in), borderColor: '#3b82f6', backgroundColor: '#3b82f620', fill: true, tension: 0.4 },
                        { label: 'Data Out', data: data.personal.daily.map((d: any) => d.bytes_out), borderColor: '#10b981', backgroundColor: '#10b98120', fill: true, tension: 0.4 }
                      ]
                    }} 
                    options={chartOptions()} 
                  />
                </div>
              </div>
            )}

            {data.personal.tunnels && data.personal.tunnels.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">{t('bandwidth_by_tunnel', 'Bandwidth by Tunnel')}</h4>
                <div style={{ height: '300px' }}>
                  <Doughnut 
                    data={{
                      labels: data.personal.tunnels.map((t: any) => t.full_host),
                      datasets: [{
                        label: 'Total Bandwidth',
                        data: data.personal.tunnels.map((t: any) => t.bytes_in + t.bytes_out),
                        backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899'],
                        borderWidth: 0
                      }]
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
          <h3 className="text-lg fw-bold mb-lg">{t('global_statistics', 'Global Statistics')}</h3>
          <div className="auto-grid-lg mb-2xl">
            
            {data.global.daily && data.global.daily.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">{t('global_bandwidth', 'Global Bandwidth')}</h4>
                <div style={{ height: '300px' }}>
                  <Line 
                    data={{
                      labels: data.global.daily.map((d: any) => d.date),
                      datasets: [
                        { label: 'Total Data In', data: data.global.daily.map((d: any) => d.bytes_in), borderColor: '#6366f1', backgroundColor: '#6366f120', fill: true, tension: 0.4 },
                        { label: 'Total Data Out', data: data.global.daily.map((d: any) => d.bytes_out), borderColor: '#f43f5e', backgroundColor: '#f43f5e20', fill: true, tension: 0.4 }
                      ]
                    }} 
                    options={chartOptions()} 
                  />
                </div>
              </div>
            )}

            {data.global.top_users && data.global.top_users.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">{t('top_users_bandwidth', 'Top Users by Bandwidth')}</h4>
                <div style={{ height: '300px' }}>
                  <Bar 
                    data={{
                      labels: data.global.top_users.map((u: any) => (u.email || "Anonymous").split('@')[0]),
                      datasets: [{
                        label: 'Total Bandwidth',
                        data: data.global.top_users.map((u: any) => u.bytes_in + u.bytes_out),
                        backgroundColor: '#8b5cf6',
                        borderRadius: 4
                      }]
                    }} 
                    options={chartOptions()} 
                  />
                </div>
              </div>
            )}

            {data.global.top_tunnels && data.global.top_tunnels.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">{t('top_tunnels_bandwidth', 'Top Tunnels by Bandwidth')}</h4>
                <div style={{ height: '300px' }}>
                  <Doughnut 
                    data={{
                      labels: data.global.top_tunnels.map((tItem: any) => tItem.full_host),
                      datasets: [{
                        label: 'Total Bandwidth',
                        data: data.global.top_tunnels.map((tItem: any) => tItem.bytes_in + tItem.bytes_out),
                        backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#f43f5e', '#14b8a6', '#6366f1', '#a855f7'],
                        borderWidth: 0
                      }]
                    }} 
                    options={doughnutOptions} 
                  />
                </div>
              </div>
            )}

            {data.global.portal_stats && data.global.portal_stats.length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">Portal Usage</h4>
                <div style={{ height: '300px' }}>
                  <Doughnut 
                    data={{
                      labels: data.global.portal_stats.map((s: any) => s.version.toUpperCase()),
                      datasets: [{
                        data: data.global.portal_stats.map((s: any) => s.count),
                        backgroundColor: ['#0b5fff', '#10b981', '#f59e0b', '#8b5cf6'],
                        borderWidth: 0
                      }]
                    }} 
                    options={{
                      responsive: true,
                      maintainAspectRatio: false,
                      plugins: {
                        legend: { position: 'bottom', labels: { color: 'var(--text-color)' } }
                      },
                      cutout: '70%'
                    }} 
                  />
                </div>
              </div>
            )}

            {data.global.node_distribution && Object.keys(data.global.node_distribution).length > 0 && (
              <div className="card p-xl">
                <h4 className="text-muted text-base mb-lg">Tunnel Distribution (Active Nodes)</h4>
                <div style={{ height: '300px' }}>
                  <Pie 
                    data={{
                      labels: Object.keys(data.global.node_distribution).map(k => k.toUpperCase()),
                      datasets: [{
                        data: Object.values(data.global.node_distribution),
                        backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6'],
                        borderWidth: 0
                      }]
                    }} 
                    options={{
                      responsive: true,
                      maintainAspectRatio: false,
                      plugins: {
                        legend: { position: 'bottom', labels: { color: 'var(--text-color)' } }
                      }
                    }} 
                  />
                </div>
              </div>
            )}
          </div>

          <div className="card overflow-hidden">
            <div className="p-md px-lg border-b">
              <h4 className="m-0 text-base fw-semibold">{t('client_versions', 'Client Versions')}</h4>
            </div>
            <div className="table-responsive">
              <table className="w-full">
                <thead>
                  <tr className="border-b text-left">
                    <th className="th-col th-col--sortable" onClick={() => requestSort('version')} aria-sort={getAriaSort('version')}>Version{getSortIndicator('version')}</th>
                    <th className="th-col th-col--sortable" onClick={() => requestSort('os')} aria-sort={getAriaSort('os')}>OS Platform{getSortIndicator('os')}</th>
                    <th className="th-col th-col--sortable" onClick={() => requestSort('count')} aria-sort={getAriaSort('count')}>Active Tunnels{getSortIndicator('count')}</th>
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
                          <span style={{ background: 'var(--primary)', color: 'white', padding: '2px 8px', borderRadius: '4px', fontSize: '12px', fontWeight: '500' }}>
                            {stat.version || "Unknown"}
                          </span>
                        </td>
                        <td className="td-cell">{stat.os || "Unknown"}</td>
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
