import { useEffect, useState, useMemo, useCallback } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';

interface BackupInfo {
  filename: string;
  size_bytes: number;
  created_at: string;
}

/**
 * Database Backups (#1567), the V2 counterpart of V1's screen.
 *
 * The API defines what this screen should offer, rather than V1 doing so. There are three
 * endpoints -- list, `POST /api/admin/backups` to trigger one, and
 * `GET /api/admin/backups/download/{name}` to fetch one -- and V1 surfaces only the first. The
 * gap is V1 under-exposing the capability, not V2 over-reaching, so both portals get all three
 * and parity is reached at the API's level rather than at the lower of the two portals.
 *
 * Both write endpoints already audit-log the actor server-side, and the download handler rejects
 * any filename containing `..` or `/`, so no new guard is needed here.
 *
 * Presentation may still differ between the arms, and does -- this uses V2's standard table
 * furniture (search, page size, column toggles) where V1 has a plain sortable table. That is the
 * half of the experiment that is meant to vary.
 */
export default function AdminBackups() {
  const { formatDate } = useSettings();
  const [backups, setBackups] = useState<BackupInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [notice, setNotice] = useState('');
  const { t } = useI18n();

  const fetchBackups = useCallback(async () => {
    try {
      const res = await axios.get('/api/admin/backups');
      setBackups(res.data || []);
      setError('');
    } catch (err: any) {
      // The endpoint returns [] rather than an error when the backups directory does not exist,
      // so reaching here means a real failure rather than "none yet".
      setError(
        err.response?.data?.error ||
          err.message ||
          t('backups_load_failed', 'Failed to load backups.'),
      );
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    fetchBackups();
  }, [fetchBackups]);

  const handleCreateBackup = async () => {
    setCreating(true);
    setNotice('');
    try {
      await axios.post('/api/admin/backups');
      setNotice(t('backup_created', 'Backup created.'));
      // Refetch rather than appending locally: the server names the file, so the row cannot be
      // constructed here without guessing at it.
      await fetchBackups();
    } catch (err: any) {
      setError(
        err.response?.data?.error ||
          err.message ||
          t('backup_create_failed', 'Failed to create backup.'),
      );
    } finally {
      setCreating(false);
    }
  };

  const columns: ColumnDef<BackupInfo>[] = useMemo(
    () => [
      { key: 'filename', label: t('th_filename', 'Filename'), sortable: true },
      { key: 'size_bytes', label: t('th_size', 'Size'), sortable: true },
      {
        key: 'created_at',
        label: t('th_created_utc', 'Created (UTC)'),
        sortable: true,
      },
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
  } = useDataTable<BackupInfo>(
    'admin_backups',
    backups,
    ['filename'],
    columns,
    10,
    [],
  );

  if (loading) {
    return (
      <div className="animate-fade-in">
        <div className="mb-xl">
          <Skeleton width={200} height={28} />
          <Skeleton width={320} height={16} className="mt-sm" />
        </div>
        <div className="card p-xl">
          <Skeleton width="100%" height={120} />
        </div>
      </div>
    );
  }

  // KB to one decimal, as V1 renders it. Left as KB rather than scaled to MB/GB so the two
  // portals show the same number for the same file.
  const renderSize = (bytes: number) => `${(bytes / 1024).toFixed(1)} KB`;

  return (
    <div className="animate-fade-in">
      <div className="page-header">
        <div>
          <h1 className="page-header__title">
            {t('sidebar_backups', 'Database Backups')}
          </h1>
          <p className="page-header__desc">
            {t(
              'backups_desc',
              'Automatic snapshots of the gateway database, newest first.',
            )}
          </p>
        </div>
        <button
          type="button"
          className="btn btn-primary w-auto"
          onClick={handleCreateBackup}
          disabled={creating}
        >
          {creating
            ? t('backup_creating', 'Creating...')
            : t('create_backup', 'Create Backup')}
        </button>
      </div>

      {notice && (
        <div className="alert-banner alert-banner--success mb-xl" role="status">
          {notice}
        </div>
      )}

      {/* Carried across from V1 because the UI is the only place the restore procedure is
          written down, and it is not something to discover during an incident. */}
      <div className="alert-banner alert-banner--info mb-xl">
        <div>
          <strong>
            🛡 {t('backups_restore_cli_title', 'Restore via CLI only.')}
          </strong>{' '}
          {t(
            'backups_restore_cli_body',
            'To restore from a backup, SSH into the VPS and run the wrapper script:',
          )}
          <div className="mt-sm">
            <code className="text-xs font-mono">
              sudo ./scripts/common/restore-with-maintenance.sh
            </code>
          </div>
          <div className="text-xs opacity-80 mt-sm">
            {t(
              'backups_restore_cli_note',
              'This wrapper automatically coordinates Nginx maintenance mode, stops the daemon, restores the database backup, and restarts the service.',
            )}
          </div>
        </div>
      </div>

      {error ? (
        <div className="alert-banner alert-banner--danger mb-xl">{error}</div>
      ) : (
        <div className="card p-0">
          <div className="p-md border-b">
            <DataTableToolbar
              searchQuery={searchQuery}
              onSearchChange={setSearchQuery}
              searchPlaceholder={t(
                'search_backups_placeholder',
                'Search backups...',
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
                  {isColumnVisible('filename') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('filename')}
                      aria-sort={getAriaSort('filename')}
                    >
                      {t('th_filename', 'Filename')}
                      {getSortIndicator('filename')}
                    </th>
                  )}
                  {isColumnVisible('size_bytes') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('size_bytes')}
                      aria-sort={getAriaSort('size_bytes')}
                    >
                      {t('th_size', 'Size')}
                      {getSortIndicator('size_bytes')}
                    </th>
                  )}
                  {isColumnVisible('created_at') && (
                    <th
                      className="th-col th-col--sortable"
                      onClick={() => requestSort('created_at')}
                      aria-sort={getAriaSort('created_at')}
                    >
                      {t('th_created_utc', 'Created (UTC)')}
                      {getSortIndicator('created_at')}
                    </th>
                  )}
                  {/* Not registered as a ColumnDef: those drive sorting and the column-toggle
                      menu, and both require a key that is a real field of the row. */}
                  <th className="th-col">{t('th_actions', 'Actions')}</th>
                </tr>
              </thead>
              <tbody>
                {paginatedItems.length === 0 ? (
                  <tr>
                    <td className="td-cell text-center text-muted" colSpan={4}>
                      {t(
                        'backups_empty',
                        'No backups found yet. The first backup runs on server startup.',
                      )}
                    </td>
                  </tr>
                ) : (
                  paginatedItems.map((b) => (
                    <tr key={b.filename} className="border-b">
                      {isColumnVisible('filename') && (
                        <td className="td-cell font-mono text-xs">
                          {b.filename}
                        </td>
                      )}
                      {isColumnVisible('size_bytes') && (
                        <td className="td-cell">{renderSize(b.size_bytes)}</td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell">{formatDate(b.created_at)}</td>
                      )}
                      <td className="td-cell">
                        {/* A real link rather than a fetch: the endpoint sets
                              Content-Disposition: attachment, so the browser saves the file
                              instead of the page having to assemble a blob. */}
                        <a
                          className="btn btn-secondary w-auto text-xs"
                          href={`/api/admin/backups/download/${encodeURIComponent(b.filename)}`}
                        >
                          {t('download', 'Download')}
                        </a>
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
            totalItems={totalItems}
            pageSize={pageSize}
            onPageChange={setCurrentPage}
          />
        </div>
      )}
    </div>
  );
}
