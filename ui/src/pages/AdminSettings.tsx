import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
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
  const { t } = useI18n();
  const { showToast } = useUI();
  const [loading, setLoading] = useState(true);

  // System Settings state
  const [allocationRule, setAllocationRule] = useState('round_robin');
  const [defaultDomain, setDefaultDomain] = useState('');
  const [supportedDomains, setSupportedDomains] = useState<string[]>([]);
  const [vanityHookPath, setVanityHookPath] = useState('');
  const [enableVanityHook, setEnableVanityHook] = useState(false);

  // Not maintenance control -- that lives on its own page (#1599). This is here only for
  // test_target, the webhook destination shown beside the Integrations test button (#1290),
  // which the maintenance endpoint happens to return. Narrowed to that one field so it does not
  // read as a second copy of the maintenance state.
  const [maintenance, setMaintenance] = useState<{ test_target?: string }>({});

  // Config view state
  const [serverConfig, setServerConfig] = useState('');
  const [configError, setConfigError] = useState('');

  const [webhookTesting, setWebhookTesting] = useState(false);

  // Broadcast state
  const [broadcastMessage, setBroadcastMessage] = useState('');
  const [broadcastSending, setBroadcastSending] = useState(false);

  // Backups state

  const fetchAllData = async () => {
    try {
      const vRes = await axios.get('/api/version');
      setSupportedDomains(vRes.data.supported_domains || []);

      const sRes = await axios.get('/api/admin/system-settings');
      setAllocationRule(sRes.data.domain_allocation_rule || 'round_robin');
      setDefaultDomain(sRes.data.default_domain || '');
      setVanityHookPath(sRes.data.vanity_domain_hook_path || '');
      setEnableVanityHook(!!sRes.data.enable_vanity_domain_hook);

      // Fetched for test_target alone; see the state declaration above.
      const mRes = await axios.get('/api/admin/maintenance');
      setMaintenance(mRes.data);

      if (user.role === 'owner' || user.role === 'admin') {
        try {
          const cRes = await axios.get('/api/admin/config-view');
          setServerConfig(objectToYAML(cRes.data));
        } catch (e: any) {
          setConfigError(
            e.response?.status === 403
              ? 'Not authorized to view config'
              : 'Failed to load configuration',
          );
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
        default_domain: defaultDomain,
        vanity_domain_hook_path: vanityHookPath,
        enable_vanity_domain_hook: enableVanityHook,
      });
      showToast('System settings saved successfully.', 'success');
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to save settings.', 'error');
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
      showToast(
        e.response?.data?.error || 'Failed to send broadcast.',
        'error',
      );
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
      showToast(
        e.response?.data?.error || 'Failed to clear broadcast.',
        'error',
      );
    } finally {
      setBroadcastSending(false);
    }
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
        <p className="page-header__desc">
          Configure global routing and domain parameters.
        </p>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">Domain Allocation</h4>
        <div className="form-group mt-lg">
          <label className="form-label" htmlFor="field">
            Allocation Rule
          </label>
          <select
            id="field"
            className="input-field"
            value={allocationRule}
            onChange={(e) => setAllocationRule(e.target.value)}
          >
            <option value="contextual">
              Contextual (Match requesting domain)
            </option>
            <option value="preference">
              Preference (Use configured domain list order)
            </option>
            <option value="user-preference">
              User Preference (Use user's preferred domain)
            </option>
            <option value="round-robin">
              Round Robin (Sequential load balancing)
            </option>
            <option value="hashing">
              Deterministic Hashing (Consistent for user/IP)
            </option>
            <option value="least-connections">
              Least Connections (Load-based allocation)
            </option>
            <option value="random">Random Allocation</option>
          </select>
        </div>
        <div className="form-group">
          <label className="form-label" htmlFor="field-2">
            Default Domain
          </label>
          <select
            id="field-2"
            className="input-field"
            value={defaultDomain}
            onChange={(e) => setDefaultDomain(e.target.value)}
          >
            <option value="">None (Force Error if Contextual Fails)</option>
            {supportedDomains.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </div>
        <button className="btn btn-primary" onClick={saveSystemSettings}>
          Save Settings
        </button>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">Vanity Domain Hook</h4>
        {user.role !== 'owner' && (
          <div className="alert-banner alert-banner--warning mb-lg text-sm m-0">
            ⚠️ Only the System Owner is authorized to modify vanity domain hook
            configurations.
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
            <span className="form-label m-0">
              Enable Automated DNS/TLS Provisioning
            </span>
          </label>
          <p className="text-muted text-xs mt-xs m-0">
            When active, registering a custom domain (via the client{' '}
            <code>-domain</code> flag) runs the specified hook script to
            automate local Nginx reverse proxy configuration and Certbot SSL/TLS
            certificate registration.
          </p>
        </div>
        <div className="form-group">
          <label className="form-label" htmlFor="field-3">
            Vanity Domain Hook Script Path
          </label>
          <input
            id="field-3"
            type="text"
            className="input-field"
            value={vanityHookPath}
            onChange={(e) => setVanityHookPath(e.target.value)}
            placeholder="/usr/local/bin/lfr-vanity-hook.sh"
            disabled={user.role !== 'owner' || !enableVanityHook}
          />
        </div>
        <button
          className="btn btn-primary"
          onClick={saveSystemSettings}
          disabled={user.role !== 'owner'}
        >
          Save Settings
        </button>
      </div>

      <div className="card mb-xl">
        <div className="flex justify-between items-center">
          <div>
            <h4 className="m-0">Integrations</h4>
            <div className="text-sm text-muted mt-xs">
              Test your configured webhooks (Slack/Teams).
            </div>
          </div>
          <button
            className="btn btn-primary"
            disabled={webhookTesting}
            onClick={testWebhook}
          >
            {webhookTesting ? 'Sending...' : 'Trigger Test Webhook'}
          </button>
        </div>
        {/* Where the test alert actually goes. V1 shows this beside the same button
            (dashboard.html:907); the value already arrives on /api/admin/maintenance as
            test_target (server.go:3201) and V2 simply was not displaying it. Without it
            the button reports success without saying what it reached (#1290). */}
        {maintenance?.test_target && (
          <div className="text-sm text-muted mt-md">
            Active Target:{' '}
            <strong className="text-main">{maintenance.test_target}</strong>
          </div>
        )}
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
            placeholder={t(
              'enter_broadcast_message_placeholder',
              'Enter broadcast message...',
            )}
            value={broadcastMessage}
            onChange={(e) => setBroadcastMessage(e.target.value)}
            aria-label={t('broadcast_message', 'Broadcast message')}
          />
        </div>
        <div className="flex gap-sm mt-lg">
          <button
            className="btn btn-primary"
            disabled={broadcastSending || !broadcastMessage.trim()}
            onClick={sendBroadcast}
          >
            {broadcastSending ? 'Sending...' : 'Send Broadcast'}
          </button>
          <button
            className="btn btn-secondary"
            disabled={broadcastSending}
            onClick={clearBroadcast}
          >
            Clear Broadcast
          </button>
        </div>
      </div>

      {(user.role === 'owner' || user.role === 'admin') && (
        <div className="card" id="card-server-config">
          <h4 className="section-title mb-xs">Server Configuration</h4>
          <p className="text-sm text-muted mb-lg">
            Current parsed server configuration with sensitive secrets
            obfuscated.
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
