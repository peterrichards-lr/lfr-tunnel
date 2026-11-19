import { useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';
import { useUI } from '../contexts/UIContext';
import { parseRateLimit } from '../rateLimit';
import ModalShell from './ModalShell';

// The fields the telemetry payload carries for one lease. Named exactly as the server sends
// them so a caller can hand over what it already has rather than translating field by field --
// which is how the access-control fields came to be absent from V2 in the first place: they
// have been in this payload since #1329 and no component ever read them.
export interface TunnelDetailFields {
  subdomain_prefix: string;
  full_host?: string;
  status?: string;
  node_id?: string;
  client_ip?: string;
  created_at?: string;
  rate_limit?: number;
  user_id?: string;
  bytes_in?: number;
  bytes_out?: number;
  visitor_ips?: string[];
  // "********" when a passcode is set, "" when it is not. The server masks it at the source
  // (#2101, #2135) -- the stored value is a bcrypt hash, so there is nothing here to render
  // even if a component wanted to.
  passcode?: string;
  whitelist_ips?: string;
  access_mode?: string;
  // Names only, never values (#2148, #2150).
  launch_flags?: string[];
  launch_overrides?: Record<string, string>;
  added_header_names?: string[];
}

interface Props {
  // The client session, with per-port aggregates already summed by the caller's grouping.
  tunnel: TunnelDetailFields;
  // One entry per port. A single-port client passes one; the per-port table is then simply a
  // one-row table rather than a special case.
  members: TunnelDetailFields[];
  isAdmin?: boolean;
  onKick?: () => void;
  // Re-reads whatever the tunnel came from. Shown as a Refresh button when given, and called
  // after an edit so the panel shows the result rather than what was there before.
  onRefresh?: () => void | Promise<void>;
  onClose: () => void;
}

// The error text the gateway gave, or a generic one. The handlers answer {"error": "..."}.
function errorText(e: any): string {
  return e?.response?.data?.error || e?.message || 'Unknown error';
}

function formatBytes(bytes?: number): string {
  if (bytes === undefined || bytes === null) return '—';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

/**
 * Per-tunnel detail view for V2 (#2150).
 *
 * ## Why this exists
 *
 * The two portals are an A/B test, so the same person may be served either arm and a
 * capability present in only one is a defect rather than a roadmap item. V1 has carried a
 * tunnel Details modal throughout; V2 had a table of eight columns and no per-tunnel view at
 * all, which meant a V2 user could not see:
 *
 *   - what access control a tunnel is under (mode, passcode, IP whitelist)
 *   - which visitors are currently connected to it
 *   - whether anything is being injected into its requests
 *   - how the client was launched
 *
 * Those are not fields presented differently in the two arms. They were absent from one of
 * them. The gap survived because each arm was only ever compared against itself.
 *
 * ## The writes (#2525)
 *
 * Custom headers can be added and removed, and an admin can override the rate limit -- what
 * V1's modal always offered. Both go to every port of the client, because a client's -header
 * and its rate limit apply to all of its ports alike. A header edit sends the CHANGE (set one,
 * remove one), never a rebuilt map: this panel only ever has header names, and the gateway
 * holding the lease applies the change to the values it has (#2530).
 *
 * ## The rule this panel must not break
 *
 * Every secret-shaped field here is already reduced by the server: the passcode to a mask,
 * custom headers to their names, launch context to flag names. That is deliberate and it is
 * recent -- this same feed published session tokens and Basic Auth credentials to every admin
 * until #2137. A panel is exactly where that gets undone, by someone reading a richer field
 * because it renders more nicely. Render what the payload gives; do not ask for more.
 */
export default function TunnelDetailsModal({
  tunnel,
  members,
  isAdmin,
  onKick,
  onRefresh,
  onClose,
}: Props) {
  const { t } = useI18n();
  const { formatDate } = useSettings();
  const { showToast, showPrompt } = useUI();
  const [headerName, setHeaderName] = useState('');
  const [headerValue, setHeaderValue] = useState('');
  const [busy, setBusy] = useState(false);

  // Every port of the client. The group row blanks full_host when there are several, so the
  // members are the source of truth for which hosts exist.
  const hosts = members
    .map((m) => m.full_host || '')
    .filter((h) => h.includes('.'));

  const editHeaders = async (change: {
    set?: Record<string, string>;
    remove?: string[];
  }) => {
    setBusy(true);
    try {
      await Promise.all(
        hosts.map((host) => {
          const dot = host.indexOf('.');
          return axios.post('/api/portal/reservations/headers', {
            subdomain: host.slice(0, dot),
            domain: host.slice(dot + 1),
            ...change,
          });
        }),
      );
      showToast(t('headers_updated', 'Custom headers updated.'), 'success');
      await onRefresh?.();
      return true;
    } catch (e) {
      showToast(
        t('headers_update_failed', 'Failed to update headers: {0}').replace(
          '{0}',
          errorText(e),
        ),
        'error',
      );
      return false;
    } finally {
      setBusy(false);
    }
  };

  const addHeader = async () => {
    const name = headerName.trim();
    const value = headerValue.trim();
    if (!name || !value) {
      showToast(
        t('header_name_value_required', 'Header name and value are required.'),
        'error',
      );
      return;
    }
    if (await editHeaders({ set: { [name]: value } })) {
      setHeaderName('');
      setHeaderValue('');
    }
  };

  const overrideRateLimit = async () => {
    const input = await showPrompt(
      t('tunnel_override_modal_title', 'Override Tunnel Rate Limit'),
      t('lbl_live_rps_override', 'Live Rate Limit Override (RPS)'),
      String(tunnel.rate_limit || 0),
    );
    if (input === null) return;
    const limit = parseRateLimit(input);
    if (limit === null) {
      showToast(
        t(
          'rate_limit_invalid',
          'Enter a whole number of requests per second (0 removes the limit).',
        ),
        'error',
      );
      return;
    }
    setBusy(true);
    try {
      await Promise.all(
        hosts.map((host) =>
          axios.post('/api/admin/leases/rate-limit', {
            host,
            rate_limit: limit,
          }),
        ),
      );
      showToast(t('rate_limit_updated', 'Rate limit updated.'), 'success');
      await onRefresh?.();
    } catch (e) {
      showToast(
        t(
          'rate_limit_update_failed',
          'Failed to update rate limit: {0}',
        ).replace('{0}', errorText(e)),
        'error',
      );
    } finally {
      setBusy(false);
    }
  };

  const copyUrl = async () => {
    try {
      await navigator.clipboard.writeText(`https://${hosts[0]}`);
      showToast(t('link_copied', 'Link copied'), 'success');
    } catch {
      showToast(t('copy_failed', 'Could not copy to the clipboard.'), 'error');
    }
  };

  const row = (label: string, value: React.ReactNode) => (
    <div className="flex justify-between gap-lg py-xs border-b text-sm">
      <span className="text-muted">{label}</span>
      <span className="td-cell--mono text-right">{value}</span>
    </div>
  );

  const sectionTitle = (label: string) => (
    <div className="mt-md mb-xs text-2xs text-muted uppercase tracking-wider">
      {label}
    </div>
  );

  // Visitors are per-lease, so a multi-port client's are spread across its members. Summed and
  // de-duplicated: the same visitor hitting two ports of one client is one visitor, and showing
  // it twice would overstate the number an operator is looking at.
  const visitorIPs = Array.from(
    new Set(members.flatMap((m) => m.visitor_ips || [])),
  ).sort();

  const passcodeSet = Boolean(tunnel.passcode);
  const whitelist = tunnel.whitelist_ips || '';
  // "or" is the server's default when a reservation names no mode (see AccessControls), so an
  // empty string here means or, not "unset". Shown as the effective mode rather than blank,
  // because a blank next to a configured passcode reads as "no rule applies".
  const accessMode = (tunnel.access_mode || 'or').toUpperCase();

  const launchFlags = tunnel.launch_flags || [];
  const launchOverrides = tunnel.launch_overrides || {};
  const overrideKeys = Object.keys(launchOverrides).sort();
  const headerNames = tunnel.added_header_names || [];

  const none = <span className="text-muted italic">{t('none', 'None')}</span>;

  return (
    <ModalShell
      isOpen
      onClose={onClose}
      labelledBy="tunnel-details-modal-title"
      cardClassName="modal-card modal-card--md"
    >
      <div className="modal-header mb-md">
        <h2 id="tunnel-details-modal-title" className="modal-title text-md">
          {tunnel.subdomain_prefix}
        </h2>
        <button
          type="button"
          onClick={onClose}
          className="modal-close"
          aria-label={t('close', 'Close')}
        >
          ×
        </button>
      </div>

      {row(
        t('status', 'Status'),
        <span className="badge badge-success">
          {(tunnel.status || 'up').toUpperCase()}
        </span>,
      )}
      {row(t('node', 'Node'), tunnel.node_id || 'control')}
      {row(t('client_ip', 'Client IP'), tunnel.client_ip || '—')}
      {row(
        t('detail_lbl_connected_at', 'Connected At'),
        tunnel.created_at ? formatDate(tunnel.created_at) : '—',
      )}
      {row(
        t('detail_lbl_rate_limit', 'Rate Limit'),
        <span className="flex items-center justify-end gap-sm">
          <span data-testid="tunnel-rate-limit">
            {tunnel.rate_limit
              ? `${tunnel.rate_limit} RPS`
              : t('unlimited', 'Unlimited')}
          </span>
          {/* Admin only: the override endpoint is an admin route, and an owner who wants a
              different limit asks for one rather than lifting their own. */}
          {isAdmin && hosts.length > 0 && (
            <button
              type="button"
              className="btn btn-outline py-xs px-md text-xs w-auto m-0"
              onClick={overrideRateLimit}
              disabled={busy}
            >
              {t('btn_override', 'Override')}
            </button>
          )}
        </span>,
      )}
      {row(t('bytes_in', '↓ In'), formatBytes(tunnel.bytes_in))}
      {row(t('bytes_out', '↑ Out'), formatBytes(tunnel.bytes_out))}
      {isAdmin &&
        row(t('detail_lbl_owner', 'Owner (User ID)'), tunnel.user_id || '—')}

      {sectionTitle(t('detail_lbl_access_control', 'Access Control'))}
      {row(
        t('detail_lbl_passcode_protection', 'Passcode Protection'),
        passcodeSet ? (
          <span className="badge badge-success">{t('enabled', 'Enabled')}</span>
        ) : (
          <span className="text-muted italic">
            {t('disabled_public', 'Disabled (Public)')}
          </span>
        ),
      )}
      {row(t('detail_lbl_ip_whitelist', 'IP Whitelist'), whitelist || none)}
      {/* The combinator only decides anything when BOTH a passcode and a whitelist exist; with
          one rule it is noise, and with neither it invites the reading that some mode is being
          enforced on an open tunnel. */}
      {passcodeSet &&
        whitelist &&
        row(t('detail_lbl_combinator_mode', 'Combinator Mode'), accessMode)}

      {sectionTitle(t('tunnel_ports', 'Ports'))}
      {members.map((m, i) => (
        <div
          key={`${m.full_host || ''}|${i}`}
          className="flex justify-between gap-lg py-xs border-b text-sm"
        >
          <a
            href={`https://${m.full_host}`}
            target="_blank"
            rel="noreferrer"
            className="text-primary no-underline fw-medium"
          >
            {m.full_host}
          </a>
          <span className="td-cell--mono text-right text-muted text-xs">
            ↓ {formatBytes(m.bytes_in)} · ↑ {formatBytes(m.bytes_out)}
          </span>
        </div>
      ))}

      {sectionTitle(t('detail_lbl_active_visitor_ips', 'Active Visitor IPs'))}
      {visitorIPs.length === 0 ? (
        <div className="py-xs text-sm text-muted italic">
          {t('detail_visitor_none', 'No active visitor connections (last 30s)')}
        </div>
      ) : (
        visitorIPs.map((ip) => (
          <div
            key={ip}
            className="flex justify-between gap-lg py-xs border-b text-sm"
          >
            <span className="td-cell--mono">{ip}</span>
          </div>
        ))
      )}

      {sectionTitle(t('detail_lbl_custom_headers', 'Custom Headers Injection'))}
      {headerNames.length === 0 ? (
        <div className="py-xs text-sm text-muted italic">
          {t('no_custom_headers', 'No custom headers injected.')}
        </div>
      ) : (
        // Names, no values. See the class comment: the value is the user's and is routinely a
        // credential, and this payload reaches every admin rather than only the tunnel's owner.
        headerNames.map((name) => (
          <div
            key={name}
            className="flex justify-between items-center gap-lg py-xs border-b text-sm"
          >
            <span className="td-cell--mono">{name}</span>
            <span className="flex items-center gap-sm">
              <span className="text-muted italic text-xs">
                {t('value_hidden', 'value hidden')}
              </span>
              {hosts.length > 0 && (
                <button
                  type="button"
                  className="btn btn-outline py-xs px-md text-xs w-auto m-0 text-danger"
                  onClick={() => editHeaders({ remove: [name] })}
                  disabled={busy}
                  aria-label={t('remove_header_named', 'Remove {0}').replace(
                    '{0}',
                    name,
                  )}
                >
                  {t('btn_remove', 'Remove')}
                </button>
              )}
            </span>
          </div>
        ))
      )}
      {hosts.length > 0 && (
        <div className="flex gap-sm mt-sm">
          <input
            type="text"
            className="input-field flex-1"
            value={headerName}
            onChange={(e) => setHeaderName(e.target.value)}
            placeholder={t(
              'detail_header_key_placeholder',
              'Header Name (e.g. X-Frame-Options)',
            )}
            aria-label={t(
              'detail_header_key_placeholder',
              'Header Name (e.g. X-Frame-Options)',
            )}
            data-testid="header-name-input"
          />
          <input
            type="text"
            className="input-field flex-1"
            value={headerValue}
            onChange={(e) => setHeaderValue(e.target.value)}
            placeholder={t('detail_header_value_placeholder', 'Value')}
            aria-label={t('aria_header_value', 'Custom header value')}
            data-testid="header-value-input"
          />
          <button
            type="button"
            className="btn btn-primary w-auto"
            onClick={addHeader}
            disabled={busy}
          >
            {t('btn_add', 'Add')}
          </button>
        </div>
      )}

      {sectionTitle(t('detail_lbl_launched_with', 'Launched With'))}
      {launchFlags.length === 0 && overrideKeys.length === 0 ? (
        // Absent for two different reasons, and the difference matters to whoever is looking: a
        // client older than #2148 never sends this, and a client started with no flags has
        // nothing to send. Neither is an error, so this says "not reported" rather than
        // implying the tunnel was launched bare.
        <div className="py-xs text-sm text-muted italic">
          {t('launch_not_reported', 'Not reported by this client.')}
        </div>
      ) : (
        <>
          {launchFlags.length > 0 &&
            row(t('flags_given', 'Flags given'), launchFlags.join(' '))}
          {overrideKeys.length > 0 && (
            <div className="mt-sm mb-xs text-2xs text-muted">
              {t('launch_settings_claimed', 'Settings claimed at launch')}
            </div>
          )}
          {overrideKeys.map((key) => row(key, launchOverrides[key]))}
        </>
      )}

      <div className="flex justify-end gap-md mt-lg">
        {hosts.length > 0 && (
          <button
            type="button"
            className="btn btn-secondary w-auto"
            onClick={copyUrl}
          >
            {t('btn_copy_url', 'Copy URL')}
          </button>
        )}
        {onRefresh && (
          <button
            type="button"
            className="btn btn-secondary w-auto"
            onClick={() => onRefresh()}
            disabled={busy}
          >
            {t('btn_refresh', 'Refresh')}
          </button>
        )}
        {isAdmin && onKick && (
          <button
            type="button"
            className="btn btn-danger w-auto"
            onClick={onKick}
          >
            {t('kick', 'Kick')}
          </button>
        )}
        <button type="button" className="btn btn-secondary" onClick={onClose}>
          {t('close', 'Close')}
        </button>
      </div>
    </ModalShell>
  );
}
