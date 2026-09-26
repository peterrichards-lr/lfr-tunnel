import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import GeoAttribution from '../components/GeoAttribution';
import ModalShell from '../components/ModalShell';

/**
 * The shape of GET /api/admin/session-secrets (#2196).
 *
 * Mirrors visitorSessionRotationStatus in pkg/server/visitor_session_rotation_api.go. No key
 * material is on that endpoint by construction -- generation ids are labels, and the rest is
 * timestamps and node names.
 */
type SessionKeyNodeStatus = { node_id: string; why: string };
type SessionKeyRotation = {
  at: string;
  trigger: string;
  actor?: string;
  outcome: string;
  generation?: string;
  previous_generation?: string;
  acknowledged: string[];
  unacknowledged: SessionKeyNodeStatus[];
  reason?: string;
};
type SessionKeyStatus = {
  current_generation: string;
  accepted_generations: string[];
  next_rotation_at: string;
  retiring_at?: Record<string, string>;
  last_rotation?: SessionKeyRotation;
  connected_nodes: string[];
  rotation_interval: string;
  retirement_lag: string;
};

// "24h0m0s" -> "24h". Go's Duration.String() is exact and unreadable; the zero tail carries no
// information. Dropping it is the ONLY transformation applied -- the interval and the lag are
// read from the endpoint, never recomputed here, so this portal cannot promise a cadence the
// engine does not run. V1's copy is formatRotationDuration() in dashboard.js.
function formatRotationDuration(raw: string): string {
  if (!raw) return '';
  return String(raw)
    .replace(/(\d+h)0m0s$/, '$1')
    .replace(/(\d+m)0s$/, '$1');
}

function formatSessionKeyTime(value?: string): string {
  if (!value) return '';
  const d = new Date(value);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString();
}

// A zero time.Time marshals as "0001-01-01T00:00:00Z", which renders as a real date in year 1
// rather than as "not scheduled". Treated as absent.
function hasSessionKeyTime(value?: string): boolean {
  if (!value) return false;
  const d = new Date(value);
  return !isNaN(d.getTime()) && d.getUTCFullYear() > 1;
}

