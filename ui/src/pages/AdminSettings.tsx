import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';

export default function AdminSettings() {
  const { user } = useOutletContext<{ user: any }>();
  const { formatDate } = useSettings();
  const [loading, setLoading] = useState(true);

  // System Settings state
  const [allocationRule, setAllocationRule] = useState('round_robin');
  const [defaultDomain, setDefaultDomain] = useState('');
  const [supportedDomains, setSupportedDomains] = useState<string[]>([]);

  // Maintenance state
  const [maintenance, setMaintenance] = useState({ enabled: false, start_time: '', action: '', reason: '' });

  // Config view state
  const [serverConfig, setServerConfig] = useState('');
  const [configError, setConfigError] = useState('');

  const [webhookTesting, setWebhookTesting] = useState(false);

  // Broadcast state
  const [broadcastMessage, setBroadcastMessage] = useState('');
  const [broadcastSending, setBroadcastSending] = useState(false);

  // Backups state
  const [backups, setBackups] = useState<any[]>([]);
  const [loadingBackups, setLoadingBackups] = useState(false);
  const [page, setPage] = useState(0);
  const ROWS_PER_PAGE = 5;

  const fetchAllData = async () => {
    try {
      const vRes = await axios.get('/api/version');
      setSupportedDomains(vRes.data.supported_domains || []);

      const sRes = await axios.get('/api/admin/system-settings');
      setAllocationRule(sRes.data.domain_allocation_rule || 'round_robin');
      setDefaultDomain(sRes.data.default_domain || '');

      const mRes = await axios.get('/api/admin/maintenance');
      setMaintenance(mRes.data);

      if (user.role === 'owner' || user.role === 'admin') {
        try {
          const cRes = await axios.get('/api/admin/config-view');
          setServerConfig(cRes.data.config || '');
        } catch (e: any) {
          setConfigError(e.response?.status === 403 ? 'Not authorized to view config' : 'Failed to load configuration');
        }
        
        // Fetch backups
        try {
          const bRes = await axios.get('/api/admin/backups');
          setBackups(bRes.data || []);
        } catch (e) {
          console.error("Failed to load backups", e);
        }
      }
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const { items: sortedBackups, requestSort, getSortIndicator, searchQuery, setSearchQuery } = useTableSort(backups, ['filename']);


  useEffect(() => {
    fetchAllData();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const saveSystemSettings = async () => {
    try {
      await axios.put('/api/admin/system-settings', {
        domain_allocation_rule: allocationRule,
        default_domain: defaultDomain
      });
      alert('System settings saved successfully.');
    } catch (e: any) {
      alert(`Failed to save settings: ${e.response?.data?.error || 'Unknown error'}`);
    }
  };

  const toggleMaintenance = async () => {
    try {
      if (maintenance.enabled) {
        if (!confirm('Are you sure you want to disable Maintenance Mode?')) return;
        await axios.delete('/api/admin/maintenance');
        alert('Maintenance Mode disabled.');
      } else {
        const action = prompt('Maintenance Action (e.g., System Upgrade):', 'System Upgrade');
        if (!action) return;
        const reason = prompt('Maintenance Reason (e.g., Deploying new version):', 'Deploying updates');
        if (!reason) return;
        
        await axios.put('/api/admin/maintenance', { action, reason });
        alert('Maintenance Mode enabled.');
      }
      fetchAllData();
    } catch (e: any) {
      alert(`Failed to toggle maintenance: ${e.response?.data?.error || 'Unknown error'}`);
    }
  };

  const testWebhook = async () => {
    try {
      setWebhookTesting(true);
      const res = await axios.post('/api/admin/test-webhook');
      alert(`Webhook Triggered: ${res.data.message}`);
    } catch (e: any) {
      alert(`Webhook Test Failed: ${e.response?.data?.error || 'Unknown error'}`);
    } finally {
      setWebhookTesting(false);
    }
  };

  const sendBroadcast = async () => {
    try {
      setBroadcastSending(true);
      await axios.post('/api/admin/broadcast', { message: broadcastMessage });
      alert('Broadcast message sent successfully.');
    } catch (e: any) {
      alert(`Failed to send broadcast: ${e.response?.data?.error || 'Unknown error'}`);
    } finally {
      setBroadcastSending(false);
    }
  };

  const clearBroadcast = async () => {
    try {
      setBroadcastSending(true);
      await axios.post('/api/admin/broadcast', { message: '' });
      setBroadcastMessage('');
      alert('Broadcast message cleared.');
    } catch (e: any) {
      alert(`Failed to clear broadcast: ${e.response?.data?.error || 'Unknown error'}`);
    } finally {
      setBroadcastSending(false);
    }
  };

  const triggerBackup = async () => {
    try {
      setLoadingBackups(true);
      await axios.post('/api/admin/backups');
      alert('Backup triggered successfully.');
      fetchAllData();
    } catch (e: any) {
      alert(`Failed to trigger backup: ${e.response?.data?.error || 'Unknown error'}`);
    } finally {
      setLoadingBackups(false);
    }
  };

  const formatSizeKB = (bytes: number) => {
    return (bytes / 1024).toFixed(1) + ' KB';
  };

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ marginBottom: '24px' }}>
          <Skeleton width={180} height={28} />
          <Skeleton width={280} height={16} style={{ marginTop: '8px' }} />
        </div>

        <div className="card" style={{ padding: '24px', marginBottom: '24px' }}>
          <Skeleton width={150} height={20} style={{ marginBottom: '16px' }} />
          <div className="form-group" style={{ marginTop: '16px' }}>
            <Skeleton width={100} height={16} style={{ marginBottom: '8px' }} />
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
          <div className="form-group" style={{ marginTop: '16px' }}>
            <Skeleton width={100} height={16} style={{ marginBottom: '8px' }} />
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
          <div style={{ marginTop: '24px' }}>
            <Skeleton width={120} height={40} />
          </div>
        </div>

        <div className="card" style={{ padding: '24px' }}>
          <Skeleton width={150} height={20} style={{ marginBottom: '16px' }} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            <Skeleton width="100%" height={24} />
            <Skeleton width="100%" height={24} />
            <Skeleton width="100%" height={24} />
          </div>
        </div>
      </div>
    );
  }

  return (
    <div>
      <div style={{ marginBottom: '24px' }}>
        <h3>System Settings</h3>
        <p style={{ color: 'var(--text-muted)' }}>Configure global routing and domain parameters.</p>
      </div>

      <div className="card" style={{ marginBottom: '24px' }}>
        <h4>Domain Allocation</h4>
        <div className="form-group" style={{ marginTop: '16px' }}>
          <label>Allocation Rule</label>
          <select className="input-field" value={allocationRule} onChange={(e) => setAllocationRule(e.target.value)}>
            <option value="round_robin">Round Robin</option>
            <option value="least_connections">Least Connections</option>
            <option value="consistent_hashing">Consistent Hashing</option>
            <option value="random">Random</option>
          </select>
        </div>
        <div className="form-group">
          <label>Default Domain</label>
          <select className="input-field" value={defaultDomain} onChange={(e) => setDefaultDomain(e.target.value)}>
            <option value="">None (Force Error if Contextual Fails)</option>
            {supportedDomains.map((d) => (
              <option key={d} value={d}>{d}</option>
            ))}
          </select>
        </div>
        <button className="btn btn-primary" onClick={saveSystemSettings}>Save Settings</button>
      </div>

      <div className="card" style={{ marginBottom: '24px' }}>
        <h4>Maintenance Mode</h4>
        <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginBottom: '16px' }}>
          When enabled, the gateway will gracefully reject new tunnel connections and display an offline page to external visitors. Existing active connections will be preserved until they naturally disconnect.
        </p>
        
        {maintenance.enabled && (
          <div style={{ background: 'rgba(239, 68, 68, 0.1)', borderLeft: '4px solid var(--danger)', padding: '12px', marginBottom: '16px', borderRadius: '4px' }}>
            <div style={{ fontWeight: 'bold', color: 'var(--danger)', marginBottom: '4px' }}>MAINTENANCE MODE IS ACTIVE</div>
            <div style={{ fontSize: '12px', color: 'var(--text-main)' }}>Action: {maintenance.action}</div>
            <div style={{ fontSize: '12px', color: 'var(--text-main)' }}>Reason: {maintenance.reason}</div>
            <div style={{ fontSize: '12px', color: 'var(--text-main)' }}>Started: {formatDate(maintenance.start_time)}</div>
          </div>
        )}

        <button className={`btn ${maintenance.enabled ? 'btn-secondary' : 'btn-danger'}`} onClick={toggleMaintenance}>
          {maintenance.enabled ? 'Disable Maintenance Mode' : 'Enable Maintenance Mode'}
        </button>
      </div>

      <div className="card" style={{ marginBottom: '24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
          <div>
            <h4 style={{ margin: 0 }}>Integrations</h4>
            <div style={{ fontSize: '13px', color: 'var(--text-muted)', marginTop: '4px' }}>Test your configured webhooks (Slack/Teams).</div>
          </div>
          <button className="btn btn-primary" disabled={webhookTesting} onClick={testWebhook}>
            {webhookTesting ? 'Sending...' : 'Trigger Test Webhook'}
          </button>
        </div>
      </div>

      <div className="card" style={{ marginBottom: '24px' }}>
        <h4>Global Broadcast</h4>
        <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginBottom: '16px' }}>
          Push a real-time banner alert to all active developer sessions.
        </p>
        <div className="form-group">
          <input
            type="text"
            className="input-field"
            placeholder="Enter broadcast message..."
            value={broadcastMessage}
            onChange={(e) => setBroadcastMessage(e.target.value)}
          />
        </div>
        <div style={{ display: 'flex', gap: '8px', marginTop: '16px' }}>
          <button className="btn btn-primary" disabled={broadcastSending || !broadcastMessage.trim()} onClick={sendBroadcast}>
            {broadcastSending ? 'Sending...' : 'Send Broadcast'}
          </button>
          <button className="btn btn-secondary" disabled={broadcastSending} onClick={clearBroadcast}>
            Clear Broadcast
          </button>
        </div>
      </div>

      {(user.role === 'owner' || user.role === 'admin') && (
        <div className="card" style={{ marginBottom: '24px' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
            <div>
              <h4 style={{ margin: 0 }}>Database Backups</h4>
              <div style={{ fontSize: '13px', color: 'var(--text-muted)', marginTop: '4px' }}>Manage and download automated database snapshots.</div>
            </div>
            <button className="btn btn-primary" disabled={loadingBackups} onClick={triggerBackup}>
              {loadingBackups ? 'Running...' : 'Trigger Backup'}
            </button>
          </div>
          
          {backups.length > 0 && (
            <div style={{ marginBottom: '16px' }}>
              <input 
                type="text" 
                placeholder="Search backups..." 
                value={searchQuery} 
                onChange={e => { setSearchQuery(e.target.value); setPage(0); }}
                style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
              />
            </div>
          )}
          <div className="table-responsive" style={{ marginTop: '16px' }}>
            <table className="table">
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('filename')}>Filename{getSortIndicator('filename')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('size_bytes')}>Size{getSortIndicator('size_bytes')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('created_at')}>Created At{getSortIndicator('created_at')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {backups.length === 0 ? (
                  <tr>
                    <td colSpan={4} style={{ textAlign: 'center', opacity: 0.6, padding: '16px' }}>
                      No backups found yet. The first backup runs on server startup.
                    </td>
                  </tr>
                ) : (
                  sortedBackups.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE).map(b => (
                    <tr key={b.filename} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s' }} onMouseOver={e => e.currentTarget.style.background = 'rgba(255,255,255,0.03)'} onMouseOut={e => e.currentTarget.style.background = 'transparent'}>
                      <td style={{ padding: '16px', fontFamily: 'monospace', fontWeight: 500, fontSize: '14px' }}>{b.filename}</td>
                      <td style={{ padding: '16px', fontSize: '14px' }}>{formatSizeKB(b.size_bytes)}</td>
                      <td style={{ padding: '16px', fontSize: '14px', color: 'var(--text-muted)' }}>{formatDate(b.created_at)}</td>
                      <td style={{ padding: '16px' }}>
                        <a 
                          href={`/api/admin/backups/download/${encodeURIComponent(b.filename)}`} 
                          download
                          className="btn btn-outline" 
                          style={{ padding: '4px 8px', fontSize: '12px', display: 'inline-block', textDecoration: 'none' }}
                        >
                          Download
                        </a>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
            
            {sortedBackups.length > 0 && (
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '16px', borderTop: '1px solid var(--border-color)' }}>
                <div style={{ fontSize: '13px', color: 'var(--text-muted)' }}>
                  Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedBackups.length)} of {sortedBackups.length}
                </div>
                <div style={{ display: 'flex', gap: '8px' }}>
                  <button 
                    className="btn btn-secondary" 
                    onClick={() => setPage(0)}
                    disabled={page === 0}
                    style={{ padding: '4px 12px', fontSize: '13px' }}
                  >
                    First
                  </button>
                  <button 
                    className="btn btn-secondary" 
                    disabled={page === 0} 
                    onClick={() => setPage(page - 1)}
                    style={{ padding: '4px 12px', fontSize: '13px' }}
                  >
                    Previous
                  </button>
                  <span style={{ padding: '4px 8px', fontSize: '14px' }}>Page {page + 1} of {Math.ceil(sortedBackups.length / ROWS_PER_PAGE)}</span>
                  <button 
                    className="btn btn-secondary" 
                    disabled={(page + 1) * ROWS_PER_PAGE >= sortedBackups.length} 
                    onClick={() => setPage(page + 1)}
                    style={{ padding: '4px 12px', fontSize: '13px' }}
                  >
                    Next
                  </button>
                  <button 
                    className="btn btn-secondary" 
                    onClick={() => setPage(Math.max(0, Math.ceil(sortedBackups.length / ROWS_PER_PAGE) - 1))}
                    disabled={(page + 1) * ROWS_PER_PAGE >= sortedBackups.length}
                    style={{ padding: '4px 12px', fontSize: '13px' }}
                  >
                    Last
                  </button>
                </div>
              </div>
            )}

          </div>
        </div>
      )}

      {(user.role === 'owner' || user.role === 'admin') && (
        <div className="card" id="card-server-config">
          <h4>Server Configuration</h4>
          <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginBottom: '16px' }}>
            Current parsed server configuration with sensitive secrets obfuscated.
          </p>
          {configError ? (
            <div style={{ color: 'var(--danger)' }}>{configError}</div>
          ) : (
            <pre style={{ background: 'rgba(0,0,0,0.2)', padding: '16px', borderRadius: '8px', overflowX: 'auto', fontSize: '12px', color: 'var(--text-main)', border: '1px solid var(--border-color)' }}>
              {serverConfig || 'No configuration available.'}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
