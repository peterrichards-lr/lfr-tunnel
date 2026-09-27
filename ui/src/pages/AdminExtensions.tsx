import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import ActionMenu from '../components/ActionMenu';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface ExtRequest {
  id: string;
  user_email: string;
  subdomain: string;
  domain: string;
  expires_at: string;
  created_at?: string;
  // Which resource this row is about, and whether this gateway will accept a permanent grant
  // for it. Both come from the server so the two portal arms cannot derive them differently
  // (#2264) -- a custom domain is a reservation with an EMPTY subdomain (#1004), and deriving
  // that from `subdomain === ''` in each arm separately is how they came to disagree.
  resource_kind?: 'subdomain' | 'custom_domain';
  permanence_allowed?: boolean;
}

export default function AdminExtensions() {
  const [loadError, setLoadError] = useState('');
  const [requests, setRequests] = useState<ExtRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast } = useUI();

  const columns: ColumnDef<ExtRequest>[] = useMemo(
    () => [
      { key: 'user_email', label: t('email', 'Email'), sortable: true },
      { key: 'resource_kind', label: t('th_type', 'Type'), sortable: true },
      { key: 'subdomain', label: t('subdomain', 'Subdomain'), sortable: true },
      { key: 'domain', label: t('domain', 'Domain'), sortable: true },
      { key: 'expires_at', label: t('expires', 'Expires'), sortable: true },
      {
        key: 'created_at',
        label: t('created_at', 'Created Date'),
        sortable: true,
      },
    ],
    [t],
  );

  const {
    paginatedItems: paginatedRequests,
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
    allColumns,
  } = useDataTable<ExtRequest>(
    'admin_extensions',
    requests,
    ['user_email', 'subdomain', 'domain', 'resource_kind'],
    columns,
    10,
    ['created_at'],
  );

  const fetchRequests = async () => {
    try {
      const res = await axios.get('/api/admin/reservations/extensions');
      setRequests(res.data || []);
    } catch (err: any) {
      console.error(err);
      setLoadError(
        err.response?.data?.error ||
          t(
            'admin_load_failed',
            'Could not load this page. The server may be unreachable — what you see is not current.',
          ),
      );
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchRequests();
  }, []);

  const handleApprove = async (
    id: string,
    days: number,
    permanent: boolean,
  ) => {
    try {
      await axios.post(`/api/admin/reservations/${id}/approve-extension`, {
        days,
        permanent,
      });
      fetchRequests();
      showToast(t('toast_request_approved', 'Request approved.'), 'success');
    } catch (err) {
      console.error(err);
      showToast(t('action_failed', 'Action failed'), 'error');
    }
  };

  const handleReject = async (id: string) => {
    try {
      await axios.post(`/api/admin/reservations/${id}/demote`);
      fetchRequests();
      showToast(
        t(
          'toast_request_rejected',
          'Request rejected; the reservation keeps a normal expiry.',
        ),
        'success',
      );
    } catch (err) {
      console.error(err);
      showToast(t('action_failed', 'Action failed'), 'error');
    }
  };

  if (loading) {
    return (
      <div>
        <div className="page-header mb-xl">
          <div>
            <Skeleton width={200} height={28} />
            <Skeleton width={300} height={16} className="mt-sm" />
          </div>
        </div>
        <div className="card p-xl">
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
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col text-right">
                    <Skeleton width={100} />
                  </th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell">
                      <Skeleton width="90%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="70%" height={16} />
                    </td>
                    <td className="td-cell text-right">
                      <Skeleton width="80%" height={16} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
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

      <div className="page-header mb-xl">
        <div>
          <h1 className="page-header__title">
            {t('extension_requests', 'Extension Requests')}
          </h1>
          <p className="page-header__desc">
            {t(
              'extension_requests_desc',
              'Review and approve reservation extension requests.',
            )}
          </p>
        </div>
      </div>

      <div className="card p-0">
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t(
              'search_extensions_placeholder',
              'Search extension requests...',
            )}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={allColumns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
          />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('user_email') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('user_email')}
                    aria-sort={getAriaSort('user_email')}
                  >
                    {t('email', 'Email')}
                    {getSortIndicator('user_email')}
                  </th>
                )}
                {isColumnVisible('resource_kind') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('resource_kind')}
                    aria-sort={getAriaSort('resource_kind')}
                  >
                    {t('th_type', 'Type')}
                    {getSortIndicator('resource_kind')}
                  </th>
                )}
                {isColumnVisible('subdomain') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('subdomain')}
                    aria-sort={getAriaSort('subdomain')}
                  >
                    {t('subdomain', 'Subdomain')}
                    {getSortIndicator('subdomain')}
                  </th>
                )}
                {isColumnVisible('domain') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('domain')}
                    aria-sort={getAriaSort('domain')}
                  >
                    {t('domain', 'Domain')}
                    {getSortIndicator('domain')}
                  </th>
                )}
                {isColumnVisible('expires_at') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('expires_at')}
                    aria-sort={getAriaSort('expires_at')}
                  >
                    {t('expires', 'Expires')}
                    {getSortIndicator('expires_at')}
                  </th>
                )}
                {isColumnVisible('created_at') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('created_at')}
                    aria-sort={getAriaSort('created_at')}
                  >
                    {t('created_at', 'Created Date')}
                    {getSortIndicator('created_at')}
                  </th>
                )}
                <th className="th-col text-right">{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedRequests.length === 0 ? (
                <tr>
                  <td colSpan={7} className="td-empty">
                    {t(
                      'no_pending_extension_requests',
                      'No pending extension requests.',
                    )}
                  </td>
                </tr>
              ) : (
                paginatedRequests.map((req) => (
                  <tr key={req.id} className="border-b">
                    {isColumnVisible('user_email') && (
                      <td className="td-cell">{req.user_email}</td>
                    )}
                    {isColumnVisible('resource_kind') && (
                      <td className="td-cell text-xs">
                        {req.resource_kind === 'custom_domain'
                          ? t('resource_kind_custom_domain', 'Custom Domain')
                          : t('resource_kind_subdomain', 'Subdomain')}
                      </td>
                    )}
                    {isColumnVisible('subdomain') && (
                      <td className="td-cell font-mono text-xs">
                        {req.resource_kind === 'custom_domain'
                          ? '—'
                          : req.subdomain}
                      </td>
                    )}
                    {isColumnVisible('domain') && (
                      <td className="td-cell font-mono text-xs">
                        {req.domain}
                      </td>
                    )}
                    {isColumnVisible('expires_at') && (
                      <td className="td-cell text-xs text-muted whitespace-nowrap">
                        {req.expires_at ? formatDate(req.expires_at) : 'Never'}
                      </td>
                    )}
                    {isColumnVisible('created_at') && (
                      <td className="td-cell text-xs text-muted whitespace-nowrap">
                        {req.created_at ? formatDate(req.created_at) : '—'}
                      </td>
                    )}
                    <td className="td-cell text-right">
                      <div className="flex gap-sm justify-end">
                        <ActionMenu
                          buttonLabel="Approve"
                          buttonClassName="btn btn-primary px-md py-xs text-xs"
                          buttonTitle="Approve extension"
                        >
                          {(close) => (
                            <>
                              <button
                                className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                onClick={() => {
                                  close();
                                  handleApprove(req.id, 30, false);
                                }}
                              >
                                {t('approve_30_days', 'Approve +30 Days')}
                              </button>
                              {/*
                                Offered only where the gateway will accept it. never_expires
                                governs a permanent grant and AdminApproveExtension answers
                                403 when it is `disabled`, so an unconditional button is one
                                that always errors (#2264).
                              */}
                              {req.permanence_allowed && (
                                <button
                                  className="dropdown-menu-item flex items-center gap-sm text-xs cursor-pointer w-full text-left"
                                  onClick={() => {
                                    close();
                                    handleApprove(req.id, 0, true);
                                  }}
                                >
                                  {t('approve_permanent', 'Approve Permanent')}
                                </button>
                              )}
                            </>
                          )}
                        </ActionMenu>
                        <button
                          className="btn btn-secondary px-md py-xs text-xs"
                          onClick={() => handleReject(req.id)}
                        >
                          {t('reject_request', 'Reject')}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
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
      </div>
    </div>
  );
}
