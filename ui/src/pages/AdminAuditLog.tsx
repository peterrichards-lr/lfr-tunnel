import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';

// 200 is what the repository allows per request, so that is the page size. The ceiling stops an
// ever-growing log being pulled into the browser wholesale; the export endpoint covers the rest.
const AUDIT_PAGE = 200;
const AUDIT_MAX = 2000;
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';

interface AuditEvent {
  id: number;
  actor_id: string;
  action: string;
  target_type: string;
  target_id: string;
  ip_address: string;
  created_at: string;
  details: string;
}

// The sort the export reproduces on the server (pkg/server/audit_export.go) and V1 already used:
// lowercased values, compared by code unit. localeCompare -- the table default -- depends on the
// browser's collation, which no server can match, so a row number would not mean the same row
// in the table and the export (#2464).
const compareLowercase = (a: string, b: string) => {
  const x = a.toLowerCase();
  const y = b.toLowerCase();
  return x < y ? -1 : x > y ? 1 : 0;
};

export default function AdminAuditLog() {
  const [loadError, setLoadError] = useState('');
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(true);
  const [exportRows, setExportRows] = useState('');
  const [exportAnonymise, setExportAnonymise] = useState(true);
  const [exportError, setExportError] = useState('');
  const [exportStatus, setExportStatus] = useState('');
  const { formatDate } = useSettings();
  const { t } = useI18n();

  const fetchEvents = async () => {
    try {
      // Paged, because one request cannot return everything: the repository caps any single call
      // at 200 and defaulted to 50 when no limit was given, so this screen showed the 50 most
      // recent entries and offered no way to reach the 51st (#1618).
      //
      // Bounded rather than unbounded: an audit log grows without limit, and pulling all of it
      // into the browser would eventually be worse than truncating. When the ceiling is reached
      // the page says so and points at the export, rather than stopping silently as before.
      const all: AuditEvent[] = [];
      let hitCeiling = false;
      for (let offset = 0; offset < AUDIT_MAX; offset += AUDIT_PAGE) {
        const res = await axios.get(
          `/api/admin/audit?limit=${AUDIT_PAGE}&offset=${offset}`,
        );
        const page: AuditEvent[] = Array.isArray(res.data)
          ? res.data
          : res.data.events || [];
        all.push(...page);
        if (page.length < AUDIT_PAGE) break;
        if (all.length >= AUDIT_MAX) {
          hitCeiling = true;
          break;
        }
      }
      setEvents(all);
      setTruncated(hitCeiling);
    } catch (e: any) {
      // An empty table is indistinguishable from "no results" (#1868). Say which.
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

  useEffect(() => {
    fetchEvents();
  }, []);

  const columns: ColumnDef<AuditEvent>[] = useMemo(
    () => [
      { key: 'created_at', label: t('tbl_time', 'Time'), sortable: true },
      { key: 'actor_id', label: t('tbl_actor', 'Actor'), sortable: true },
      { key: 'action', label: t('tbl_action', 'Action'), sortable: true },
      {
        key: 'target_id',
        label: t('tbl_resource', 'Resource'),
        sortable: true,
      },
      {
        key: 'ip_address',
        label: t('tbl_ip_address', 'IP Address'),
        sortable: true,
      },
      { key: 'details', label: t('tbl_details', 'Details'), sortable: true },
    ],
    [t],
  );

  const {
    paginatedItems,
    searchQuery,
    setSearchQuery,
    pageSize,
    setPageSize,
    currentPage,
    setCurrentPage,
    totalPages,
    totalItems,
    isColumnVisible,
    toggleColumn,
    requestSort,
    getSortIndicator,
    getAriaSort,
    sortConfig,
  } = useDataTable<AuditEvent>(
    'admin_audit',
    events,
    // The same fields the server's export search matches (auditSearchFields).
    ['actor_id', 'action', 'target_type', 'target_id', 'ip_address', 'details'],
    columns,
    10,
    ['ip_address'],
    undefined,
    undefined,
    'all',
    compareLowercase,
  );

  // Asks the server how many rows the export holds first, so a selection it cannot apply is
  // explained under the field rather than downloaded as an error, then lets the browser fetch the
  // file itself -- with anonymise on, the raw values never pass through this page (#2464).
  const handleExport = async () => {
    setExportError('');
    setExportStatus('');
    const params = new URLSearchParams({ q: searchQuery, rows: exportRows });
    if (sortConfig) {
      params.set('sort', String(sortConfig.key));
      params.set('dir', sortConfig.direction);
    }
    try {
      const res = await axios.get(`/api/admin/audit/export/preview?${params}`);
      setExportStatus(
        t('audit_export_status', 'Exporting {0} of {1} rows')
          .replace('{0}', String(res.data.selected))
          .replace('{1}', String(res.data.total)),
      );
    } catch (e: any) {
      setExportError(
        e.response?.data?.error ||
          t('audit_export_failed', 'The export could not be prepared.'),
      );
      return;
    }
    if (exportAnonymise) params.set('anonymise', '1');
    window.location.href = `/api/admin/audit/export?${params}`;
  };

  if (loading) {
    return (
      <div className="animate-fade-in">
        <div className="page-header">
          <div>
            <Skeleton width={180} height={28} />
            <Skeleton width={280} height={16} className="mt-sm" />
          </div>
          <Skeleton width={100} height={40} />
        </div>

        <div className="card p-xl mb-xl">
          <div className="flex gap-md items-center">
            <Skeleton width="100%" height={40} className="max-w-sm" />
          </div>
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col">
                    <Skeleton width={100} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={120} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={90} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={180} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={60} />
                  </th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell">
                      <Skeleton width="90%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="85%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="60%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="70%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="80%" height={16} />
                    </td>
                    <td className="td-cell">
                      <Skeleton width="50%" height={16} />
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

      <div className="page-header">
        <div>
          <h1 className="page-header__title">
            {t('system_audit_log', 'System Audit Log')}
          </h1>
          <p className="page-header__desc">
            {t(
              'audit_log_desc',
              'Immutable record of administrative and security events.',
            )}
          </p>
          {/* Said out loud when the ceiling is reached. The previous behaviour was to stop at 50
              with no indication, so an absent entry and an entry beyond the cut looked identical
              -- which is the part that made this worse than merely showing fewer rows (#1618). */}
          {truncated && (
            <p className="text-sm text-warning mt-sm" role="status">
              {t(
                'audit_log_truncated',
                'Showing the most recent {0} events. Use Export for the full history.',
              ).replace('{0}', String(AUDIT_MAX))}
            </p>
          )}
          <p
            id="audit-export-error"
            className="text-danger text-sm mt-sm mb-0"
            role="alert"
            hidden={!exportError}
          >
            {exportError}
          </p>
          {exportStatus && (
            <p className="text-sm mt-sm" role="status">
              {exportStatus}
            </p>
          )}
        </div>
        <div className="flex gap-md items-center print-hide">
          <input
            type="text"
            className="form-control w-auto"
            value={exportRows}
            onChange={(e) => setExportRows(e.target.value)}
            placeholder={t(
              'audit_export_rows_placeholder',
              'Rows, e.g. 4-9,12,18',
            )}
            aria-label={t(
              'aria_audit_export_rows',
              'Rows to export (optional)',
            )}
            aria-invalid={exportError ? true : undefined}
            aria-describedby="audit-export-error"
          />
          <label className="form-label m-0 inline-flex items-center gap-sm whitespace-nowrap">
            <input
              type="checkbox"
              checked={exportAnonymise}
              onChange={(e) => setExportAnonymise(e.target.checked)}
            />
            {t(
              'audit_export_anonymise',
              'Anonymise emails, subdomains and IPs',
            )}
          </label>
          <button
            type="button"
            onClick={handleExport}
            className="btn btn-secondary w-auto inline-flex items-center gap-sm whitespace-nowrap"
          >
            📥 {t('export_csv', 'Export CSV')}
          </button>
        </div>
      </div>

      <div className="card p-0">
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t(
              'search_audit_logs_placeholder',
              'Search audit logs...',
            )}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={columns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
          />
        </div>
        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('created_at') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('created_at')}
                    aria-sort={getAriaSort('created_at')}
                  >
                    {t('tbl_time', 'Time')}
                    {getSortIndicator('created_at')}
                  </th>
                )}
                {isColumnVisible('actor_id') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('actor_id')}
                    aria-sort={getAriaSort('actor_id')}
                  >
                    {t('tbl_actor', 'Actor')}
                    {getSortIndicator('actor_id')}
                  </th>
                )}
                {isColumnVisible('action') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('action')}
                    aria-sort={getAriaSort('action')}
                  >
                    {t('tbl_action', 'Action')}
                    {getSortIndicator('action')}
                  </th>
                )}
                {isColumnVisible('target_id') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('target_id')}
                    aria-sort={getAriaSort('target_id')}
                  >
                    {t('tbl_resource', 'Resource')}
                    {getSortIndicator('target_id')}
                  </th>
                )}
                {isColumnVisible('ip_address') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('ip_address')}
                    aria-sort={getAriaSort('ip_address')}
                  >
                    {t('tbl_ip_address', 'IP Address')}
                    {getSortIndicator('ip_address')}
                  </th>
                )}
                {isColumnVisible('details') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('details')}
                    aria-sort={getAriaSort('details')}
                  >
                    {t('tbl_details', 'Details')}
                    {getSortIndicator('details')}
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {paginatedItems.length === 0 ? (
                <tr>
                  <td colSpan={6} className="td-empty">
                    {t('no_audit_logs', 'No audit logs available.')}
                  </td>
                </tr>
              ) : (
                paginatedItems.map((e: AuditEvent, idx: number) => (
                  <tr key={e.id || idx} className="border-b">
                    {isColumnVisible('created_at') && (
                      <td className="td-cell whitespace-nowrap">
                        {formatDate(e.created_at)}
                      </td>
                    )}
                    {isColumnVisible('actor_id') && (
                      <td className="td-cell">{e.actor_id}</td>
                    )}
                    {isColumnVisible('action') && (
                      <td className="td-cell">
                        <span className="badge badge-info">{e.action}</span>
                      </td>
                    )}
                    {isColumnVisible('target_id') && (
                      <td className="td-cell">
                        {e.target_type && e.target_id ? (
                          <span className="text-sm">
                            <strong className="opacity-60">
                              {e.target_type}:
                            </strong>{' '}
                            {e.target_id}
                          </span>
                        ) : (
                          e.target_id || '-'
                        )}
                      </td>
                    )}
                    {isColumnVisible('ip_address') && (
                      <td className="td-cell--mono">{e.ip_address}</td>
                    )}
                    {isColumnVisible('details') && (
                      <td className="td-cell--mono text-2xs text-muted overflow-auto max-w-xs">
                        {e.details}
                      </td>
                    )}
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
