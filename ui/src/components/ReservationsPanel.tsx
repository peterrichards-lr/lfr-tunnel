import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import { useI18n } from '../contexts/I18nContext';
import Skeleton from './Skeleton';
import { useUI } from '../contexts/UIContext';
import ReservationsTable from './ReservationsTable';
import SectionHeading from './SectionHeading';
import ModalShell from './ModalShell';

interface Reservation {
  id: string;
  subdomain: string;
  domain: string;
  status: string;
  created_at?: string;
  expires_at?: string;
  extension_requested?: boolean;
  access_mode?: string;
  passcode?: string;
  whitelist_ips?: string;
}

// Which factors a mode actually applies (#2155).
//
// The same vocabulary the server enforces in missingAccessControlValue (#2156) and the same
// table the Inspector and V1 use. A mode that does not use a field leaves it unsettable; a mode
// that does use one refuses to save without it.
function accessControlFieldsForMode(mode: string): {
  passcode: boolean;
  whitelist: boolean;
} {
  switch (mode) {
    case 'passcode':
      return { passcode: true, whitelist: false };
    case 'whitelist':
      return { passcode: false, whitelist: true };
    case 'or':
    case 'and':
      return { passcode: true, whitelist: true };
    case 'public':
    default:
      return { passcode: false, whitelist: false };
  }
}

