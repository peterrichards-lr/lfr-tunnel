import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

/**
 * The admin side of never_expires `approval` for Personal Access Tokens (#2280).
 *
 * #2275 built the lifecycle and routed it; #2279 made `approval` selectable and showed the holder
 * their own request as pending or denied. Neither arm ever called the admin queue, so requests
 * accumulated with no way to act on one except the API directly -- or "Extend Permanent" on the
 * token list, which grants without reference to the request and leaves it pending forever.
 *
 * Modelled on AdminExtensions, deliberately: these are two halves of one idea and an admin should
 * not have to learn a second set of controls for the second one.
 */
interface PermanenceRequest {
  // int64 on the wire, so a number here -- unlike the reservation queue, whose ids are strings.
  id: number;
  user_email: string;
  name: string;
  token_prefix: string;
  expires_at?: string;
  created_at: string;
  permanence_state?: string;
}

export default function AdminTokenPermanence() {
  const [loadError, setLoadError] = useState('');
  const [requests, setRequests] = useState<PermanenceRequest[]>([]);
  const [loading, setLoading] = useState(true);
  // Which row is mid-decision. The server refuses a second decision with 409 (the state is
  // claimed before the expiry is touched), so a double click cannot corrupt anything -- but it
  // can show the admin an error for work that actually succeeded, which reads like a failure.
  const [deciding, setDeciding] = useState<number | null>(null);
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast } = useUI();

  const columns: ColumnDef<PermanenceRequest>[] = useMemo(
    () => [
      { key: 'user_email', label: t('email', 'Email'), sortable: true },
      { key: 'name', label: t('token_name', 'Token Name'), sortable: true },
      { key: 'token_prefix', label: t('th_prefix', 'Prefix'), sortable: true },
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
  } = useDataTable<PermanenceRequest>(
    'admin_token_permanence',
    requests,
    ['user_email', 'name', 'token_prefix'],
    columns,
    10,
    ['created_at'],
  );

  const fetchRequests = async () => {
    try {
      const res = await axios.get('/api/admin/tokens/permanence-requests');
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

  /**
   * Grant or deny one request.
   *
   * `grant` is sent explicitly on both paths. Server-side it is a POINTER, so an absent field is
   * a 400 rather than a silent denial -- which means omitting it to mean "no" would look like it
   * worked in the arm and fail at the gateway.
   *
   * Reloads the QUEUE rather than patching the row out locally: the row's absence is the server's
   * answer, and a local removal would show an admin a cleared queue after a decision the gateway
   * refused with 409.
   */
  const decide = async (id: number, grant: boolean) => {
    setDeciding(id);
    try {
      await axios.post(`/api/admin/tokens/${id}/permanence`, { grant });
      await fetchRequests();
      showToast(
        grant
          ? t(
              'toast_permanence_granted',
              'Granted; the token no longer expires.',
            )
          : t('toast_permanence_denied', 'Denied; the token keeps its expiry.'),
        'success',
      );
    } catch (err: any) {
      console.error(err);
      // 409 is its own message. It means somebody else decided this request first, and telling
      // an admin "Action failed" for work that is already done sends them looking for a fault.
      if (err.response?.status === 409) {
        showToast(
          t(
            'toast_permanence_already_decided',
            'Already decided by someone else. The queue has been refreshed.',
          ),
          'error',
        );
        await fetchRequests();
      } else {
        showToast(t('action_failed', 'Action failed'), 'error');
      }
    } finally {
      setDeciding(null);
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
                  <th className="th-col">{t('email', 'Email')}</th>
                  <th className="th-col">{t('token_name', 'Token Name')}</th>
                  <th className="th-col">{t('th_prefix', 'Prefix')}</th>
                  <th className="th-col">{t('expires', 'Expires')}</th>
                  <th className="th-col">{t('created_at', 'Created Date')}</th>
                  <th className="th-col text-right">
                    {t('actions', 'Actions')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell">
                      <Skeleton width="70%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="50%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
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
            {t('token_permanence_requests', 'Token Permanence Requests')}
          </h1>
          <p className="page-header__desc">
            {t(
              'token_permanence_requests_desc',
              'Holders have asked for these tokens never to expire. Granting removes the expiry; denying keeps it.',
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
              'search_token_permanence_placeholder',
              'Search permanence requests...',
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
                {isColumnVisible('name') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('name')}
                    aria-sort={getAriaSort('name')}
                  >
                    {t('token_name', 'Token Name')}
                    {getSortIndicator('name')}
                  </th>
                )}
                {isColumnVisible('token_prefix') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('token_prefix')}
                    aria-sort={getAriaSort('token_prefix')}
                  >
                    {t('th_prefix', 'Prefix')}
                    {getSortIndicator('token_prefix')}
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
                  <td colSpan={6} className="td-cell text-center text-muted">
                    {t(
                      'no_pending_permanence_requests',
                      'No pending permanence requests.',
                    )}
                  </td>
                </tr>
              ) : (
                paginatedRequests.map((req) => (
                  <tr key={req.id} className="border-b">
                    {isColumnVisible('user_email') && (
                      <td className="td-cell">{req.user_email}</td>
                    )}
                    {isColumnVisible('name') && (
                      <td className="td-cell">{req.name}</td>
                    )}
                    {isColumnVisible('token_prefix') && (
                      <td className="td-cell font-mono">{req.token_prefix}</td>
                    )}
                    {isColumnVisible('expires_at') && (
                      <td className="td-cell">
                        {req.expires_at
                          ? formatDate(req.expires_at)
                          : t('expiry_never', 'Never')}
                      </td>
                    )}
                    {isColumnVisible('created_at') && (
                      <td className="td-cell">{formatDate(req.created_at)}</td>
                    )}
                    <td className="td-cell text-right">
                      <div className="flex gap-sm justify-end">
                        <button
                          className="btn btn-primary px-md py-xs text-xs"
                          disabled={deciding === req.id}
                          onClick={() => decide(req.id, true)}
                        >
                          {t('grant_permanence', 'Grant')}
                        </button>
                        <button
                          className="btn btn-secondary px-md py-xs text-xs"
                          disabled={deciding === req.id}
                          onClick={() => decide(req.id, false)}
                        >
                          {t('deny_permanence', 'Deny')}
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