// Whether the fleet actually switched -- visitorSessionRotationOutcome.committed(), in the arm
// that renders it. A named helper rather than an inline comparison because the badge's className
// would otherwise contain the literal 'committed', and check-css-modifiers.cjs reads every string
// literal inside a className expression as a class name (its parsing rule 1). It would be
// reported as an undefined CSS class, which is a true statement about a string that is not one.
function rotationCommitted(r?: SessionKeyRotation): boolean {
  return r?.outcome === 'committed';
}

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
  // Whether /api/admin/system-settings actually answered. The four fields below have plausible
  // initial values -- 'round_robin', empty, false -- so a failed load renders a form that looks
  // authoritative and is not. Saving from that state would PUT those defaults over the real
  // configuration (#1868), which is a worse outcome than the failure that caused it.
  const [settingsLoaded, setSettingsLoaded] = useState(false);
  const [loadError, setLoadError] = useState('');

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

  // Email alert toggles (#1882). The vocabulary comes from the server -- `alertSettings` is
  // whatever this gateway declares, not a list maintained here -- so a gateway that adds a
  // seventh alert grows a seventh checkbox without a portal release. `alertValues` holds the
  // resolved on/off per key, and `alertsLoaded` gates saving for the same reason
  // `settingsLoaded` does: an unloaded form reads as "everything off", and posting that would
  // silently disable every alert on the gateway.
  const [alertSettings, setAlertSettings] = useState<
    { key: string; label_key: string; default_on: boolean }[]
  >([]);
  const [alertValues, setAlertValues] = useState<Record<string, boolean>>({});
  const [alertsLoaded, setAlertsLoaded] = useState(false);

  // The geo-IP vendor (#1995). A dropdown rather than a text field, so a typo becomes
  // impossible rather than merely reported -- and getting it wrong means publishing one
  // vendor's required credit over another vendor's data, which is a licence problem.
  //
  // `geoProviders` is the vocabulary THIS gateway supports, served from the endpoint like
  // `alertSettings` is, so the portal renders what the build declares instead of carrying its
  // own copy of the list. `geoSource` is which of the two configuration sources the value in
  // force came from, and it is rendered: two sources of truth disagreeing silently is the
  // failure this repo keeps hitting (#1412, #1921, #1919).
  const [geoProviders, setGeoProviders] = useState<
    {
      value: string;
      label_key: string;
      attribution_key: string;
      // The anchor the credit must carry, served per option from geo.AttributionLink (#2044).
      // Per option rather than for the vendor in force, because this screen previews the credit
      // for one the admin has SELECTED and not yet saved.
      attribution_href?: string;
      attribution_text?: string;
    }[]
  >([]);
  const [geoProvider, setGeoProvider] = useState('');
  const [geoSource, setGeoSource] = useState('');
  const [geoConfigValue, setGeoConfigValue] = useState('');
  const [geoPath, setGeoPath] = useState('');
  const [geoLoaded, setGeoLoaded] = useState(false);

  // Config view state
  const [serverConfig, setServerConfig] = useState('');
  const [configError, setConfigError] = useState('');

  const [webhookTesting, setWebhookTesting] = useState(false);

  // Broadcast state
  const [broadcastMessage, setBroadcastMessage] = useState('');
  const [broadcastSending, setBroadcastSending] = useState(false);

  // Visitor session keys (#2196). Kept in step with V1's copy in dashboard.js.
  const [sessionKeys, setSessionKeys] = useState<SessionKeyStatus | null>(null);
  // Same reasoning as settingsLoaded above: null is both "not fetched yet" and "fetch failed",
  // and a card that renders empty strings for a generation id looks like a gateway holding no
  // keys rather than like a page that could not read them.
  const [sessionKeysError, setSessionKeysError] = useState('');
  const [sessionKeysConfirming, setSessionKeysConfirming] = useState(false);
  const [sessionKeysRotating, setSessionKeysRotating] = useState(false);

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
      // Only now are the four fields the server's rather than React's initial state.
      setSettingsLoaded(true);

      const aRes = await axios.get('/api/admin/settings');
      const declared = Array.isArray(aRes.data.alert_settings)
        ? aRes.data.alert_settings
        : [];
      setAlertSettings(declared);
      setAlertValues(
        Object.fromEntries(
          declared.map((a: { key: string }) => [
            a.key,
            aRes.data[a.key] === 'true',
          ]),
        ),
      );
      // Only true when the server actually declared a vocabulary: an empty list is a
      // failure to describe itself, not a gateway with no alerts.
      setAlertsLoaded(declared.length > 0);

      const vendors = Array.isArray(aRes.data.geo_providers)
        ? aRes.data.geo_providers
        : [];
      setGeoProviders(vendors);
      setGeoProvider(aRes.data.country_db_provider || '');
      setGeoSource(aRes.data.country_db_provider_source || '');
      setGeoConfigValue(aRes.data.country_db_provider_config || '');
      setGeoPath(aRes.data.country_db_path || '');
      // Same reasoning as alertsLoaded: an empty vocabulary is a gateway that failed to
      // describe itself, and saving from that state would post a vendor nobody chose.
      setGeoLoaded(vendors.length > 0);

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
    } catch (e: any) {
      // Not console.error alone (#1868). That left setLoading(false) to run and the form to
      // render with its initial state, indistinguishable from values the server had supplied.
      // The nested config-view catch above already did this correctly; this one did not.
      console.error(e);
      setLoadError(
        e.response?.data?.error ||
          t(
            'admin_load_failed',
            'Could not load this page. The server may be unreachable — what you see is not current.',
          ),
      );
    } finally {
      setLoading(false);
    }
  };

  // Its own request, and its own error, deliberately NOT folded into fetchAllData: that
  // function's catch sets loadError for the whole page, so a gateway that answered every other
  // endpoint would render "could not load this page" because one card could not be filled.
  const fetchSessionKeys = async () => {
    try {
      const res = await axios.get('/api/admin/session-secrets');
      setSessionKeys(res.data);
      setSessionKeysError('');
    } catch (e: any) {
      console.error(e);
      // Cleared, so a rotation outcome can never be rendered beside a generation from before it.
      setSessionKeys(null);
      setSessionKeysError(
        e.response?.data?.error ||
          t(
            'session_keys_load_failed',
            'The visitor session-key status could not be read, so what you see is not current.',
          ),
      );
    }
  };

  // The manual trigger. The confirmation is the point of the two-step: a rotation is fleet-wide.
  const rotateSessionKeys = async () => {
    setSessionKeysRotating(true);
    try {
      await axios.post('/api/admin/session-secrets/rotate');
    } catch (e: any) {
      // 409 is the documented ABORT status and its body IS the outcome, reason included. It is
      // not an error to report as a toast and discard -- re-reading the status below renders
      // the abort with its reason and the nodes that did not acknowledge, which is the whole
      // reason this card exists. Anything else is a real failure and gets said out loud.
      if (e.response?.status !== 409) {
        showToast(
          e.response?.data?.error ||
            t(
              'session_keys_rotate_failed',
              'The rotation request did not reach the gateway.',
            ),
          'error',
        );
      }
    } finally {
      setSessionKeysRotating(false);
      setSessionKeysConfirming(false);
      await fetchSessionKeys();
    }
  };

  useEffect(() => {
    fetchAllData();
    fetchSessionKeys();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const saveSystemSettings = async () => {
    // Refuse rather than write defaults nobody chose. The disabled buttons below are the visible
    // half; this is the half that holds if the page is reached another way.
    if (!settingsLoaded) {
      showToast(
        t(
          'admin_load_failed',
          'Could not load this page. The server may be unreachable — what you see is not current.',
        ),
        'error',
      );
      return;
    }
    try {
      await axios.put('/api/admin/system-settings', {
        domain_allocation_rule: allocationRule,
        default_domain: defaultDomain,
        vanity_domain_hook_path: vanityHookPath,
        enable_vanity_domain_hook: enableVanityHook,
      });
      // Two endpoints, so two results. Reported separately rather than under one
      // success message: one half failing while the other succeeded is exactly the
      // state an admin must not read as "saved".
      if (alertsLoaded) {
        await axios.post('/api/admin/settings', {
          ...Object.fromEntries(
            alertSettings.map((a) => [
              a.key,
              alertValues[a.key] ? 'true' : 'false',
            ]),
          ),
          // Only when the vocabulary actually arrived. Posting '' from an unloaded form
          // would clear a vendor the operator chose, and silently stop crediting them.
          ...(geoLoaded ? { country_db_provider: geoProvider } : {}),
        });
        showToast('System settings saved successfully.', 'success');
      } else {
        showToast(
          'System settings were saved, but the email alert toggles were not.',
          'error',
        );
      }
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
      {loadError && (
        <div className="alert-banner alert-banner--danger mb-xl">
          {loadError}
        </div>
      )}

      <div className="mb-xl">
        <h1 className="page-header__title">
          {t('system_settings_title', 'System Settings')}
        </h1>
        <p className="page-header__desc">
          Configure global routing and domain parameters.
        </p>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">
          {t('domain_allocation_title', 'Domain Allocation')}
        </h4>
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
              {t('alloc_contextual', 'Contextual (Match requesting domain)')}
            </option>
            <option value="preference">
              {t(
                'alloc_preference',
                'Preference (Use configured domain list order)',
              )}
            </option>
            <option value="user-preference">
              {t(
                'alloc_user_preference',
                "User Preference (Use user's preferred domain)",
              )}
            </option>
            <option value="round-robin">
              {t(
                'alloc_round_robin',
                'Round Robin (Sequential load balancing)',
              )}
            </option>
            <option value="hashing">
              {t(
                'alloc_hashing',
                'Deterministic Hashing (Consistent for user/IP)',
              )}
            </option>
            <option value="least-connections">
              {t(
                'alloc_least_connections',
                'Least Connections (Load-based allocation)',
              )}
            </option>
            <option value="random">
              {t('alloc_random', 'Random Allocation')}
            </option>
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
        <button
          className="btn btn-primary"
          onClick={saveSystemSettings}
          disabled={!settingsLoaded}
        >
          {t('save_settings', 'Save Settings')}
        </button>
      </div>

      <div className="card mb-xl">
        <h4 className="section-title mb-lg">
          {t('vanity_hook_title', 'Vanity Domain Hook')}
        </h4>
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
            {t('vanity_hook_path', 'Vanity Domain Hook Script Path')}
          </label>
          <input
            id="field-3"
            type="text"
            className="input-field"
            value={vanityHookPath}
            onChange={(e) => setVanityHookPath(e.target.value)}
            placeholder={t(
              'vanity_hook_path_placeholder',
              '/usr/local/bin/lfr-vanity-hook.sh',
            )}
            disabled={user.role !== 'owner' || !enableVanityHook}
          />
        </div>
        <button
          className="btn btn-primary"
          onClick={saveSystemSettings}
          disabled={user.role !== 'owner' || !settingsLoaded}
        >
          {t('save_settings', 'Save Settings')}
        </button>
      </div>

      {/* Email Alerts (#1882). Rendered from the server-declared table, never from a
          copy held here -- the copy is what left three of the six alerts with no control
          at all, switchable only by writing the admin_settings row by hand. */}
      <div className="card mb-xl">
        <h4 className="section-title mb-xs">
          {t('alert_settings_title', 'Email Alerts')}
        </h4>
        <p className="text-sm text-muted mb-lg">
          {t(
            'alert_settings_desc',
            'Choose which events email the administrator address. Unchecked events still appear in the audit log.',
          )}
        </p>
        {alertsLoaded ? (
          <div className="flex flex-col gap-sm">
            {alertSettings.map((a) => (
              <label
                key={a.key}
                className="flex items-center gap-sm text-sm cursor-pointer"
              >
                <input
                  type="checkbox"
                  checked={!!alertValues[a.key]}
                  onChange={(e) =>
                    setAlertValues((prev) => ({
                      ...prev,
                      [a.key]: e.target.checked,
                    }))
                  }
                />
                <span>{t(a.label_key, a.key)}</span>
              </label>
            ))}
          </div>
        ) : (
          <div className="text-sm text-muted">
            {t(
              'admin_load_failed',
              'Could not load this page. The server may be unreachable \u2014 what you see is not current.',
            )}
          </div>
        )}
      </div>

      {/* Geo-IP Vendor (#1995). A dropdown, not a text field: the value decides whose
          licence-required credit this deployment publishes, so a typo is a licence problem
          and the right fix is to make one impossible rather than to report it. The options
          come from the gateway (geo_providers), like the alert vocabulary above, so a build
          that learns a fourth vendor grows a fourth option with no portal release. */}
      <div className="card mb-xl">
        <h4 className="section-title mb-xs">
          {t('geo_provider_title', 'Geo-IP Vendor')}
        </h4>
        <p className="text-sm text-muted mb-lg">
          {t(
            'geo_provider_desc',
            "Every supported vendor's licence requires a different visible credit, and a geo-IP database file cannot be trusted to say who published it, so the vendor has to be named. Choosing one here takes effect immediately -- no restart.",
          )}
        </p>
        {geoLoaded ? (
          <>
            <div className="form-group">
              <label className="form-label" htmlFor="geo-provider-select">
                {t('geo_provider_label', 'Database vendor')}
              </label>
              <select
                id="geo-provider-select"
                data-testid="geo-provider-select"
                className="input-field"
                value={geoProvider}
                onChange={(e) => setGeoProvider(e.target.value)}
              >
                <option value="">
                  {t(
                    'geo_provider_none',
                    '(not set) -- geographic distribution stays off',
                  )}
                </option>
                {geoProviders.map((p) => (
                  <option key={p.value} value={p.value}>
                    {t(p.label_key, p.value)}
                  </option>
                ))}
              </select>
            </div>

            {/* Which source is in force, named rather than implied. Without this an operator
                edits server-config.yaml, sees no change, and has no way to learn that a row
                set here is overriding them. */}
            <p
              className="text-xs text-muted mt-xs"
              data-testid="geo-provider-source"
            >
              {geoSource === 'portal'
                ? t(
                    'geo_provider_source_portal',
                    'In force: the vendor chosen here, which overrides country_db_provider in server-config.yaml.',
                  )
                : geoSource === 'server_config'
                  ? t(
                      'geo_provider_source_config',
                      'In force: country_db_provider in server-config.yaml, set to {0}. Choosing a vendor here overrides it.',
                    ).replace('{0}', geoConfigValue)
                  : t(
                      'geo_provider_source_unset',
                      'No vendor is set here or in server-config.yaml, so geographic distribution is off and no credit is published.',
                    )}
            </p>

            {/* A configured value this build does not recognise. Reported here as well as in
                the analytics panel, because this is the screen where it can be corrected. */}
            {!geoProvider && geoSource !== '' && (
              <div className="alert-banner alert-banner--warning mt-md text-sm">
                {t(
                  'geo_provider_unknown',
                  'country_db_provider names a vendor this gateway does not recognise, so geographic distribution is off. It must be one of maxmind, dbip or ip2location.',
                )}
              </div>
            )}

            {/* The exact credit that will be published, rendered by the SAME component the
                analytics panel uses -- a preview that can differ from the thing it previews
                is worse than no preview. Absent when no vendor is chosen: there is nobody to
                credit, and printing any vendor's line would be a false provenance claim. */}
            {geoProvider && (
              <div className="mt-lg">
                <p className="text-xs text-muted mb-xs">
                  {t(
                    'geo_provider_credit_label',
                    'This credit will be published wherever geographic distribution is shown:',
                  )}
                </p>
                <p
                  data-testid="geo-provider-credit"
                  className="text-sm m-0 p-md copy-box"
                >
                  <GeoAttribution
                    provider={geoProvider}
                    href={
                      geoProviders.find((p) => p.value === geoProvider)
                        ?.attribution_href
                    }
                    text={
                      geoProviders.find((p) => p.value === geoProvider)
                        ?.attribution_text
                    }
                  />
                </p>
              </div>
            )}

            {/* country_db_path, read-only and deliberately so: it is a filesystem path on the
                gateway host, read at startup, and letting an admin session point the gateway
                at an arbitrary readable path is a file-disclosure vector. Shown because a
                wrong path is the other half of diagnosing an empty panel. */}
            <div className="mt-lg">
              <p className="text-xs text-muted mb-xs">
                {t('geo_provider_path_label', 'Database file')}
              </p>
              <p className="text-sm m-0" data-testid="geo-provider-path">
                {geoPath || (
                  <span className="text-muted">
                    {t(
                      'geo_provider_path_unset',
                      'No geo-IP database file is configured.',
                    )}
                  </span>
                )}
              </p>
              <p className="text-xs text-muted mt-xs m-0">
                {t(
                  'geo_provider_path_note',
                  'country_db_path is set in server-config.yaml only. It is a path on the gateway host and is read at startup, so it is shown here but cannot be changed from the portal.',
                )}
              </p>
            </div>

            <button
              className="btn btn-primary mt-lg"
              onClick={saveSystemSettings}
              disabled={!settingsLoaded}
            >
              {t('save_settings', 'Save Settings')}
            </button>
          </>
        ) : (
          <div className="text-sm text-muted">
            {t(
              'admin_load_failed',
              'Could not load this page. The server may be unreachable \u2014 what you see is not current.',
            )}
          </div>
        )}
      </div>

      {/* Visitor Session Keys (#2196) -- the portal half of #2184, and V1 carries the same card
          in dashboard.html. Everything shown here, the rotation interval and the retirement lag
          included, comes from GET /api/admin/session-secrets: the endpoint states them precisely
          so neither arm keeps a second copy to drift against the engine.

          The last-rotation block is why the card exists. Without it a rotation that keeps
          aborting and one that works look identical, which is the failure pattern this repo
          keeps re-finding -- so the outcome, its trigger, the abort reason and the nodes that
          did not acknowledge are each rendered rather than collapsed into "failed". */}
      <div className="card mb-xl" data-testid="session-keys-card">
        <h4 className="section-title mb-xs">
          {t('session_keys_title', 'Visitor Session Keys')}
        </h4>
        <p className="text-sm text-muted mb-lg">
          {t(
            'session_keys_desc',
            "A visitor entering a tunnel's passcode is given a signed cookie so they are not asked again. Every node signs with the current generation and goes on verifying the generations being retired, so rotating the key does not sign anybody out.",
          )}
        </p>
        {sessionKeys ? (
          <>
            <div className="flex gap-md flex-wrap items-center mb-sm">
              <span className="text-xs text-muted">
                {t('session_keys_current', 'Current generation')}
              </span>
              <code className="text-sm" data-testid="session-keys-current">
                {sessionKeys.current_generation}
              </code>
            </div>
            <div className="flex gap-md flex-wrap items-center mb-sm">
              <span className="text-xs text-muted">
                {t('session_keys_accepted', 'Still accepted')}
              </span>
              <span className="text-sm" data-testid="session-keys-accepted">
                {(sessionKeys.accepted_generations || [])
                  .map((g) =>
                    hasSessionKeyTime(sessionKeys.retiring_at?.[g])
                      ? `${g} (${t(
                          'session_keys_retires',
                          'retires {0}',
                        ).replace(
                          '{0}',
                          formatSessionKeyTime(sessionKeys.retiring_at?.[g]),
                        )})`
                      : g,
                  )
                  .join(', ')}
              </span>
            </div>
            <div className="flex gap-md flex-wrap items-center mb-sm">
              <span className="text-xs text-muted">
                {t('session_keys_next_rotation', 'Next rotation due')}
              </span>
              <span className="text-sm" data-testid="session-keys-next">
                {hasSessionKeyTime(sessionKeys.next_rotation_at)
                  ? formatSessionKeyTime(sessionKeys.next_rotation_at)
                  : t('session_keys_next_unscheduled', 'Not scheduled yet')}
              </span>
            </div>
            <p className="text-xs text-muted mb-lg">
              {t(
                'session_keys_interval_note',
                'Rotations run every {0}. A replaced generation goes on verifying for {1} afterwards, which is longer than a visitor cookie lives, so no session ends because of a rotation.',
              )
                .replace(
                  '{0}',
                  formatRotationDuration(sessionKeys.rotation_interval),
                )
                .replace(
                  '{1}',
                  formatRotationDuration(sessionKeys.retirement_lag),
                )}
            </p>
            <div className="flex gap-md flex-wrap items-center mb-sm">
              <span className="text-xs text-muted">
                {t('session_keys_connected', 'Nodes that must acknowledge')}
              </span>
              <span className="text-sm" data-testid="session-keys-connected">
                {(sessionKeys.connected_nodes || []).length > 0
                  ? sessionKeys.connected_nodes.join(', ')
                  : t(
                      'session_keys_connected_none',
                      'None connected right now',
                    )}
              </span>
            </div>
            {/* Said out loud rather than left to be inferred. A commit is gated on connected
                nodes, never on configured ones, so an operator who knows edge-us and edge-sa
                power off overnight would otherwise reasonably assume a rotation is waiting for
                them. */}
            <p className="text-xs text-muted mb-lg">
              {t(
                'session_keys_connected_note',
                'Only the nodes connected right now have to acknowledge. An edge that is powered off is not a reason a rotation is waiting -- it is handed the current keys when it reconnects.',
              )}
            </p>

            <p className="text-xs text-muted mb-xs">
              {t('session_keys_last', 'Last rotation')}
            </p>
            <div
              className="copy-box p-md"
              data-testid="session-keys-last-rotation"
            >
              {sessionKeys.last_rotation ? (
                <>
                  <div className="flex gap-sm items-center flex-wrap mb-sm">
                    <span
                      className={
                        rotationCommitted(sessionKeys.last_rotation)
                          ? 'badge badge-success'
                          : 'badge badge-danger'
                      }
                      data-testid="session-keys-last-outcome"
                    >
                      {rotationCommitted(sessionKeys.last_rotation)
                        ? t('session_keys_outcome_committed', 'Committed')
                        : t('session_keys_outcome_aborted', 'Aborted')}
                    </span>
                    {/* Manual or periodic, always. A periodic rotation that aborts is a system
                        fault; a manual one that aborts is probably somebody watching the
                        screen, and the two call for different responses. */}
                    <span
                      className="text-sm"
                      data-testid="session-keys-last-trigger"
                    >
                      {sessionKeys.last_rotation.trigger === 'manual'
                        ? t('session_keys_trigger_manual', 'Manual')
                        : t('session_keys_trigger_periodic', 'Periodic')}
                    </span>
                    <span className="text-xs text-muted">
                      {formatSessionKeyTime(sessionKeys.last_rotation.at)}
                    </span>
                    {sessionKeys.last_rotation.actor && (
                      <span className="text-xs text-muted">
                        {t('session_keys_actor', 'triggered by {0}').replace(
                          '{0}',
                          sessionKeys.last_rotation.actor,
                        )}
                      </span>
                    )}
                  </div>
                  {rotationCommitted(sessionKeys.last_rotation) &&
                    sessionKeys.last_rotation.generation && (
                      <p className="text-sm mb-sm">
                        {t(
                          'session_keys_moved_to',
                          'Now minting with generation {0}.',
                        ).replace('{0}', sessionKeys.last_rotation.generation)}
                      </p>
                    )}
                  {sessionKeys.last_rotation.reason && (
                    <p
                      className="text-sm mb-sm"
                      data-testid="session-keys-reason"
                    >
                      <strong>
                        {t('session_keys_reason', 'Why it stopped')}:{' '}
                      </strong>
                      {sessionKeys.last_rotation.reason}
                    </p>
                  )}
                  <div data-testid="session-keys-unacked">
                    {(sessionKeys.last_rotation.unacknowledged || []).length >
                      0 && (
                      <>
                        <p className="text-xs text-muted mb-xs">
                          {t('session_keys_unacked', 'Did not acknowledge')}
                        </p>
                        <ul className="text-sm m-0">
                          {sessionKeys.last_rotation.unacknowledged.map((n) => (
                            <li key={n.node_id}>
                              {n.why ? `${n.node_id} — ${n.why}` : n.node_id}
                            </li>
                          ))}
                        </ul>
                      </>
                    )}
                  </div>
                  {(sessionKeys.last_rotation.acknowledged || []).length >
                    0 && (
                    <p
                      className="text-xs text-muted mt-sm mb-0"
                      data-testid="session-keys-acked"
                    >
                      {t('session_keys_acked', 'Acknowledged')}:{' '}
                      {sessionKeys.last_rotation.acknowledged.join(', ')}
                    </p>
                  )}
                </>
              ) : (
                <span className="text-sm text-muted">
                  {t(
                    'session_keys_last_none',
                    'No rotation has run on this control plane yet.',
                  )}
                </span>
              )}
            </div>

            <button
              className="btn btn-primary mt-lg"
              data-testid="session-keys-rotate"
              disabled={sessionKeysRotating}
              onClick={() => setSessionKeysConfirming(true)}
            >
              {sessionKeysRotating
                ? t('session_keys_rotating', 'Rotating...')
                : t('session_keys_rotate', 'Rotate keys now')}
            </button>
          </>
        ) : (
          <div className="text-sm text-muted" data-testid="session-keys-error">
            {sessionKeysError ||
              t(
                'session_keys_load_failed',
                'The visitor session-key status could not be read, so what you see is not current.',
              )}
          </div>
        )}
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

      {/* The confirmation step (#2196). A rotation is fleet-wide, and although it fails closed
          rather than dangerously, it is not something to set off by mis-clicking. V1 asks the
          same two questions through confirm(), which is that arm's convention. */}
      <ModalShell
        isOpen={sessionKeysConfirming}
        onClose={() => setSessionKeysConfirming(false)}
        labelledBy="session-keys-confirm-title"
        cardClassName="modal-card modal-card--sm"
      >
        <div className="modal-header">
          <h3 id="session-keys-confirm-title" className="modal-title">
            {t(
              'session_keys_confirm_title',
              'Rotate the visitor session keys?',
            )}
          </h3>
        </div>
        <p className="text-sm text-muted">
          {t(
            'session_keys_confirm_body',
            'This is fleet-wide. Every connected node must confirm it holds the new generation before anything switches; if one does not, nothing changes and the attempt is recorded as aborted. No visitor is signed out either way.',
          )}
        </p>
        <div className="flex gap-md justify-end mt-lg">
          <button
            type="button"
            className="btn btn-secondary w-auto"
            onClick={() => setSessionKeysConfirming(false)}
          >
            {t('cancel', 'Cancel')}
          </button>
          <button
            type="button"
            className="btn btn-primary w-auto"
            data-testid="session-keys-rotate-confirm"
            disabled={sessionKeysRotating}
            onClick={rotateSessionKeys}
          >
            {t('confirm', 'Confirm')}
          </button>
        </div>
      </ModalShell>
    </div>
  );
}
