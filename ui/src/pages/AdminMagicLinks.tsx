import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';
import Skeleton from '../components/Skeleton';
import { useI18n } from '../contexts/I18nContext';

interface MagicLink {
  email: string;
  client_ip: string;
  expires_at: number | string;
  used_at: number | string | null;
}

export default function AdminMagicLinks() {
  const { formatDate } = useSettings();
  const [links, setLinks] = useState<MagicLink[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const { t } = useI18n();

  useEffect(() => {
    const fetchLinks = async () => {
      try {
        const res = await axios.get('/api/admin/magic-links');
        setLinks(res.data || []);
      } catch (err: any) {
        setError(err.response?.data?.error || err.message || 'Failed to load magic links');
      } finally {
        setLoading(false);
      }
    };
    fetchLinks();
  }, []);

  const columns: ColumnDef<MagicLink>[] = useMemo(() => [
    { key: 'email', label: t('email', 'Email'), sortable: true },
    { key: 'client_ip', label: t('client_ip', 'Client IP'), sortable: true },
    { key: 'expires_at', label: t('expires_at', 'Expires At'), sortable: true },
    { key: 'used_at', label: t('status', 'Status / Used At'), sortable: true },
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
  } = useDataTable<MagicLink>(
    'admin_magic_links',
    links,
    ['email', 'client_ip'],
    columns,
    10
  );

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={280} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl mb-xl">
          <div className="flex gap-md items-center">
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(3)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }

  const renderDate = (val: number | string | null | undefined) => {
    if (!val) return <span className="badge badge-info">{t('unused', 'Unused')}</span>;
    return formatDate(typeof val === 'number' ? new Date(val * 1000) : val);
  };

  return (
    <div>
      <div className="mb-xl">
        <h3 className="page-header__title">{t('magic_links', 'Magic Links')}</h3>
        <p className="page-header__desc">{t('magic_links_desc', 'Track pending and unredeemed magic links.')}</p>
      </div>

      {error ? (
        <div className="alert-banner alert-banner--danger mb-xl">
          {error}
        </div>
      ) : (
        <>
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t('search_magic_links_placeholder', 'Search magic links...')}
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
                    {isColumnVisible('email') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('email')} aria-sort={getAriaSort('email')}>
                        {t('email', 'Email')}{getSortIndicator('email')}
                      </th>
                    )}
                    {isColumnVisible('client_ip') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>
                        {t('client_ip', 'Client IP')}{getSortIndicator('client_ip')}
                      </th>
                    )}
                    {isColumnVisible('expires_at') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>
                        {t('expires_at', 'Expires At')}{getSortIndicator('expires_at')}
                      </th>
                    )}
                    {isColumnVisible('used_at') && (
                      <th className="th-col th-col--sortable" onClick={() => requestSort('used_at')} aria-sort={getAriaSort('used_at')}>
                        {t('status', 'Status / Used At')}{getSortIndicator('used_at')}
                      </th>
                    )}
                  </tr>
                </thead>
                <tbody>
                  {paginatedItems.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="td-empty">
                        {t('no_magic_links', 'No magic links found.')}
                      </td>
                    </tr>
                  ) : (
                    paginatedItems.map((item: MagicLink, idx: number) => (
                      <tr key={idx} className="border-b">
                        {isColumnVisible('email') && (
                          <td className="td-cell fw-medium">{item.email}</td>
                        )}
                        {isColumnVisible('client_ip') && (
                          <td className="td-cell--mono">{item.client_ip || '-'}</td>
                        )}
                        {isColumnVisible('expires_at') && (
                          <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>
                            {formatDate(typeof item.expires_at === 'number' ? new Date(item.expires_at * 1000) : item.expires_at)}
                          </td>
                        )}
                        {isColumnVisible('used_at') && (
                          <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>
                            {renderDate(item.used_at)}
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
        </>
      )}
    </div>
  );
}