export default function ReservationsPanel() {
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();
  const [reservations, setReservations] = useState<Reservation[]>([]);
  const { formatDate } = useSettings();
  const [limit, setLimit] = useState(0);
  const [used, setUsed] = useState(0);
  const [customDomainLimit, setCustomDomainLimit] = useState(0);
  const [customDomainUsed, setCustomDomainUsed] = useState(0);
  const [loading, setLoading] = useState(true);

  const [domains, setDomains] = useState<string[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [subdomainInput, setSubdomainInput] = useState('');
  const [customDomainInput, setCustomDomainInput] = useState('');
  const [customDomainSubmitting, setCustomDomainSubmitting] = useState(false);
  const [subdomainStyle, setSubdomainStyle] = useState('liferay');
  const [styleInitialized, setStyleInitialized] = useState(false);

  const fetchData = async () => {
    try {
      const vRes = await axios.get('/api/version');
      setDomains(vRes.data.supported_domains || []);
      if (vRes.data.supported_domains?.length > 0 && !selectedDomain) {
        setSelectedDomain(vRes.data.supported_domains[0]);
      }

      const res = await axios.get('/api/portal/reservations');
      setReservations(res.data.reservations || []);
      setLimit(res.data.limit || 0);
      setUsed(res.data.used || 0);
      setCustomDomainLimit(res.data.custom_domain_limit || 0);
      setCustomDomainUsed(res.data.custom_domain_used || 0);
    } catch {
      showToast(
        t('error_fetch_reservations', 'Failed to load reservations'),
        'error',
      );
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    axios
      .get('/api/me')
      .then((res) => {
        const style = res.data?.subdomain_style;
        if (style && !styleInitialized) {
          setSubdomainStyle(style);
          setStyleInitialized(true);
        }
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const generateSubdomain = async () => {
    try {
      const res = await axios.get(
        `/api/portal/generate-subdomain?style=${subdomainStyle}`,
      );
      setSubdomainInput(res.data.subdomain);
    } catch {
      showToast(
        t('error_generate_subdomain', 'Failed to generate subdomain'),
        'error',
      );
    }
  };

  const createReservation = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!subdomainInput) {
      showToast(
        t('error_enter_subdomain', 'Please enter or generate a subdomain'),
        'error',
      );
      return;
    }
    try {
      await axios.post('/api/portal/reservations', {
        subdomain: subdomainInput.toLowerCase(),
        domain: selectedDomain,
      });
      setSubdomainInput('');
      fetchData();
      showToast(
        t('success_create_reservation', 'Subdomain reserved successfully'),
        'success',
      );
    } catch (err: any) {
      showToast(
        `${t('error', 'Error')}: ${err.response?.data?.error || t('failed_create_reservation', 'Failed to create reservation')}`,
        'error',
      );
    }
  };

  // Which of the endpoint's refusals this was, in words the user can act on (#2223).
  //
  // Status alone cannot tell them apart: mapErrorToStatusCode (pkg/server/api_errors.go) puts
  // BOTH ErrQuotaReached and ErrInvalidRequest on 400, so "you are out of quota" and "that is
  // not a domain this flow accepts" arrive identically and only the sentinel text separates
  // them. Collapsing the three into one "failed" toast would leave the two most actionable
  // cases -- release one, or fix the name -- indistinguishable from each other.
  const customDomainErrorMessage = (err: any): string => {
    const status = err?.response?.status;
    const reason = String(err?.response?.data?.error || '');
    if (status === 409) {
      return t(
        'error_custom_domain_taken',
        'That domain is already registered to someone else.',
      );
    }
    if (status === 400 && reason.includes('quota')) {
      return t(
        'custom_domain_limit_reached',
        'You have reached your custom domain limit. Release one to register a new one.',
      );
    }
    if (status === 400) {
      return t(
        'error_custom_domain_invalid',
        'Enter a domain you own, such as demo.customer.com. A name under a domain this gateway already serves is a subdomain reservation, not a custom domain.',
      );
    }
    return t('failed_create_custom_domain', 'Failed to register custom domain');
  };

  // Registering a custom domain from the portal (#2222 built the endpoint, #2223 this control).
  //
  // Idempotent for the holder: re-submitting a domain already held answers 200 with the existing
  // row rather than a conflict (CreateCustomDomain, api_service_reservation.go), so landing on
  // this form twice is success and is reported as such.
  const createCustomDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    const domain = customDomainInput.trim().toLowerCase();
    if (!domain) {
      showToast(
        t('error_enter_custom_domain', 'Please enter a domain name'),
        'error',
      );
      return;
    }
    setCustomDomainSubmitting(true);
    try {
      await axios.post('/api/portal/custom-domains', { domain });
      setCustomDomainInput('');
      fetchData();
      showToast(
        t(
          'success_create_custom_domain',
          'Custom domain registered. Point it at this gateway with a CNAME record if you have not already.',
        ),
        'success',
      );
    } catch (err: any) {
      showToast(
        `${t('error', 'Error')}: ${customDomainErrorMessage(err)}`,
        'error',
      );
    } finally {
      setCustomDomainSubmitting(false);
    }
  };

  // Subdomain reservations and custom domains are tracked as one list server-side, but
  // shown as two separate tables (#1053) -- server.go sets SubdomainPrefix = "" for
  // custom-domain rows, so a truthy r.subdomain is the reliable signal for "which table
  // does this row belong in" (same signal the host-string/CLI-command logic below already
  // relied on before the split).
  const subdomainColumns: ColumnDef<Reservation>[] = useMemo(
    () => [
      { key: 'subdomain', label: t('subdomain', 'Subdomain'), sortable: true },
      { key: 'status', label: t('status', 'Status'), sortable: true },
      { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
      {
        key: 'created_at',
        label: t('created_at', 'Created Date'),
        sortable: true,
      },
    ],
    [t],
  );

  const customDomainColumns: ColumnDef<Reservation>[] = useMemo(
    () => [
      { key: 'domain', label: t('custom_domain', 'Domain'), sortable: true },
      { key: 'status', label: t('status', 'Status'), sortable: true },
      { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
      {
        key: 'created_at',
        label: t('created_at', 'Created Date'),
        sortable: true,
      },
    ],
    [t],
  );

  const mappedReservations = useMemo(() => {
    const now = new Date();
    return reservations.map((r) => {
      const isExpired = r.expires_at && new Date(r.expires_at) <= now;
      const statusLabel = isExpired
        ? 'quarantined'
        : r.extension_requested
          ? 'extension requested'
          : 'active';
      return {
        ...r,
        computed_status: statusLabel,
      };
    });
  }, [reservations]);

  const subdomainReservations = useMemo(
    () => mappedReservations.filter((r) => !!r.subdomain),
    [mappedReservations],
  );
  const customDomainReservations = useMemo(
    () => mappedReservations.filter((r) => !r.subdomain),
    [mappedReservations],
  );

  const statusOptions = useMemo(
    () => [
      { value: 'active', label: t('status_active', 'active') },
      { value: 'quarantined', label: t('status_quarantined', 'quarantined') },
      {
        value: 'extension requested',
        label: t('status_extension_requested', 'extension requested'),
      },
    ],
    [t],
  );

  const subdomainTable = useDataTable<
    Reservation & { computed_status: string }
  >(
    'dashboard_reservations_subdomains',
    subdomainReservations,
    ['subdomain', 'domain', 'status'],
    subdomainColumns as any,
    10,
    ['created_at'],
    'computed_status',
    statusOptions,
    'all',
  );

  const customDomainTable = useDataTable<
    Reservation & { computed_status: string }
  >(
    'dashboard_reservations_custom_domains',
    customDomainReservations,
    ['domain', 'status'],
    customDomainColumns as any,
    10,
    ['created_at'],
    'computed_status',
    statusOptions,
    'all',
  );

  const deleteReservation = async (id: string) => {
    if (
      !(await showConfirm(
        t('release_subdomain_title', 'Release Subdomain'),
        t(
          'confirm_release_subdomain',
          'Are you sure you want to release this subdomain?',
        ),
      ))
    )
      return;
    try {
      await axios.delete(`/api/portal/reservations/${encodeURIComponent(id)}`);
      fetchData();
      showToast(
        t('success_delete_reservation', 'Subdomain released successfully'),
        'success',
      );
    } catch (err: any) {
      showToast(
        `${t('error', 'Error')}: ${err.response?.data?.error || t('failed_delete', 'Failed to delete')}`,
        'error',
      );
    }
  };

  const requestExtension = async (id: string) => {
    try {
      await axios.post(
        `/api/portal/reservations/${encodeURIComponent(id)}/request-extension`,
      );
      fetchData();
      showToast(
        t(
          'success_request_extension',
          'Lease extension requested successfully',
        ),
        'success',
      );
    } catch (err: any) {
      showToast(
        err.response?.data?.error ||
          t('failed_request_extension', 'Failed to request extension'),
        'error',
      );
    }
  };

  const [acModalReservation, setAcModalReservation] =
    useState<Reservation | null>(null);
  const [acMode, setAcMode] = useState('public');
  // Which fields the selected mode actually uses. Drives `disabled`, never whether a field is
  // rendered -- see the note by the passcode group.
  const acFields = accessControlFieldsForMode(acMode);
  const [acPasscode, setAcPasscode] = useState('');
  const [acWhitelist, setAcWhitelist] = useState('');
  const [acPasscodeConfirm, setAcPasscodeConfirm] = useState('');
  const [acError, setAcError] = useState('');
  const [acSaving, setAcSaving] = useState(false);

  const openAcModal = (res: Reservation) => {
    setAcModalReservation(res);
    setAcMode(res.access_mode || 'public');
    // Deliberately NOT res.passcode. The API now returns a mask rather than the stored bcrypt
    // hash, and putting even the mask in the box invites saving it back. Empty means "leave the
    // passcode alone"; the hint below says whether one is set (#2103).
    setAcPasscode('');
    setAcPasscodeConfirm('');
    setAcError('');
    setAcWhitelist(res.whitelist_ips || '');
  };

  const handleUpdateAccessControl = async () => {
    // Obscured input needs confirming: you cannot see what you typed, and a passcode with a
    // typo in it locks out the people it was meant to admit (#2103).
    if (acPasscode !== '' && acPasscode !== acPasscodeConfirm) {
      setAcError(t('passcode_mismatch', 'The two passcodes do not match.'));
      return;
    }
    setAcError('');

    if (!acModalReservation) return;
    setAcSaving(true);
    try {
      await axios.post('/api/portal/reservations/access-control', {
        subdomain: acModalReservation.subdomain,
        domain: acModalReservation.domain,
        access_mode: acMode,
        // The mask when untouched, so the gateway leaves the stored passcode alone.
        passcode: acPasscode === '' ? '********' : acPasscode,
        whitelist_ips: acWhitelist,
      });
      fetchData();
      setAcModalReservation(null);
      showToast(
        t('access_control_saved', 'Access control settings saved'),
        'success',
      );
    } catch (err: any) {
      showToast(
        err.response?.data?.error ||
          t('error_save_access_control', 'Failed to save access control'),
        'error',
      );
    } finally {
      setAcSaving(false);
    }
  };

  const copyText = async (text: string, message: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showToast(message, 'success');
    } catch {
      showToast('Failed to copy to clipboard', 'error');
    }
  };

  if (loading) {
    return (
      <div className="card mb-xl">
        <div className="mb-md">
          <Skeleton width={200} height={24} />
        </div>
        <div className="mb-xl">
          <div className="flex justify-between mb-xs">
            <Skeleton width={120} height={16} />
            <Skeleton width={80} height={16} />
          </div>
          <Skeleton width="100%" height={8} borderRadius={4} />
        </div>
        <div className="flex gap-sm mb-xl flex-wrap">
          <Skeleton width="100%" height={40} className="flex-1 min-w-sm" />
          <Skeleton width="100%" height={40} className="flex-1 min-w-sm" />
          <Skeleton width={80} height={40} />
          <Skeleton width={80} height={40} />
        </div>
        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="th-col">
                  <Skeleton width={120} />
                </th>
                <th className="th-col">
                  <Skeleton width={80} />
                </th>
                <th className="th-col">
                  <Skeleton width={120} />
                </th>
                <th className="th-col">
                  <Skeleton width={80} />
                </th>
              </tr>
            </thead>
            <tbody>
              {[...Array(3)].map((_, i) => (
                <tr key={i} className="border-b">
                  <td className="td-cell">
                    <Skeleton width="80%" height={16} />
                  </td>
                  <td className="td-cell">
                    <Skeleton width={50} height={20} borderRadius={10} />
                  </td>
                  <td className="td-cell">
                    <Skeleton width="70%" height={16} />
                  </td>
                  <td className="td-cell">
                    <Skeleton width={60} height={28} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    );
  }

  const percent = limit > 0 ? (used / limit) * 100 : 0;
  const isAtLimit = limit >= 0 && used >= limit;
  const customDomainPercent =
    customDomainLimit > 0 ? (customDomainUsed / customDomainLimit) * 100 : 0;
  const isAtCustomDomainLimit =
    customDomainLimit >= 0 && customDomainUsed >= customDomainLimit;

  return (
    <>
      <div className="card mb-xl">
        <div className="section-header mb-md">
          <SectionHeading
            anchor="reservations-overview"
            className="section-title"
            label={t('reservations_overview', 'Reservations Overview')}
          />
        </div>

        <div className="mb-xl">
          <div className="flex justify-between text-sm mb-xs">
            <span>{t('reservation_quota', 'My Personal Quota')}</span>
            <span>
              {limit < 0 ? `${used} / ∞` : `${used} / ${limit}`}{' '}
              {t('reserved', 'reserved')}
            </span>
          </div>
          <div className="progress-track">
            {/* Only the width is computed; at-limit is a state, so it is a class. */}
            <div
              className={`progress-fill${isAtLimit ? ' is-at-limit' : ''}`}
              style={{ width: `${Math.min(percent, 100)}%` }}
            ></div>
          </div>
          {isAtLimit && limit >= 0 && (
            <div className="mt-sm text-xs text-warning">
              ⚠️{' '}
              {t(
                'reservation_limit_reached',
                'You have reached your reservation limit. Release a subdomain to register a new one.',
              )}
            </div>
          )}
          <div className="flex justify-end mt-sm">
            <a
              href="#registered-subdomains"
              className="btn btn-outline py-xs px-md text-xs w-auto m-0"
            >
              {t('view_registered_subdomains', 'View Registered Subdomains')} ↓
            </a>
          </div>
        </div>

        <div className="mb-xl">
          <div className="flex justify-between text-sm mb-xs">
            <span>{t('custom_domain_quota', 'Custom Domain Quota')}</span>
            <span>
              {customDomainLimit < 0
                ? `${customDomainUsed} / ∞`
                : `${customDomainUsed} / ${customDomainLimit}`}{' '}
              {t('reserved', 'reserved')}
            </span>
          </div>
          <div className="progress-track">
            <div
              className={`progress-fill${isAtCustomDomainLimit ? ' is-at-limit' : ''}`}
              style={{ width: `${Math.min(customDomainPercent, 100)}%` }}
            ></div>
          </div>
          {/* Said what the admin/owner path does, and that path is refused for everyone else
              (canUserAutoReserve, server.go) -- which is probably why nobody noticed the portal
              had no register control at all (#2221). It now describes the control below, and
              keeps the CNAME visible: that is the one step the user performs outside the
              product, and being asked for a domain name is the moment they need to know it. */}
          <p className="text-muted text-xs mt-xs mb-0">
            {t(
              'custom_domain_quota_hint',
              'Custom domains are tracked separately from the subdomain reservations above, up to this limit. Point the domain at this gateway with a CNAME record, register it below, then connect with -domain.',
            )}
          </p>
          {isAtCustomDomainLimit && customDomainLimit >= 0 && (
            <div className="mt-sm text-xs text-warning">
              ⚠️{' '}
              {t(
                'custom_domain_limit_reached',
                'You have reached your custom domain limit. Release one to register a new one.',
              )}
            </div>
          )}
          <div className="flex justify-end mt-sm">
            <a
              href="#custom-domains"
              className="btn btn-outline py-xs px-md text-xs w-auto m-0"
            >
              {t('view_custom_domains', 'View Custom Domains')} ↓
            </a>
          </div>
        </div>

        {!isAtLimit && (
          <form
            onSubmit={createReservation}
            className="flex gap-sm mb-xl flex-wrap"
          >
            <div className="flex-1 min-w-sm">
              <input
                type="text"
                className="form-control"
                placeholder={t('subdomain', 'subdomain')}
                value={subdomainInput}
                onChange={(e) => setSubdomainInput(e.target.value)}
                aria-label={t('subdomain', 'Subdomain')}
              />
            </div>
            <div className="flex-1 min-w-sm">
              <select
                className="form-control"
                value={selectedDomain}
                onChange={(e) => setSelectedDomain(e.target.value)}
                aria-label={t('domain', 'Domain')}
              >
                {domains.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </select>
            </div>
            <div className="min-w-xs">
              <select
                className="form-control"
                value={subdomainStyle}
                onChange={(e) => setSubdomainStyle(e.target.value)}
                aria-label={t('style_label', 'Subdomain style')}
              >
                <option value="liferay">
                  {t('style_liferay', 'Liferay SE Style')}
                </option>
                <option value="words">{t('style_words', 'Words Style')}</option>
                <option value="heroku">
                  {t('style_heroku', 'Heroku Style')}
                </option>
                <option value="ngrok">{t('style_ngrok', 'Ngrok Style')}</option>
                <option value="random">
                  {t('style_random', 'Alphanumeric')}
                </option>
              </select>
            </div>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={generateSubdomain}
            >
              {t('generate', 'Generate')}
            </button>
            <button type="submit" className="btn btn-primary">
              {t('reserve', 'Reserve')}
            </button>
          </form>
        )}

        {/* DISABLED at the quota, never removed -- V1 does the same, and the two arms must not
            disagree about whether the control exists (#2241).

            It was `{!isAtCustomDomainLimit && (...)}`, mirroring the subdomain form above. That
            is right for subdomains, where the quota is several and a user at the limit has
            visible reservations explaining why. The custom-domain quota defaults to ONE, so the
            same pattern hid the control from everyone who had ever used the feature -- including
            every admin and owner, who have always been able to auto-reserve by connecting with
            -domain. Reported from production the day it shipped as "I can see it on V1 but not
            V2", where the honest reading of an absent control is that it was never built.

            Deliberately here and not on VanityDomainStatusPanel, which returns null for a user
            with no attempts yet and so would hide this from precisely the people who have never
            had a custom domain (#2223). */}
        <div>
          <p className="text-muted text-xs mt-0 mb-sm">
            {t(
              'custom_domain_cname_hint',
              'Before you register: point the domain at this gateway with a CNAME record. That record is the one step only you can do -- the gateway obtains and installs the TLS certificate itself.',
            )}
          </p>
          <form onSubmit={createCustomDomain} className="flex gap-sm flex-wrap">
            <div className="flex-1 min-w-sm">
              <input
                type="text"
                className="form-control"
                placeholder={t(
                  'custom_domain_placeholder',
                  'demo.customer.com',
                )}
                value={customDomainInput}
                onChange={(e) => setCustomDomainInput(e.target.value)}
                disabled={isAtCustomDomainLimit}
                aria-label={t(
                  'aria_custom_domain',
                  'Custom domain to register',
                )}
              />
            </div>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={customDomainSubmitting || isAtCustomDomainLimit}
            >
              {t('register_custom_domain', 'Register Domain')}
            </button>
          </form>
        </div>
      </div>

      <ReservationsTable
        id="registered-subdomains"
        title={t('subdomain_reservations', 'Registered Subdomains')}
        emptyMessage={t(
          'no_subdomains_reserved',
          'No subdomains reserved yet.',
        )}
        searchPlaceholder={t(
          'search_reservations_placeholder',
          'Search reservations...',
        )}
        primaryColumnKey="subdomain"
        columns={subdomainColumns}
        statusOptions={statusOptions}
        formatDate={formatDate}
        copyText={copyText}
        requestExtension={requestExtension}
        openAcModal={openAcModal}
        deleteReservation={deleteReservation}
        totalUnfilteredCount={subdomainReservations.length}
        paginatedItems={subdomainTable.paginatedItems}
        searchQuery={subdomainTable.searchQuery}
        setSearchQuery={subdomainTable.setSearchQuery}
        statusFilter={subdomainTable.statusFilter}
        setStatusFilter={subdomainTable.setStatusFilter}
        pageSize={subdomainTable.pageSize}
        setPageSize={subdomainTable.setPageSize}
        currentPage={subdomainTable.currentPage}
        setCurrentPage={subdomainTable.setCurrentPage}
        totalPages={subdomainTable.totalPages}
        totalItems={subdomainTable.totalItems}
        isColumnVisible={subdomainTable.isColumnVisible}
        toggleColumn={subdomainTable.toggleColumn}
        requestSort={subdomainTable.requestSort}
        getSortIndicator={subdomainTable.getSortIndicator}
        getAriaSort={subdomainTable.getAriaSort}
      />

      <ReservationsTable
        id="custom-domains"
        title={t('custom_domains', 'Custom Domains')}
        emptyMessage={t(
          'no_custom_domains_registered',
          'No custom domains registered yet.',
        )}
        searchPlaceholder={t(
          'search_custom_domains_placeholder',
          'Search custom domains...',
        )}
        primaryColumnKey="domain"
        columns={customDomainColumns}
        statusOptions={statusOptions}
        formatDate={formatDate}
        copyText={copyText}
        requestExtension={requestExtension}
        openAcModal={openAcModal}
        deleteReservation={deleteReservation}
        totalUnfilteredCount={customDomainReservations.length}
        paginatedItems={customDomainTable.paginatedItems}
        searchQuery={customDomainTable.searchQuery}
        setSearchQuery={customDomainTable.setSearchQuery}
        statusFilter={customDomainTable.statusFilter}
        setStatusFilter={customDomainTable.setStatusFilter}
        pageSize={customDomainTable.pageSize}
        setPageSize={customDomainTable.setPageSize}
        currentPage={customDomainTable.currentPage}
        setCurrentPage={customDomainTable.setCurrentPage}
        totalPages={customDomainTable.totalPages}
        totalItems={customDomainTable.totalItems}
        isColumnVisible={customDomainTable.isColumnVisible}
        toggleColumn={customDomainTable.toggleColumn}
        requestSort={customDomainTable.requestSort}
        getSortIndicator={customDomainTable.getSortIndicator}
        getAriaSort={customDomainTable.getAriaSort}
      />

      {/* Access Control Modal */}
      {acModalReservation && (
        <ModalShell
          isOpen
          onClose={() => setAcModalReservation(null)}
          labelledBy="access-control-modal-title"
          cardClassName="card modal-card max-w-md p-xl"
        >
          <div className="modal-header">
            <h3 id="access-control-modal-title" className="modal-title">
              🔒 {t('access_control', 'Access Control')}
            </h3>
            <button
              type="button"
              onClick={() => setAcModalReservation(null)}
              className="modal-close"
              aria-label={t('close', 'Close')}
            >
              ✕
            </button>
          </div>
          <p className="text-muted text-sm mb-lg">
            <strong className="text-primary font-mono">
              {acModalReservation.subdomain
                ? `${acModalReservation.subdomain}.${acModalReservation.domain}`
                : acModalReservation.domain}
            </strong>
          </p>

          <div className="form-group">
            <span className="form-label--bold" id="access-mode-label">
              {t('access_mode', 'Access Mode')}
            </span>
            {/* A caption for a set of radios, not for one control, so it is announced
                  via a group rather than htmlFor -- which names a single element. */}
            <div
              className="flex flex-col gap-sm"
              role="radiogroup"
              aria-labelledby="access-mode-label"
            >
              {(
                [
                  [
                    'public',
                    '🌐',
                    t('access_public', 'Public — Anyone can access'),
                  ],
                  [
                    'passcode',
                    '🔑',
                    t('access_passcode', 'Passcode — Requires a secret code'),
                  ],
                  [
                    'whitelist',
                    '🛡',
                    t(
                      'access_whitelist',
                      'IP Whitelist — Restrict by IP address',
                    ),
                  ],
                  // V2 offered only the three above, so a reservation in `or` or `and` opened
                  // with NO radio selected and, because each field rendered only under its own
                  // exact mode, no passcode or whitelist field either -- an empty dialog on the
                  // two strictest settings in the product. Picking any radio to make the form
                  // usable silently downgraded the tunnel (#2155).
                  ['or', '🔑/🛡', t('access_or', 'Passcode OR Whitelist')],
                  ['and', '🔒', t('access_and', 'Passcode AND Whitelist')],
                ] as [string, string, string][]
              ).map(([val, icon, label]) => (
                <label
                  key={val}
                  className={`flex items-center gap-md p-md rounded cursor-pointer border ${acMode === val ? 'border-primary surface-selected' : 'border'}`}
                >
                  <input
                    type="radio"
                    name="acMode"
                    value={val}
                    checked={acMode === val}
                    onChange={() => setAcMode(val)}
                  />
                  <span>
                    {icon} {label}
                  </span>
                </label>
              ))}
            </div>
          </div>

          {/* Rendered whichever mode is selected, and disabled when the mode does not use
              it (#2155). Hiding gave no evidence that the value is retained, while the public
              notice promises exactly that -- a greyed field still showing its value is the
              honest version. */}
          {acMode === 'public' && (
            <p className="form-hint mb-md">
              {t(
                'access_public_notice',
                'This tunnel is open to all traffic. Any passcode or IP list is kept for when you switch back, and is not being applied.',
              )}
            </p>
          )}

          {
            <div className="form-group">
              <label className="form-label--bold" htmlFor="passcode">
                {t('passcode', 'Passcode')}
              </label>
              <input
                id="passcode"
                type="password"
                autoComplete="new-password"
                className="input-field"
                disabled={!acFields.passcode}
                value={acPasscode}
                onChange={(e) => setAcPasscode(e.target.value)}
                placeholder={
                  acModalReservation.passcode
                    ? t(
                        'passcode_set_placeholder',
                        'A passcode is set — leave blank to keep it',
                      )
                    : t('passcode_placeholder', 'Enter a secret passcode...')
                }
              />
              <label className="form-label mt-sm" htmlFor="passcode-confirm">
                {t('passcode_confirm', 'Confirm passcode')}
              </label>
              <input
                id="passcode-confirm"
                type="password"
                autoComplete="new-password"
                className="input-field"
                disabled={!acFields.passcode}
                value={acPasscodeConfirm}
                onChange={(e) => setAcPasscodeConfirm(e.target.value)}
                placeholder={t('passcode_confirm', 'Confirm passcode')}
              />
              {acError && (
                <p className="text-danger text-sm mt-sm mb-0">{acError}</p>
              )}
            </div>
          }

          {
            <div className="form-group">
              <label className="form-label--bold" htmlFor="allowed-ips">
                {t('allowed_ips', 'Allowed IPs')}
              </label>
              <textarea
                id="allowed-ips"
                className="input-field font-mono text-sm resize-y"
                disabled={!acFields.whitelist}
                value={acWhitelist}
                onChange={(e) => setAcWhitelist(e.target.value)}
                placeholder={
                  'One IP or CIDR per line, e.g.\n192.168.1.0/24\n10.0.0.1'
                }
                rows={4}
              />
              <p className="form-hint">
                {t(
                  'whitelist_hint',
                  'Enter individual IP addresses or CIDR ranges, one per line.',
                )}
              </p>
            </div>
          }

          <div className="flex gap-sm justify-end">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setAcModalReservation(null)}
              disabled={acSaving}
            >
              {t('cancel', 'Cancel')}
            </button>
            <button
              type="button"
              className="btn btn-primary"
              onClick={handleUpdateAccessControl}
              disabled={acSaving}
            >
              {acSaving ? t('saving', 'Saving...') : t('save', 'Save')}
            </button>
          </div>
        </ModalShell>
      )}
    </>
  );
}
