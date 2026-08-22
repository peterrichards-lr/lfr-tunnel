import type { ColumnDef, StatusOption } from '../hooks/useDataTable';
import DataTableToolbar from './DataTableToolbar';
import DataTablePagination from './DataTablePagination';
import { useI18n } from '../contexts/I18nContext';

export interface ReservationRow {
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
  computed_status: string;
}

// The bag of state/handlers useDataTable returns -- passed straight through from
// ReservationsPanel, which owns the two useDataTable() calls (one per table). This
// component stays a plain presentational one so it never calls a hook itself, which
// would break if the two tables ever needed different hook-call counts.
interface DataTableBag {
  searchQuery: string;
  setSearchQuery: (v: string) => void;
  statusFilter: string;
  setStatusFilter: (v: string) => void;
  pageSize: number;
  setPageSize: (n: number) => void;
  currentPage: number;
  setCurrentPage: (n: number) => void;
  totalPages: number;
  totalItems: number;
  isColumnVisible: (key: string) => boolean;
  toggleColumn: (key: string) => void;
  requestSort: (key: keyof ReservationRow & string) => void;
  getSortIndicator: (key: keyof ReservationRow & string) => string | null;
  getAriaSort: (key: keyof ReservationRow & string) => 'ascending' | 'descending' | 'none' | undefined;
}

interface ReservationsTableProps extends DataTableBag {
  title: string;
  emptyMessage: string;
  searchPlaceholder: string;
  paginatedItems: ReservationRow[];
  columns: ColumnDef<ReservationRow>[];
  // Which real field identifies a row -- 'subdomain' for the Registered Subdomains
  // table, 'domain' for Custom Domains (every row there has subdomain === '', so
  // sorting/hiding by 'subdomain' would be meaningless there; 'domain' is the field
  // that actually varies). Whichever it is, it's always the first column and always
  // renders the computed host link + copy/CLI buttons below.
  primaryColumnKey: 'subdomain' | 'domain';
  statusOptions: StatusOption[];
  formatDate: (d: string) => string;
  copyText: (text: string, message: string) => void;
  requestExtension: (id: string) => void;
  openAcModal: (r: ReservationRow) => void;
  deleteReservation: (id: string) => void;
  totalUnfilteredCount: number;
  // Anchor target, so the Reservations Overview can link straight down to this table.
  id?: string;
}

export default function ReservationsTable({
  id,
  title,
  emptyMessage,
  searchPlaceholder,
  paginatedItems,
  columns,
  primaryColumnKey,
  statusOptions,
  formatDate,
  copyText,
  requestExtension,
  openAcModal,
  deleteReservation,
  totalUnfilteredCount,
  searchQuery,
  setSearchQuery,
  statusFilter,
  setStatusFilter,
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
}: ReservationsTableProps) {
  const { t } = useI18n();

  return (
    <div id={id} className="card mb-xl scroll-target">
      <div className="section-header mb-md">
        <h3 className="section-title">{title}</h3>
      </div>

      {totalUnfilteredCount > 0 && (
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={searchPlaceholder}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={columns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
            statusFilter={statusFilter}
            onStatusFilterChange={setStatusFilter}
            statusOptions={statusOptions}
          />
        </div>
      )}

      {totalUnfilteredCount === 0 ? (
        <div className="empty-state p-xl">
          <div className="empty-state__text">{emptyMessage}</div>
        </div>
      ) : (
        <>
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  {columns.map(col => (
                    isColumnVisible(col.key) && (
                      <th
                        key={col.key}
                        className={col.sortable ? 'th-col th-col--sortable' : 'th-col'}
                        onClick={col.sortable ? () => requestSort(col.key) : undefined}
                        aria-sort={col.sortable ? getAriaSort(col.key) : undefined}
                      >
                        {col.label}{col.sortable ? getSortIndicator(col.key) : null}
                      </th>
                    )
                  ))}
                  <th className="th-col">{t('actions', 'Actions')}</th>
                </tr>
              </thead>
              <tbody>
                {paginatedItems.map(r => {
                  // Custom-domain reservations have an empty subdomain (server.go sets
                  // SubdomainPrefix = "" for these) -- plain `${subdomain}.${domain}` then
                  // leaves a leading dot ("`.dev.solaramoto.com`"), which browsers don't treat
                  // as the bare domain. Omit the separator when there's no prefix to join.
                  const host = r.subdomain ? `${r.subdomain}.${r.domain}` : r.domain;
                  const cliCommand = r.subdomain
                    ? `lfr-tunnel -subdomain ${r.subdomain} -server ${window.location.origin}`
                    : `lfr-tunnel -domain ${r.domain} -server ${window.location.origin}`;
                  const isExpired = r.expires_at && new Date(r.expires_at) < new Date();
                  const canExtend = !!(r.expires_at && !r.extension_requested && !isExpired);
                  return (
                    <tr key={r.id} className="border-b">
                      {isColumnVisible(primaryColumnKey) && (
                        <td className="td-cell">
                          <div className="flex items-center gap-sm">
                            <a href={`https://${host}`} target="_blank" rel="noreferrer" className="text-primary fw-semibold no-underline font-mono text-base">
                              {host}
                            </a>
                            <button
                              onClick={() => copyText(host, 'Host copied to clipboard')}
                              className="btn-icon text-muted cursor-pointer text-base"
                              style={{ background: 'none', border: 'none', padding: '2px' }}
                              title="Copy Host"
                            >
                              📋
                            </button>
                            <button
                              onClick={() => copyText(cliCommand, 'CLI command copied')}
                              className="btn-icon text-muted cursor-pointer text-base"
                              style={{ background: 'none', border: 'none', padding: '2px' }}
                              title="Copy CLI Connection Command"
                            >
                              🔌
                            </button>
                          </div>
                          {r.access_mode && r.access_mode !== 'public' && (
                            <span className="badge badge-warning text-2xs mt-xs inline-block">
                              {r.access_mode === 'passcode' ? '🔑 Passcode' : '🛡 IP Whitelist'}
                            </span>
                          )}
                        </td>
                      )}
                      {isColumnVisible('status') && (
                        <td className="td-cell">
                          {isExpired ? (
                            <span className="badge badge-danger">quarantined</span>
                          ) : r.extension_requested ? (
                            <span className="badge badge-warning">extension requested</span>
                          ) : (
                            <span className="badge badge-success">active</span>
                          )}
                        </td>
                      )}
                      {isColumnVisible('expires_at') && (
                        <td className="td-cell">
                          {r.expires_at ? formatDate(r.expires_at) : 'Never (Permanent)'}
                        </td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell whitespace-nowrap">
                          {r.created_at ? formatDate(r.created_at) : '—'}
                        </td>
                      )}
                      <td className="td-cell">
                        <div className="flex gap-sm">
                          {canExtend && (
                            <button
                              type="button"
                              className="btn btn-secondary py-xs px-sm text-xs"
                              onClick={() => requestExtension(r.id)}
                            >
                              Extend
                            </button>
                          )}
                          <button
                            type="button"
                            className="btn btn-secondary py-xs px-sm text-xs"
                            title={t('access_control', 'Access Control')}
                            aria-label={t('access_control', 'Access Control')}
                            onClick={() => openAcModal(r)}
                          >
                            🔒
                          </button>
                          <button type="button" className="btn btn-danger py-xs px-sm text-xs" onClick={() => deleteReservation(r.id)}>
                            {t('release', 'Release')}
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
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
  );
}
