import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useTableSort } from '../hooks/useTableSort';
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

  const { items: sortedLinks, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(links, ['email', 'client_ip']);
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
    if (!val) return 'Unused';
    return formatDate(typeof val === 'number' ? new Date(val * 1000) : val);
  };

  return (
    <div>
      <div className="mb-xl">
        <h3 className="page-header__title">Magic Links</h3>
        <p className="page-header__desc">Track pending and unredeemed magic links.</p>
      </div>

      {error ? (
        <div className="alert-banner alert-banner--danger mb-xl">
          {error}
        </div>
      ) : (
        <>
          {links.length > 0 && (
            <div className="search-row">
              <input 
                type="text" 
                placeholder={t('search_magic_links_placeholder', 'Search magic links...')} 
                value={searchQuery} 
                onChange={e => setSearchQuery(e.target.value)}
                className="search-input"
              />
            </div>
          )}
          <div className="card p-0">
            <div className="table-responsive">
              <table className="w-full">
                <thead>
                  <tr className="border-b text-left">
                    <th className="th-col th-col--sortable" onClick={() => requestSort('email')} aria-sort={getAriaSort('email')}>Email{getSortIndicator('email')}</th>
                    <th className="th-col th-col--sortable" onClick={() => requestSort('client_ip')} aria-sort={getAriaSort('client_ip')}>IP Address{getSortIndicator('client_ip')}</th>
                    <th className="th-col th-col--sortable" onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>Expires{getSortIndicator('expires_at')}</th>
                    <th className="th-col th-col--sortable" onClick={() => requestSort('used_at')} aria-sort={getAriaSort('used_at')}>Used At{getSortIndicator('used_at')}</th>
                  </tr>
                </thead>
                <tbody>
                  {links.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="td-empty opacity-60">
                        No magic links found
                      </td>
                    </tr>
                  ) : (
                    sortedLinks.map((l, i) => (
                      <tr key={i} className="border-b">
                        <td className="td-cell fw-medium">{l.email}</td>
                        <td className="td-cell--mono">{l.client_ip}</td>
                        <td className="td-cell">{renderDate(l.expires_at)}</td>
                        <td className="td-cell">{renderDate(l.used_at)}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
