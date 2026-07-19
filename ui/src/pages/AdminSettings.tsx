import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';

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
      }
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

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

  if (loading) return <div>Loading settings...</div>;

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
          <select className="form-control" value={allocationRule} onChange={(e) => setAllocationRule(e.target.value)}>
            <option value="round_robin">Round Robin</option>
            <option value="least_connections">Least Connections</option>
            <option value="consistent_hashing">Consistent Hashing</option>
            <option value="random">Random</option>
          </select>
        </div>
        <div className="form-group">
          <label>Default Domain</label>
          <select className="form-control" value={defaultDomain} onChange={(e) => setDefaultDomain(e.target.value)}>
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
