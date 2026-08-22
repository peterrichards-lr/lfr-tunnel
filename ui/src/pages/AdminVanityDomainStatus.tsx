import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import Skeleton from '../components/Skeleton';
import ActionMenu from '../components/ActionMenu';
import StageIcon from '../components/VanityStageIcon';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';

interface VanityDomainStatus {
  full_host: string;
  user_id: string;
  requested_at: string | null;
  nginx_config_at: string | null;
  cert_issued_at: string | null;
  live_at: string | null;
  failed_stage?: string;
  error_message?: string;
  updated_at: string;
}

export default function AdminVanityDomainStatus() {
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();
  const { formatDate } = useSettings();
  const [statuses, setStatuses] = useState<VanityDomainStatus[]>([]);
  const [loading, setLoading] = useState(true);

  const columns: ColumnDef<VanityDomainStatus>[] = useMemo(() => [
    { key: 'full_host', label: t('vanity_status_col_domain', 'Domain'), sortable: true },
    { key: 'user_id', label: t('vanity_status_col_owner', 'Owner'), sortable: true },
    { key: 'updated_at', label: t('vanity_status_col_updated', 'Last Updated'), sortable: true },
  ], [t]);

  const {
    paginatedItems,
    currentPage,
    totalPages,
    totalItems,
    pageSize,
    setCurrentPage,
    setPageSize,
    searchQuery,
    setSearchQuery,
    requestSort,
    getSortIndicator,
    getAriaSort,
    isColumnVisible,
    toggleColumn,
    allColumns
  } = useDataTable<VanityDomainStatus>(
    'admin_vanity_domain_status',
    statuses,
    ['full_host', 'user_id'],
    columns,
    10
  );

  const fetchData = async () => {
    try {
      const res = await axios.get('/api/admin/vanity-domain-status');
      setStatuses(res.data || []);
    } catch {
      showToast(t('error_fetch_vanity_status', 'Failed to load vanity domain status'), 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // Same rationale as the portal panel: provisioning runs over roughly 10-60s, poll so a
    // domain's progress is visible without a manual refresh.
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const stageIcon = (status: VanityDomainStatus, stage: string, timestamp: string | null) => {
    if (status.failed_stage === stage) {
      return <StageIcon state="failed" title={status.error_message || t('vanity_status_failed', 'Failed')} />;
    }
    if (timestamp) {
      return <StageIcon state="done" title={formatDate(timestamp)} />;
    }
    return <StageIcon state="open" title={t('vanity_status_not_reached', 'Not yet reached')} />;
  };

  const summaryFor = (status: VanityDomainStatus) => {
    if (status.failed_stage) {
      return `${t('vanity_status_summary_failed', 'Failed')} (${status.failed_stage}): ${status.error_message || t('vanity_status_unknown_error', 'Unknown error')}`;
    }
    if (status.live_at) {
      return `${t('vanity_status_summary_live', 'Live since')} ${formatDate(status.live_at)}`;
    }
    if (status.cert_issued_at) {
      return t('vanity_status_summary_cert', 'Certificate issued, finishing setup...');
    }
    if (status.nginx_config_at) {
      return t('vanity_status_summary_nginx', 'Requesting certificate...');
    }
    return t('vanity_status_summary_requested', 'Setup starting...');
  };

  const retryDomain = async (fullHost: string) => {
    if (!(await showConfirm(t('vanity_retry_title', 'Retry Domain Setup'), t('vanity_retry_confirm', `Re-run the setup for ${fullHost}? This resets its progress and requests a fresh certificate.`)))) return;
    try {
      await axios.post(`/api/admin/vanity-domain-status/${encodeURIComponent(fullHost)}/retry`);
      showToast(t('vanity_retry_queued', 'Retry queued -- watch the status update over the next ~30s'), 'success');
      fetchData();
    } catch (err: any) {
      showToast(err.response?.data?.error || t('vanity_retry_failed', 'Failed to queue retry'), 'error');
    }
  };

  const removeDomain = async (fullHost: string) => {
    if (!(await showConfirm(t('vanity_remove_title', 'Remove Domain'), t('vanity_remove_confirm', `Tear down ${fullHost}'s nginx config and certificate, and clear its tracked status? The domain's own reservation is not affected.`)))) return;
    try {
      await axios.post(`/api/admin/vanity-domain-status/${encodeURIComponent(fullHost)}/remove`);
      showToast(t('vanity_remove_queued', 'Removal queued'), 'success');
      fetchData();
    } catch (err: any) {
      showToast(err.response?.data?.error || t('vanity_remove_failed', 'Failed to queue removal'), 'error');
    }
  };

  if (loading) {
    return (
      <div className="animate-fade-in">
        <div className="page-header">
          <Skeleton width={260} height={28} />
        </div>
        <div className="card p-xl">
          <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="page-header">
        <h3 className="page-header__title">{t('vanity_status_admin_title', 'Vanity Domain Status')}</h3>
        <p className="page-header__desc">{t('vanity_status_admin_desc', 'Provisioning progress for every custom domain across all users.')}</p>
      </div>

      <div className="card p-0">
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t('search_vanity_domains_placeholder', 'Search domains or owners...')}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={allColumns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
          />
        </div>

        {paginatedItems.length === 0 ? (
          <div className="empty-state p-xl">
            <div className="empty-state__text">{t('no_vanity_domains', 'No custom domain attempts tracked yet.')}</div>
          </div>
        ) : (
          <>
            <div className="table-responsive">
              <table className="w-full">
                <thead>
                  <tr className="border-b text-left">
                    {isColumnVisible('full_host') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('full_host')} aria-sort={getAriaSort('full_host')}>
                        {t('vanity_status_col_domain', 'Domain')}{getSortIndicator('full_host')}
                      </th>
                    )}
                    {isColumnVisible('user_id') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('user_id')} aria-sort={getAriaSort('user_id')}>
                        {t('vanity_status_col_owner', 'Owner')}{getSortIndicator('user_id')}
                      </th>
                    )}
                    <th className="th-col text-center">{t('vanity_status_col_requested', 'Requested')}</th>
                    <th className="th-col text-center">{t('vanity_status_col_nginx', 'Nginx Config')}</th>
                    <th className="th-col text-center">{t('vanity_status_col_cert', 'Cert Issued')}</th>
                    <th className="th-col text-center">{t('vanity_status_col_live', 'Live')}</th>
                    <th className="th-col">{t('vanity_status_col_summary', 'Summary')}</th>
                    <th className="th-col text-right">{t('actions', 'Actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {paginatedItems.map((status) => (
                    <tr key={status.full_host} className="border-b">
                      {isColumnVisible('full_host') && <td className="td-cell font-mono">{status.full_host}</td>}
                      {isColumnVisible('user_id') && <td className="td-cell text-xs text-muted">{status.user_id}</td>}
                      <td className="td-cell text-center">
                        {status.requested_at ? (
                          <StageIcon state="done" title={formatDate(status.requested_at)} />
                        ) : (
                          <StageIcon state="open" title={t('vanity_status_not_reached', 'Not yet reached')} />
                        )}
                      </td>
                      <td className="td-cell text-center">{stageIcon(status, 'nginx_config', status.nginx_config_at)}</td>
                      <td className="td-cell text-center">{stageIcon(status, 'cert_issued', status.cert_issued_at)}</td>
                      <td className="td-cell text-center">{stageIcon(status, 'live', status.live_at)}</td>
                      <td className="td-cell text-sm text-muted">{summaryFor(status)}</td>
                      <td className="td-cell text-right">
                        <ActionMenu buttonTitle={t('actions', 'Actions')}>
                          {(close) => (
                            <>
                              <button
                                className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                style={{ background: 'none', border: 'none' }}
                                onClick={() => { close(); retryDomain(status.full_host); }}
                              >
                                🔄 {t('vanity_action_retry', 'Retry')}
                              </button>
                              <button
                                className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                style={{ background: 'none', border: 'none', color: 'var(--danger)' }}
                                onClick={() => { close(); removeDomain(status.full_host); }}
                              >
                                🗑 {t('vanity_action_remove', 'Remove')}
                              </button>
                            </>
                          )}
                        </ActionMenu>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <DataTablePagination
              currentPage={currentPage}
              totalPages={totalPages}
              pageSize={pageSize}
              totalItems={totalItems}
              onPageChange={setCurrentPage}
            />
          </>
        )}
      </div>
    </div>
  );
}
