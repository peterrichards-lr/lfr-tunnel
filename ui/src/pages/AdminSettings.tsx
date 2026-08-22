import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

function objectToYAML(obj: any, indent = 0): string {
  if (!obj || typeof obj !== 'object') {
    return String(obj);
  }
  let yaml = '';
  const spaces = ' '.repeat(indent);
  for (const key of Object.keys(obj)) {
    const val = obj[key];
    if (val === null || val === undefined) {
      yaml += `${spaces}${key}: null\n`;
    } else if (Array.isArray(val)) {
      yaml += `${spaces}${key}:\n`;
      for (const item of val) {
        if (typeof item === 'object') {
          yaml += `${spaces}  -\n${objectToYAML(item, indent + 4)}`;
        } else {
          yaml += `${spaces}  - ${item}\n`;
        }
      }
    } else if (typeof val === 'object') {
      yaml += `${spaces}${key}:\n${objectToYAML(val, indent + 2)}`;
    } else {
      yaml += `${spaces}${key}: ${val}\n`;
    }
  }
  return yaml;
}

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
  const [vanityHookPath, setVanityHookPath] = useState('');
  const [enableVanityHook, setEnableVanityHook] = useState(false);

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
      setVanityHookPath(sRes.data.vanity_domain_hook_path || '');
      setEnableVanityHook(!!sRes.data.enable_vanity_domain_hook);

      const mRes = await axios.get('/api/admin/maintenance');
      setMaintenance(mRes.data);

      if (user.role === 'owner' || user.role === 'admin') {
        try {
          const cRes = await axios.get('/api/admin/config-view');
          setServerConfig(objectToYAML(cRes.data));
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
        default_domain: defaultDomain,
        vanity_domain_hook_path: vanityHookPath,
        enable_vanity_domain_hook: enableVanityHook
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
      <div className="animate-fade-in">
        <div className="mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={280} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl mb-xl">
          <Skeleton width={150} height={20} className="mb-lg" />
          <div className="form-group mt-lg">
            <Skeleton width={100} height={16} className="mb-sm" />
            <Skeleton width="100%" height={40} className="max-w-sm" />
          </div>
          <div className="form-group mt-lg">
            <Skeleton width={100} height={16} className="mb-sm" />
            <Skeleton width="100%" height={40} className="max-w-sm" />
          </div>
          <div className="mt-xl">
            <Skeleton width={120} height={40} />
          </div>
        </div>

        <div className="card p-xl">
          <Skeleton width={150} height={20} className="mb-lg" />
          <div className="flex flex-col gap-md">
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
      <div className="mb-xl">
        <h1 className="page-header__title">System Settings</h1>
        <p className="page-header__desc">Configure global routing and domain parameters.</p>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">Domain Allocation</h4>
        <div className="form-group mt-lg">
          <label className="form-label">Allocation Rule</label>
          <select className="input-field" value={allocationRule} onChange={(e) => setAllocationRule(e.target.value)}>
            <option value="contextual">Contextual (Match requesting domain)</option>
            <option value="preference">Preference (Use configured domain list order)</option>
            <option value="user-preference">User Preference (Use user's preferred domain)</option>
            <option value="round-robin">Round Robin (Sequential load balancing)</option>
            <option value="hashing">Deterministic Hashing (Consistent for user/IP)</option>
            <option value="least-connections">Least Connections (Load-based allocation)</option>
            <option value="random">Random Allocation</option>
          </select>
        </div>
        <div className="form-group">
          <label className="form-label">Default Domain</label>
          <select className="input-field" value={defaultDomain} onChange={(e) => setDefaultDomain(e.target.value)}>
            <option value="">None (Force Error if Contextual Fails)</option>
            {supportedDomains.map((d) => (
              <option key={d} value={d}>{d}</option>
            ))}
          </select>
        </div>
        <button className="btn btn-primary" onClick={saveSystemSettings}>Save Settings</button>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">Vanity Domain Hook</h4>
        {user.role !== 'owner' && (
          <div className="alert-banner alert-banner--warning mb-lg text-sm m-0">
            ⚠️ Only the System Owner is authorized to modify vanity domain hook configurations.
          </div>
        )}
        <div className="form-group mt-lg">
          <label className="flex items-center gap-sm cursor-pointer">
            <input 
              type="checkbox" 
              checked={enableVanityHook} 
              onChange={(e) => setEnableVanityHook(e.target.checked)} 
              disabled={user.role !== 'owner'}
              className="w-auto m-0"
            />
            <span className="form-label m-0">Enable Automated DNS/TLS Provisioning</span>
          </label>
          <p className="text-muted text-xs mt-xs m-0">
            When active, registering a custom domain (via the client <code>-domain</code> flag) runs the specified hook script to automate local Nginx reverse proxy configuration and Certbot SSL/TLS certificate registration.
          </p>
        </div>
        <div className="form-group">
          <label className="form-label">Vanity Domain Hook Script Path</label>
          <input 
            type="text" 
            className="input-field" 
            value={vanityHookPath} 
            onChange={(e) => setVanityHookPath(e.target.value)} 
            placeholder="/usr/local/bin/lfr-vanity-hook.sh"
            disabled={user.role !== 'owner' || !enableVanityHook}
          />
        </div>
        <button className="btn btn-primary" onClick={saveSystemSettings} disabled={user.role !== 'owner'}>Save Settings</button>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-xs">Maintenance Mode</h4>
        <p className="text-muted text-sm mb-xl">
          Configure maintenance gates to manage system upgrades and deployments. Soft maintenance gracefully alerts and migrates standard sessions, while the Iron Curtain locks down the VPS web proxy completely.
        </p>

        <div className="col-2">
          {/* Soft Maintenance Section */}
          <div className="p-lg border rounded flex flex-col gap-md">
            <div className="flex justify-between items-center">
              <h5 className="m-0 text-md fw-semibold">Soft Maintenance</h5>
              {/* .badge already carries these exact --status-* tokens, so the state is a
                  class rather than two parallel ternaries picking colours inline. */}
              <span className={`badge ${maintenance.status === 'true' ? 'danger' : maintenance.status === 'pending' ? 'warning' : 'success'}`}>
                {maintenance.status === 'true' ? 'Active' : maintenance.status === 'pending' ? 'Scheduled' : 'Inactive'}
              </span>
            </div>

            {maintenance.status !== 'false' ? (
              <div className="p-md rounded-sm text-sm flex flex-col gap-xs surface-subtle">
                <div><strong>Action:</strong> {maintenance.action}</div>
                <div><strong>Reason:</strong> {maintenance.reason}</div>
                <div><strong>Scheduled/Started:</strong> {formatDate(maintenance.start_time)}</div>
                {maintenance.duration > 0 && <div><strong>Duration:</strong> {maintenance.duration} minutes</div>}
              </div>
            ) : (
              <>
                <div className="form-group m-0">
                  <label className="form-label text-xs">Action Name</label>
                  <input type="text" className="input-field" value={softAction} onChange={e => setSoftAction(e.target.value)} />
                </div>
                <div className="form-group m-0">
                  <label className="form-label text-xs">Reason</label>
                  <input type="text" className="input-field" value={softReason} onChange={e => setSoftReason(e.target.value)} />
                </div>
                <div className="flex gap-md">
                  <div className="form-group m-0 flex-1">
                    <label className="form-label text-xs">Duration (min)</label>
                    <input type="number" className="input-field" value={softDuration} onChange={e => setSoftDuration(parseInt(e.target.value) || 0)} />
                  </div>
                  <div className="form-group m-0 flex-1">
                    <label className="form-label text-xs">Countdown (min)</label>
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
              className={`btn mt-auto ${maintenance.status !== 'false' ? 'btn-secondary' : 'btn-primary'}`}
              onClick={toggleSoftMaintenanceMode}
            >
              {maintenance.status !== 'false' ? 'Disable Soft Maintenance' : softCountdown > 0 ? 'Schedule Soft Maintenance' : 'Enable Soft Maintenance'}
            </button>
          </div>

          {/* Hard Maintenance Section */}
          <div className="p-lg border rounded flex flex-col gap-md">
            <div className="flex justify-between items-center">
              <h5 className="m-0 text-md fw-semibold">Iron Curtain (Hard Lockdown)</h5>
              <span className={`badge ${maintenance.iron_curtain ? 'danger' : 'success'}`}>
                {maintenance.iron_curtain ? 'Locked' : 'Unlocked'}
              </span>
            </div>

            {!maintenance.iron_curtain ? (
              <>
                <div className="form-group m-0">
                  <label className="form-label text-xs">Lockout Action</label>
                  <input type="text" className="input-field" value={hardAction} onChange={e => setHardAction(e.target.value)} />
                </div>
                <div className="form-group m-0">
                  <label className="form-label text-xs">Lockout Reason</label>
                  <input type="text" className="input-field" value={hardReason} onChange={e => setHardReason(e.target.value)} />
                </div>
                <div className="form-group m-0">
                  <label className="form-label text-xs">Duration (min)</label>
                  <input type="number" className="input-field" value={hardDuration} onChange={e => setHardDuration(parseInt(e.target.value) || 0)} />
                </div>
              </>
            ) : (
              <div className="alert-banner alert-banner--danger flex-col items-start gap-xs">
                <div className="fw-bold text-danger">SERVER IS UNDER HARD LOCKOUT</div>
                <div><strong>Action:</strong> {maintenance.action}</div>
                <div><strong>Reason:</strong> {maintenance.reason}</div>
                <div><strong>Expires in:</strong> {maintenance.duration} minutes</div>
              </div>
            )}

            <button 
              className={`btn mt-auto ${maintenance.iron_curtain ? 'btn-secondary' : 'btn-danger'}`}
              onClick={toggleHardMaintenanceMode}
            >
              {maintenance.iron_curtain ? 'Disable Iron Curtain' : 'Enable Iron Curtain'}
            </button>
          </div>
        </div>
      </div>

      <div className="card mb-xl">
        <div className="flex justify-between items-center">
          <div>
            <h4 className="m-0">Integrations</h4>
            <div className="text-sm text-muted mt-xs">Test your configured webhooks (Slack/Teams).</div>
          </div>
          <button className="btn btn-primary" disabled={webhookTesting} onClick={testWebhook}>
            {webhookTesting ? 'Sending...' : 'Trigger Test Webhook'}
          </button>
        </div>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-xs">Global Broadcast</h4>
        <p className="text-sm text-muted mb-lg">
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
        <div className="flex gap-sm mt-lg">
          <button className="btn btn-primary" disabled={broadcastSending || !broadcastMessage.trim()} onClick={sendBroadcast}>
            {broadcastSending ? 'Sending...' : 'Send Broadcast'}
          </button>
          <button className="btn btn-secondary" disabled={broadcastSending} onClick={clearBroadcast}>
            Clear Broadcast
          </button>
        </div>
      </div>

      {(user.role === 'owner' || user.role === 'admin') && (
        <div className="card mb-xl">
          <div className="flex justify-between items-center mb-lg">
            <div>
              <h4 className="m-0">Database Backups</h4>
              <div className="text-sm text-muted mt-xs">Manage and download automated database snapshots.</div>
            </div>
            <button className="btn btn-primary" disabled={loadingBackups} onClick={triggerBackup}>
              {loadingBackups ? 'Running...' : 'Trigger Backup'}
            </button>
          </div>
          
          {backups.length > 0 && (
            <div className="search-row">
              <input 
                type="text" 
                placeholder={t('search_backups_placeholder', 'Search backups...')} 
                value={searchQuery} 
                onChange={e => { setSearchQuery(e.target.value); setPage(0); }}
                className="search-input"
              />
            </div>
          )}
          <div className="table-responsive mt-lg">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col th-col--sortable" onClick={() => requestSort('filename')} aria-sort={getAriaSort('filename')}>Filename{getSortIndicator('filename')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('size_bytes')} aria-sort={getAriaSort('size_bytes')}>Size{getSortIndicator('size_bytes')}</th>
                  <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Created At{getSortIndicator('created_at')}</th>
                  <th className="th-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {backups.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="td-empty opacity-60">
                      No backups found yet. The first backup runs on server startup.
                    </td>
                  </tr>
                ) : (
                  sortedBackups.slice(page * ROWS_PER_PAGE, (page + 1) * ROWS_PER_PAGE).map(b => (
                    <tr key={b.filename} className="border-b">
                      <td className="td-cell--mono fw-medium">{b.filename}</td>
                      <td className="td-cell">{formatSizeKB(b.size_bytes)}</td>
                      <td className="td-cell text-muted">{formatDate(b.created_at)}</td>
                      <td className="td-cell">
                        <a 
                          href={`/api/admin/backups/download/${encodeURIComponent(b.filename)}`} 
                          download
                          className="btn btn-outline py-xs px-sm text-xs inline-block no-underline"
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
              <div className="pagination-row p-lg border-t">
                <div className="pagination-count">
                  Showing {page * ROWS_PER_PAGE + 1} to {Math.min((page + 1) * ROWS_PER_PAGE, sortedBackups.length)} of {sortedBackups.length}
                </div>
                <div className="pagination-controls">
                  <button 
                    className="btn btn-secondary py-xs px-md text-xs w-auto" 
                    onClick={() => setPage(0)}
                    disabled={page === 0}
                  >
                    First
                  </button>
                  <button 
                    className="btn btn-secondary py-xs px-md text-xs w-auto" 
                    disabled={page === 0} 
                    onClick={() => setPage(page - 1)}
                  >
                    Previous
                  </button>
                  <span className="pagination-page-label">Page {page + 1} of {Math.ceil(sortedBackups.length / ROWS_PER_PAGE)}</span>
                  <button 
                    className="btn btn-secondary py-xs px-md text-xs w-auto" 
                    disabled={(page + 1) * ROWS_PER_PAGE >= sortedBackups.length} 
                    onClick={() => setPage(page + 1)}
                  >
                    Next
                  </button>
                  <button 
                    className="btn btn-secondary py-xs px-md text-xs w-auto" 
                    onClick={() => setPage(Math.max(0, Math.ceil(sortedBackups.length / ROWS_PER_PAGE) - 1))}
                    disabled={(page + 1) * ROWS_PER_PAGE >= sortedBackups.length}
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
          <h4 className="section-title mb-xs">Server Configuration</h4>
          <p className="text-sm text-muted mb-lg">
            Current parsed server configuration with sensitive secrets obfuscated.
          </p>
          {configError ? (
            <div className="text-danger">{configError}</div>
          ) : (
            <pre className="copy-box text-xs text-main overflow-auto">
              {serverConfig || 'No configuration available.'}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
