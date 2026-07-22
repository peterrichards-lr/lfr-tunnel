import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

export default function AdminSettings() {
  const { user } = useOutletContext<{ user: any }>();
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast, showConfirm, showPrompt } = useUI();
  const [loading, setLoading] = useState(true);

  // System Settings state
  const [allocationRule, setAllocationRule] = useState('round_robin');
  const [defaultDomain, setDefaultDomain] = useState('');
  const [supportedDomains, setSupportedDomains] = useState<string[]>([]);

  // Maintenance state
  const [maintenance, setMaintenance] = useState<any>({ enabled: false, start_time: '', action: '', reason: '', status: 'false', iron_curtain: false });

  // Form states for soft maintenance
  const [softAction, setSoftAction] = useState('System Upgrade');
  const [softReason, setSoftReason] = useState('Deploying updates');
  const [softDuration, setSoftDuration] = useState(30);
  const [softCountdown, setSoftCountdown] = useState(0);

  // Form states for hard maintenance (Iron Curtain)
  const [hardAction, setHardAction] = useState('System Upgrade');
  const [hardReason, setHardReason] = useState('Deploying updates');
  const [hardDuration, setHardDuration] = useState(60);

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

  const { items: sortedBackups, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(backups, ['filename']);


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
      showToast('System settings saved successfully.', 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to save settings.', 'error');
    }
  };

  const toggleSoftMaintenanceMode = async () => {
    let nextState = true;
    if (maintenance.status === "true" || maintenance.status === "pending") {
      nextState = false;
    }

    const promptMsg = nextState 
      ? (softCountdown > 0 
          ? `Are you sure you want to schedule Gateway Soft Maintenance Mode to start in ${softCountdown} minutes?\n\nThis will show a warning banner to users and activate when the timer hits 0.`
          : `Are you sure you want to enable Gateway Soft Maintenance Mode IMMEDIATELY?\n\nThis will instantly close all standard tunnels, reject new connections, and block standard logins!`)
      : "Are you sure you want to disable/cancel Gateway Maintenance Mode?\n\nThis will restore standard gateway routing, logins, and tunnel connections.";

    if (!(await showConfirm(nextState ? "Enable Soft Maintenance" : "Disable Soft Maintenance", promptMsg))) return;

    try {
      const payload: any = { 
        enabled: nextState,
        iron_curtain: false,
        action: softAction,
        reason: softReason,
        duration: softDuration
      };
      if (nextState && softCountdown > 0) {
        payload.countdown_minutes = softCountdown;
      }

      const res = await axios.post('/api/admin/maintenance', payload);
      setMaintenance(res.data);
      showToast(`Soft Maintenance Mode successfully updated!`, "success");
      fetchAllData();
    } catch (e: any) {
      showToast(e.response?.data?.error || "Failed to update maintenance mode", "error");
    }
  };

  const toggleHardMaintenanceMode = async () => {
    let nextState = true;
    if (maintenance.iron_curtain) {
      nextState = false;
    }

    if (nextState) {
      const firstConfirm = await showConfirm(
        "⚠️ Iron Curtain Lockdown WARNING",
        "WARNING: Activating Nginx Iron Curtain Mode will completely lock down the server.\n\n" +
        "This blocks ALL traffic including the Admin Dashboard itself. You will be immediately disconnected " +
        "and will not be able to turn this off from this website.\n\n" +
        "To restore service, you MUST log into the VPS via SSH and run the disable-maintenance scripts.\n\n" +
        "Are you sure you want to proceed?"
      );
      if (!firstConfirm) return;

      const confirmWord = await showPrompt(
        "Confirm Lockdown",
        "To confirm immediate lockdown, please type 'LOCKOUT' in all caps:"
      );
      if (confirmWord !== "LOCKOUT") {
        showToast("Lockdown cancelled: confirmation word did not match.", "info");
        return;
      }

      try {
        const payload = {
          enabled: true,
          iron_curtain: true,
          action: hardAction,
          reason: hardReason,
          duration: hardDuration
        };

        const res = await axios.post('/api/admin/maintenance', payload);
        setMaintenance(res.data);
        showToast("Nginx Iron Curtain activated. You will be disconnected shortly.", "error");
        setTimeout(() => {
          window.location.reload();
        }, 1500);
      } catch (e: any) {
        showToast(e.response?.data?.error || "Failed to activate Iron Curtain", "error");
      }
    } else {
      const confirmDisable = await showConfirm(
        "Disable Iron Curtain",
        "Are you sure you want to disable Nginx Iron Curtain Mode?\n\n" +
        "Note: If you are seeing this, either the server is not actually behind the Nginx block or you are accessing it via a bypassed endpoint. Disabling will remove the trigger files."
      );
      if (!confirmDisable) return;

      try {
        const payload = {
          enabled: false,
          iron_curtain: true
        };

        const res = await axios.post('/api/admin/maintenance', payload);
        setMaintenance(res.data);
        showToast("Nginx Iron Curtain disabled successfully.", "success");
        fetchAllData();
      } catch (e: any) {
        showToast(e.response?.data?.error || "Failed to disable Iron Curtain", "error");
      }
    }
  };

  const testWebhook = async () => {
    try {
      setWebhookTesting(true);
      const res = await axios.post('/api/admin/test-webhook');
      showToast(`Webhook Triggered: ${res.data.message}`, 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Webhook Test Failed.', 'error');
    } finally {
      setWebhookTesting(false);
    }
  };

  const sendBroadcast = async () => {
    try {
      setBroadcastSending(true);
      await axios.post('/api/admin/broadcast', { message: broadcastMessage });
      showToast('Broadcast message sent successfully.', 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to send broadcast.', 'error');
    } finally {
      setBroadcastSending(false);
    }
  };

  const clearBroadcast = async () => {
    try {
      setBroadcastSending(true);
      await axios.post('/api/admin/broadcast', { message: '' });
      setBroadcastMessage('');
      showToast('Broadcast message cleared.', 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to clear broadcast.', 'error');
    } finally {
      setBroadcastSending(false);
    }
  };

  const triggerBackup = async () => {
    try {
      setLoadingBackups(true);
      await axios.post('/api/admin/backups');
      showToast('Backup triggered successfully.', 'success');
      fetchAllData();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to trigger backup.', 'error');
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
        <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginBottom: '24px' }}>
          Configure maintenance gates to manage system upgrades and deployments. Soft maintenance gracefully alerts and migrates standard sessions, while the Iron Curtain locks down the VPS web proxy completely.
        </p>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: 'var(--spacing-xl)' }}>
          {/* Soft Maintenance Section */}
          <div style={{ padding: 'var(--spacing-lg)', border: '1px solid var(--border)', borderRadius: '8px', display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <h5 style={{ margin: 0, fontSize: '15px', fontWeight: 600 }}>Soft Maintenance</h5>
              <span style={{ 
                padding: '2px 8px', 
                borderRadius: '4px', 
                fontSize: '11px', 
                fontWeight: 700,
                textTransform: 'uppercase',
                background: maintenance.status === 'true' ? 'var(--status-danger-bg)' : maintenance.status === 'pending' ? 'var(--status-warning-bg)' : 'var(--status-success-bg)',
                color: maintenance.status === 'true' ? 'var(--status-danger-text)' : maintenance.status === 'pending' ? 'var(--status-warning-text)' : 'var(--status-success-text)'
              }}>
                {maintenance.status === 'true' ? 'Active' : maintenance.status === 'pending' ? 'Scheduled' : 'Inactive'}
              </span>
            </div>

            {maintenance.status !== 'false' ? (
              <div style={{ background: 'rgba(255,255,255,0.02)', padding: 'var(--spacing-md)', borderRadius: '6px', fontSize: '13px', display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <div><strong>Action:</strong> {maintenance.action}</div>
                <div><strong>Reason:</strong> {maintenance.reason}</div>
                <div><strong>Scheduled/Started:</strong> {formatDate(maintenance.start_time)}</div>
                {maintenance.duration > 0 && <div><strong>Duration:</strong> {maintenance.duration} minutes</div>}
              </div>
            ) : (
              <>
                <div className="form-group" style={{ margin: 0 }}>
                  <label style={{ fontSize: '12px' }}>Action Name</label>
                  <input type="text" className="input-field" value={softAction} onChange={e => setSoftAction(e.target.value)} />
                </div>
                <div className="form-group" style={{ margin: 0 }}>
                  <label style={{ fontSize: '12px' }}>Reason</label>
                  <input type="text" className="input-field" value={softReason} onChange={e => setSoftReason(e.target.value)} />
                </div>
                <div style={{ display: 'flex', gap: 'var(--spacing-md)' }}>
                  <div className="form-group" style={{ margin: 0, flex: 1 }}>
                    <label style={{ fontSize: '12px' }}>Duration (min)</label>
                    <input type="number" className="input-field" value={softDuration} onChange={e => setSoftDuration(parseInt(e.target.value) || 0)} />
                  </div>
                  <div className="form-group" style={{ margin: 0, flex: 1 }}>
                    <label style={{ fontSize: '12px' }}>Countdown (min)</label>
                    <select className="input-field" value={softCountdown} onChange={e => setSoftCountdown(parseInt(e.target.value) || 0)}>
                      <option value={0}>Immediate (0m)</option>
                      <option value={5}>5 minutes</option>
                      <option value={10}>10 minutes</option>
                      <option value={15}>15 minutes</option>
                      <option value={30}>30 minutes</option>
                      <option value={60}>60 minutes</option>
                    </select>
                  </div>
                </div>
              </>
            )}

            <button 
              className={`btn ${maintenance.status !== 'false' ? 'btn-secondary' : 'btn-primary'}`} 
              onClick={toggleSoftMaintenanceMode}
              style={{ marginTop: 'auto' }}
            >
              {maintenance.status !== 'false' ? 'Disable Soft Maintenance' : softCountdown > 0 ? 'Schedule Soft Maintenance' : 'Enable Soft Maintenance'}
            </button>
          </div>

          {/* Hard Maintenance Section */}
          <div style={{ padding: 'var(--spacing-lg)', border: '1px solid var(--border)', borderRadius: '8px', display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <h5 style={{ margin: 0, fontSize: '15px', fontWeight: 600 }}>Iron Curtain (Hard Lockdown)</h5>
              <span style={{ 
                padding: '2px 8px', 
                borderRadius: '4px', 
                fontSize: '11px', 
                fontWeight: 700,
                textTransform: 'uppercase',
                background: maintenance.iron_curtain ? 'var(--status-danger-bg)' : 'var(--status-success-bg)',
                color: maintenance.iron_curtain ? 'var(--status-danger-text)' : 'var(--status-success-text)'
              }}>
                {maintenance.iron_curtain ? 'Locked' : 'Unlocked'}
              </span>
            </div>

            {!maintenance.iron_curtain ? (
              <>
                <div className="form-group" style={{ margin: 0 }}>
                  <label style={{ fontSize: '12px' }}>Lockout Action</label>
                  <input type="text" className="input-field" value={hardAction} onChange={e => setHardAction(e.target.value)} />
                </div>
                <div className="form-group" style={{ margin: 0 }}>
                  <label style={{ fontSize: '12px' }}>Lockout Reason</label>
                  <input type="text" className="input-field" value={hardReason} onChange={e => setHardReason(e.target.value)} />
                </div>
                <div className="form-group" style={{ margin: 0 }}>
                  <label style={{ fontSize: '12px' }}>Duration (min)</label>
                  <input type="number" className="input-field" value={hardDuration} onChange={e => setHardDuration(parseInt(e.target.value) || 0)} />
                </div>
              </>
            ) : (
              <div style={{ background: 'rgba(239, 68, 68, 0.05)', border: '1px dashed var(--danger)', padding: 'var(--spacing-md)', borderRadius: '6px', fontSize: '13px', display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <div style={{ fontWeight: 'bold', color: 'var(--danger)' }}>SERVER IS UNDER HARD LOCKOUT</div>
                <div><strong>Action:</strong> {maintenance.action}</div>
                <div><strong>Reason:</strong> {maintenance.reason}</div>
                <div><strong>Expires in:</strong> {maintenance.duration} minutes</div>
              </div>
            )}

            <button 
              className={`btn ${maintenance.iron_curtain ? 'btn-secondary' : 'btn-danger'}`} 
              onClick={toggleHardMaintenanceMode}
              style={{ marginTop: 'auto' }}
            >
              {maintenance.iron_curtain ? 'Disable Iron Curtain' : 'Enable Iron Curtain'}
            </button>
          </div>
        </div>
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
            placeholder={t('enter_broadcast_message_placeholder', 'Enter broadcast message...')}
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
                placeholder={t('search_backups_placeholder', 'Search backups...')} 
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
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('filename')} aria-sort={getAriaSort('filename')}>Filename{getSortIndicator('filename')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('size_bytes')} aria-sort={getAriaSort('size_bytes')}>Size{getSortIndicator('size_bytes')}</th>
                  <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Created At{getSortIndicator('created_at')}</th>
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
                    <tr key={b.filename}>
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
