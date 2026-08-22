import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import { useI18n } from '../contexts/I18nContext';
import Skeleton from './Skeleton';
import { useUI } from '../contexts/UIContext';
import ReservationsTable from './ReservationsTable';

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
      showToast(t('error_fetch_reservations', 'Failed to load reservations'), 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    axios.get('/api/me')
      .then(res => {
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
      const res = await axios.get(`/api/portal/generate-subdomain?style=${subdomainStyle}`);
      setSubdomainInput(res.data.subdomain);
    } catch {
      showToast(t('error_generate_subdomain', 'Failed to generate subdomain'), 'error');
    }
  };

  const createReservation = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!subdomainInput) {
      showToast(t('error_enter_subdomain', 'Please enter or generate a subdomain'), 'error');
      return;
    }
    try {
      await axios.post('/api/portal/reservations', {
        subdomain: subdomainInput.toLowerCase(),
        domain: selectedDomain
      });
      setSubdomainInput('');
      fetchData();
      showToast(t('success_create_reservation', 'Subdomain reserved successfully'), 'success');
    } catch (err: any) {
      showToast(`${t('error', 'Error')}: ${err.response?.data?.error || t('failed_create_reservation', 'Failed to create reservation')}`, 'error');
    }
  };

  // Subdomain reservations and custom domains are tracked as one list server-side, but
  // shown as two separate tables (#1053) -- server.go sets SubdomainPrefix = "" for
  // custom-domain rows, so a truthy r.subdomain is the reliable signal for "which table
  // does this row belong in" (same signal the host-string/CLI-command logic below already
  // relied on before the split).
  const subdomainColumns: ColumnDef<Reservation>[] = useMemo(() => [
    { key: 'subdomain', label: t('subdomain', 'Subdomain'), sortable: true },
    { key: 'status', label: t('status', 'Status'), sortable: true },
    { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
    { key: 'created_at', label: t('created_at', 'Created Date'), sortable: true },
  ], [t]);

  const customDomainColumns: ColumnDef<Reservation>[] = useMemo(() => [
    { key: 'domain', label: t('custom_domain', 'Domain'), sortable: true },
    { key: 'status', label: t('status', 'Status'), sortable: true },
    { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
    { key: 'created_at', label: t('created_at', 'Created Date'), sortable: true },
  ], [t]);

  const mappedReservations = useMemo(() => {
    const now = new Date();
    return reservations.map(r => {
      const isExpired = r.expires_at && new Date(r.expires_at) <= now;
      const statusLabel = isExpired ? 'quarantined' : (r.extension_requested ? 'extension requested' : 'active');
      return {
        ...r,
        computed_status: statusLabel
      };
    });
  }, [reservations]);

  const subdomainReservations = useMemo(
    () => mappedReservations.filter(r => !!r.subdomain),
    [mappedReservations]
  );
  const customDomainReservations = useMemo(
    () => mappedReservations.filter(r => !r.subdomain),
    [mappedReservations]
  );

  const statusOptions = useMemo(() => [
    { value: 'active', label: t('status_active', 'active') },
    { value: 'quarantined', label: t('status_quarantined', 'quarantined') },
    { value: 'extension requested', label: t('status_extension_requested', 'extension requested') }
  ], [t]);

  const subdomainTable = useDataTable<Reservation & { computed_status: string }>(
    'dashboard_reservations_subdomains',
    subdomainReservations,
    ['subdomain', 'domain', 'status'],
    subdomainColumns as any,
    10,
    ['created_at'],
    'computed_status',
    statusOptions,
    'all'
  );

  const customDomainTable = useDataTable<Reservation & { computed_status: string }>(
    'dashboard_reservations_custom_domains',
    customDomainReservations,
    ['domain', 'status'],
    customDomainColumns as any,
    10,
    ['created_at'],
    'computed_status',
    statusOptions,
    'all'
  );

  const deleteReservation = async (id: string) => {
    if (!(await showConfirm(t('release_subdomain_title', 'Release Subdomain'), t('confirm_release_subdomain', 'Are you sure you want to release this subdomain?')))) return;
    try {
      await axios.delete(`/api/portal/reservations/${encodeURIComponent(id)}`);
      fetchData();
      showToast(t('success_delete_reservation', 'Subdomain released successfully'), 'success');
    } catch (err: any) {
      showToast(`${t('error', 'Error')}: ${err.response?.data?.error || t('failed_delete', 'Failed to delete')}`, 'error');
    }
  };

  const requestExtension = async (id: string) => {
    try {
      await axios.post(`/api/portal/reservations/${encodeURIComponent(id)}/request-extension`);
      fetchData();
      showToast(t('success_request_extension', 'Lease extension requested successfully'), 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || t('failed_request_extension', 'Failed to request extension'), 'error');
    }
  };

  const [acModalReservation, setAcModalReservation] = useState<Reservation | null>(null);
  const [acMode, setAcMode] = useState('public');
  const [acPasscode, setAcPasscode] = useState('');
  const [acWhitelist, setAcWhitelist] = useState('');
  const [acSaving, setAcSaving] = useState(false);

  const openAcModal = (res: Reservation) => {
    setAcModalReservation(res);
    setAcMode(res.access_mode || 'public');
    setAcPasscode(res.passcode || '');
    setAcWhitelist(res.whitelist_ips || '');
  };

  const handleUpdateAccessControl = async () => {
    if (!acModalReservation) return;
    setAcSaving(true);
    try {
      await axios.post('/api/portal/reservations/access-control', {
        subdomain: acModalReservation.subdomain,
        domain:    acModalReservation.domain,
        access_mode:   acMode,
        passcode:      acPasscode,
        whitelist_ips: acWhitelist,
      });
      fetchData();
      setAcModalReservation(null);
      showToast(t('access_control_saved', 'Access control settings saved'), 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || t('error_save_access_control', 'Failed to save access control'), 'error');
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
                <th className="th-col"><Skeleton width={120} /></th>
                <th className="th-col"><Skeleton width={80} /></th>
                <th className="th-col"><Skeleton width={120} /></th>
                <th className="th-col"><Skeleton width={80} /></th>
              </tr>
            </thead>
            <tbody>
              {[...Array(3)].map((_, i) => (
                <tr key={i} className="border-b">
                  <td className="td-cell"><Skeleton width="80%" height={16} /></td>
                  <td className="td-cell"><Skeleton width={50} height={20} borderRadius={10} /></td>
                  <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                  <td className="td-cell"><Skeleton width={60} height={28} /></td>
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
  const customDomainPercent = customDomainLimit > 0 ? (customDomainUsed / customDomainLimit) * 100 : 0;
  const isAtCustomDomainLimit = customDomainLimit >= 0 && customDomainUsed >= customDomainLimit;

  return (
    <>
      <div className="card mb-xl">
        <div className="section-header mb-md">
          <h3 className="section-title">{t('reservations_overview', 'Reservations Overview')}</h3>
        </div>
        
        <div className="mb-xl">
          <div className="flex justify-between text-sm mb-xs">
            <span>{t('reservation_quota', 'My Personal Quota')}</span>
            <span>{limit < 0 ? `${used} / ∞` : `${used} / ${limit}`} {t('reserved', 'reserved')}</span>
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
              ⚠️ {t('reservation_limit_reached', 'You have reached your reservation limit. Release a subdomain to register a new one.')}
            </div>
          )}
          <div className="flex justify-end mt-sm">
            <a href="#registered-subdomains" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
              {t('view_registered_subdomains', 'View Registered Subdomains')} ↓
            </a>
          </div>
        </div>

        <div className="mb-xl">
          <div className="flex justify-between text-sm mb-xs">
            <span>{t('custom_domain_quota', 'Custom Domain Quota')}</span>
            <span>{customDomainLimit < 0 ? `${customDomainUsed} / ∞` : `${customDomainUsed} / ${customDomainLimit}`} {t('reserved', 'reserved')}</span>
          </div>
          <div className="progress-track">
            <div
              className={`progress-fill${isAtCustomDomainLimit ? ' is-at-limit' : ''}`}
              style={{ width: `${Math.min(customDomainPercent, 100)}%` }}
            ></div>
          </div>
          <p className="text-muted text-xs mt-xs mb-0">
            {t('custom_domain_quota_hint', 'Custom domains (via CNAME) are tracked separately from subdomain reservations above -- connect your client with -domain to reserve one, up to this limit.')}
          </p>
          {isAtCustomDomainLimit && customDomainLimit >= 0 && (
            <div className="mt-sm text-xs text-warning">
              ⚠️ {t('custom_domain_limit_reached', 'You have reached your custom domain limit. Release one to register a new one.')}
            </div>
          )}
          <div className="flex justify-end mt-sm">
            <a href="#custom-domains" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
              {t('view_custom_domains', 'View Custom Domains')} ↓
            </a>
          </div>
        </div>

        {!isAtLimit && (
          <form onSubmit={createReservation} className="flex gap-sm mb-xl flex-wrap">
            <div className="flex-1 min-w-sm">
              <input 
                type="text" 
                className="form-control" 
                placeholder={t('subdomain', 'subdomain')} 
                value={subdomainInput} 
                onChange={(e) => setSubdomainInput(e.target.value)} 
              />
            </div>
            <div className="flex-1 min-w-sm">
              <select className="form-control" value={selectedDomain} onChange={(e) => setSelectedDomain(e.target.value)}>
                {domains.map(d => (
                  <option key={d} value={d}>{d}</option>
                ))}
              </select>
            </div>
            <div className="min-w-xs">
              <select className="form-control" value={subdomainStyle} onChange={(e) => setSubdomainStyle(e.target.value)}>
                <option value="liferay">{t('style_liferay', 'Liferay SE Style')}</option>
                <option value="words">{t('style_words', 'Words Style')}</option>
                <option value="heroku">{t('style_heroku', 'Heroku Style')}</option>
                <option value="ngrok">{t('style_ngrok', 'Ngrok Style')}</option>
                <option value="random">{t('style_random', 'Alphanumeric')}</option>
              </select>
            </div>
            <button type="button" className="btn btn-secondary" onClick={generateSubdomain}>{t('generate', 'Generate')}</button>
            <button type="submit" className="btn btn-primary">{t('reserve', 'Reserve')}</button>
          </form>
        )}
      </div>

      <ReservationsTable
        id="registered-subdomains"
        title={t('subdomain_reservations', 'Registered Subdomains')}
        emptyMessage={t('no_subdomains_reserved', 'No subdomains reserved yet.')}
        searchPlaceholder={t('search_reservations_placeholder', 'Search reservations...')}
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
        emptyMessage={t('no_custom_domains_registered', 'No custom domains registered yet.')}
        searchPlaceholder={t('search_custom_domains_placeholder', 'Search custom domains...')}
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
        <div className="modal-backdrop">
          <div 
            className="card modal-card max-w-md p-xl"
            role="dialog"
            aria-modal="true"
            aria-labelledby="access-control-modal-title"
          >
            <div className="modal-header">
              <h3 id="access-control-modal-title" className="modal-title">
                🔒 {t('access_control', 'Access Control')}
              </h3>
              <button type="button" onClick={() => setAcModalReservation(null)} className="modal-close" aria-label={t('close', 'Close')}>✕</button>
            </div>
            <p className="text-muted text-sm mb-lg">
              <strong className="text-primary font-mono">
                {acModalReservation.subdomain ? `${acModalReservation.subdomain}.${acModalReservation.domain}` : acModalReservation.domain}
              </strong>
            </p>

            <div className="form-group">
              <label className="form-label--bold">
                {t('access_mode', 'Access Mode')}
              </label>
              <div className="flex flex-col gap-sm">
                {([['public', '🌐', t('access_public', 'Public — Anyone can access')],
                  ['passcode', '🔑', t('access_passcode', 'Passcode — Requires a secret code')],
                  ['whitelist', '🛡', t('access_whitelist', 'IP Whitelist — Restrict by IP address')]] as [string, string, string][]).map(([val, icon, label]) => (
                  <label key={val} className={`flex items-center gap-md p-md rounded cursor-pointer border ${acMode === val ? 'border-primary' : 'border'}`} style={{ background: acMode === val ? 'rgba(11,95,255,0.05)' : 'transparent' }}>
                    <input type="radio" name="acMode" value={val} checked={acMode === val} onChange={() => setAcMode(val)} style={{ accentColor: 'var(--primary)' }} />
                    <span>{icon} {label}</span>
                  </label>
                ))}
              </div>
            </div>

            {acMode === 'passcode' && (
              <div className="form-group">
                <label className="form-label--bold">{t('passcode', 'Passcode')}</label>
                <input
                  type="text"
                  className="input-field"
                  value={acPasscode}
                  onChange={e => setAcPasscode(e.target.value)}
                  placeholder={t('passcode_placeholder', 'Enter a secret passcode...')}
                />
              </div>
            )}

            {acMode === 'whitelist' && (
              <div className="form-group">
                <label className="form-label--bold">{t('allowed_ips', 'Allowed IPs')}</label>
                <textarea
                  className="input-field"
                  value={acWhitelist}
                  onChange={e => setAcWhitelist(e.target.value)}
                  placeholder={'One IP or CIDR per line, e.g.\n192.168.1.0/24\n10.0.0.1'}
                  rows={4}
                  style={{ fontFamily: 'monospace', fontSize: '13px', resize: 'vertical' }}
                />
                <p className="form-hint">
                  {t('whitelist_hint', 'Enter individual IP addresses or CIDR ranges, one per line.')}
                </p>
              </div>
            )}

            <div className="flex gap-sm justify-end">
              <button type="button" className="btn btn-secondary" onClick={() => setAcModalReservation(null)} disabled={acSaving}>{t('cancel', 'Cancel')}</button>
              <button type="button" className="btn btn-primary" onClick={handleUpdateAccessControl} disabled={acSaving}>
                {acSaving ? t('saving', 'Saving...') : t('save', 'Save')}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
