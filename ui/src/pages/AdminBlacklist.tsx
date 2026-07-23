import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface BlacklistEntry {
  ip: string;
  reason: string;
  created_at: string;
}

export default function AdminBlacklist() {
  const [entries, setEntries] = useState<BlacklistEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [ipInput, setIpInput] = useState('');
  const [reasonInput, setReasonInput] = useState('');
  const { formatDate } = useSettings();
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();

  const fetchEntries = async () => {
    try {
      const res = await axios.get('/api/admin/blacklist');
      setEntries(res.data || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchEntries();
  }, []);

  const addEntry = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ipInput) return;
    try {
      await axios.post('/api/admin/blacklist', {
        ip: ipInput,
        reason: reasonInput || 'Manual ban'
      });
      setIpInput('');
      setReasonInput('');
      fetchEntries();
      showToast('IP blocked successfully', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || 'Failed to block IP', 'error');
    }
  };

  const removeEntry = async (ip: string) => {
    if (!(await showConfirm('Unblock IP', `Are you sure you want to unblock ${ip}?`))) return;
    try {
      await axios.delete(`/api/admin/blacklist/${encodeURIComponent(ip)}`);
      fetchEntries();
      showToast('IP unblocked successfully', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || 'Failed to unblock IP', 'error');
    }
  };

  const columns: ColumnDef<BlacklistEntry>[] = useMemo(() => [
    { key: 'ip', label: t('tbl_ip_address', 'IP Address'), sortable: true },
    { key: 'reason', label: t('tbl_reason', 'Reason'), sortable: true },
    { key: 'created_at', label: t('tbl_time', 'Time'), sortable: true },
  ], [t]);

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
    getAriaSort
  } = useDataTable<BlacklistEntry>(
    'admin_blacklist',
    entries,
    ['ip', 'reason'],
    columns,
    10
  );

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="page-header">
          <div>
            <Skeleton width={180} height={28} />
          </div>
        </div>

        <div className="card p-xl mb-xl">
          <div className="flex gap-md flex-wrap">
            <Skeleton width="100%" height={40} style={{ flex: '1', minWidth: '150px' }} />
            <Skeleton width="100%" height={40} style={{ flex: '2', minWidth: '200px' }} />
            <Skeleton width={120} height={40} />
          </div>
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={200} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={60} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="50%" height={16} /></td>
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
      <div className="page-header">
        <div>
          <h3 className="page-header__title">{t('ip_blacklist', 'IP Blacklist & WAF Bans')}</h3>
          <p className="page-header__desc">{t('ip_blacklist_desc', 'Manage blocked IP addresses and security enforcement.')}</p>
        </div>
      </div>

      <div className="card p-xl mb-xl">
        <h4 className="text-md fw-bold mb-md">{t('block_new_ip', 'Block IP Address')}</h4>
        <form onSubmit={addEntry} className="flex gap-md items-end flex-wrap">
          <div className="flex-1 min-w-[200px]">
            <label className="input-label text-xs mb-xs">{t('ip_address', 'IP Address')}</label>
            <input
              type="text"
              placeholder="e.g. 192.0.2.1"
              value={ipInput}
              onChange={e => setIpInput(e.target.value)}
              className="input-field w-full"
              required
            />
          </div>
          <div className="flex-2 min-w-[250px]">
            <label className="input-label text-xs mb-xs">{t('reason', 'Reason')}</label>
            <input
              type="text"
              placeholder="e.g. Malicious payload scan"
              value={reasonInput}
              onChange={e => setReasonInput(e.target.value)}
              className="input-field w-full"
            />
          </div>
          <button type="submit" className="btn btn-primary h-[38px] w-auto">
            🚫 {t('block_ip', 'Block IP')}
          </button>
        </form>
      </div>

      <DataTableToolbar
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        searchPlaceholder={t('search_blacklist_placeholder', 'Search blacklisted IPs...')}
        pageSize={pageSize}
        onPageSizeChange={setPageSize}
        columns={columns}
        isColumnVisible={isColumnVisible}
        onToggleColumn={toggleColumn}
      />

      <div className="card p-0">
        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('ip') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('ip')} aria-sort={getAriaSort('ip')}>
                    {t('tbl_ip_address', 'IP Address')}{getSortIndicator('ip')}
                  </th>
                )}
                {isColumnVisible('reason') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('reason')} aria-sort={getAriaSort('reason')}>
                    {t('tbl_reason', 'Reason')}{getSortIndicator('reason')}
                  </th>
                )}
                {isColumnVisible('created_at') && (
                  <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>
                    {t('tbl_time', 'Time')}{getSortIndicator('created_at')}
                  </th>
                )}
                <th className="th-col text-right">{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {paginatedItems.length === 0 ? (
                <tr>
                  <td colSpan={4} className="td-empty">
                    {t('no_blacklist_entries', 'No blacklisted IP addresses.')}
                  </td>
                </tr>
              ) : (
                paginatedItems.map((entry: BlacklistEntry) => (
                  <tr key={entry.ip} className="border-b">
                    {isColumnVisible('ip') && (
                      <td className="td-cell--mono fw-bold">{entry.ip}</td>
                    )}
                    {isColumnVisible('reason') && (
                      <td className="td-cell">{entry.reason}</td>
                    )}
                    {isColumnVisible('created_at') && (
                      <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>{formatDate(entry.created_at)}</td>
                    )}
                    <td className="td-cell text-right">
                      <button
                        className="btn btn-secondary text-xs text-danger py-xs px-md"
                        onClick={() => removeEntry(entry.ip)}
                      >
                        Unblock
                      </button>
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
