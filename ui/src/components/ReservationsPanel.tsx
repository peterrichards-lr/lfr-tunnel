import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';

import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from './Skeleton';
import { useUI } from '../contexts/UIContext';

interface Reservation {
  id: string;
  subdomain: string;
  domain: string;
  status: string;
  expires_at?: string;
  extension_requested?: boolean;
}

export default function ReservationsPanel() {
  const { t } = useI18n();
  const { showToast, showConfirm } = useUI();
  const [reservations, setReservations] = useState<Reservation[]>([]);
  const { formatDate } = useSettings();
  const [limit, setLimit] = useState(0);
  const [used, setUsed] = useState(0);
  const [loading, setLoading] = useState(true);

  const [domains, setDomains] = useState<string[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [subdomainInput, setSubdomainInput] = useState('');
  const [subdomainStyle, setSubdomainStyle] = useState('liferay');

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
    } catch {
      showToast(t('error_fetch_reservations', 'Failed to load reservations'), 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
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

  const copyText = async (text: string, message: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showToast(message, 'success');
    } catch {
      showToast('Failed to copy to clipboard', 'error');
    }
  };

  const { items: sortedReservations, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(reservations, ['subdomain', 'domain', 'status']);
  if (loading) {
    return (
      <div className="card" style={{ marginBottom: '24px' }}>
        <div style={{ marginBottom: '16px' }}>
          <Skeleton width={200} height={24} />
        </div>
        <div style={{ marginBottom: '24px' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '8px' }}>
            <Skeleton width={120} height={16} />
            <Skeleton width={80} height={16} />
          </div>
          <Skeleton width="100%" height={8} borderRadius={4} />
        </div>
        <div style={{ display: 'flex', gap: '8px', marginBottom: '24px' }}>
          <Skeleton width="100%" height={40} style={{ flex: '1', minWidth: '150px' }} />
          <Skeleton width="100%" height={40} style={{ flex: '1', minWidth: '150px' }} />
          <Skeleton width={80} height={40} />
          <Skeleton width={80} height={40} />
        </div>
        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)' }}>
                <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={120} /></th>
                <th style={{ padding: '12px 16px' }}><Skeleton width={80} /></th>
              </tr>
            </thead>
            <tbody>
              {[...Array(3)].map((_, i) => (
                <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '16px' }}><Skeleton width="80%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width={50} height={20} borderRadius={10} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width="70%" height={16} /></td>
                  <td style={{ padding: '16px' }}><Skeleton width={60} height={28} /></td>
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


  return (
    <div className="card" style={{ marginBottom: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <h3 style={{ margin: 0 }}>{t('subdomain_reservations', 'Subdomain Reservations')}</h3>
      </div>
      
      <div style={{ marginBottom: '24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px', marginBottom: '8px' }}>
          <span>{t('reservation_quota', 'My Personal Quota')}</span>
          <span>{limit < 0 ? `${used} / ∞` : `${used} / ${limit}`} {t('reserved', 'reserved')}</span>
        </div>
        <div style={{ height: '8px', background: 'rgba(255,255,255,0.1)', borderRadius: '4px', overflow: 'hidden' }}>
          <div style={{ height: '100%', width: `${Math.min(percent, 100)}%`, background: isAtLimit ? 'var(--danger)' : 'var(--primary)', transition: 'width 0.3s' }}></div>
        </div>
        {isAtLimit && limit >= 0 && (
          <div style={{ marginTop: '8px', fontSize: '12px', color: 'var(--warning)' }}>
            ⚠️ {t('reservation_limit_reached', 'You have reached your reservation limit. Release a subdomain to register a new one.')}
          </div>
        )}
      </div>

      {!isAtLimit && (
        <form onSubmit={createReservation} style={{ display: 'flex', gap: '8px', marginBottom: '24px', flexWrap: 'wrap' }}>
          <div style={{ flex: '1', minWidth: '150px' }}>
            <input 
              type="text" 
              className="form-control" 
              placeholder={t('subdomain', 'subdomain')} 
              value={subdomainInput} 
              onChange={(e) => setSubdomainInput(e.target.value)} 
            />
          </div>
          <div style={{ flex: '1', minWidth: '150px' }}>
            <select className="form-control" value={selectedDomain} onChange={(e) => setSelectedDomain(e.target.value)}>
              {domains.map(d => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
          </div>
          <div style={{ minWidth: '130px' }}>
            <select className="form-control" value={subdomainStyle} onChange={(e) => setSubdomainStyle(e.target.value)}>
              <option value="liferay">{t('style_liferay', 'Liferay Style')}</option>
              <option value="words">{t('style_words', 'Words Style')}</option>
              <option value="heroku">{t('style_heroku', 'Heroku Style')}</option>
              <option value="random">{t('style_random', 'Alphanumeric')}</option>
            </select>
          </div>
          <button type="button" className="btn btn-secondary" onClick={generateSubdomain}>{t('generate', 'Generate')}</button>
          <button type="submit" className="btn btn-primary">{t('reserve', 'Reserve')}</button>
        </form>
      )}

      {reservations.length > 0 && (
        <div style={{ marginBottom: '16px' }}>
          <input 
            type="text" 
            placeholder={t('search_reservations_placeholder', 'Search reservations...')} 
            value={searchQuery} 
            onChange={e => setSearchQuery(e.target.value)}
            style={{ padding: '8px 12px', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
          />
        </div>
      )}

      {reservations.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 20px', background: 'rgba(0,0,0,0.1)', border: '1px dashed var(--border)', borderRadius: '12px' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '15px' }}>{t('no_subdomains_reserved', 'No subdomains reserved yet.')}</div>
        </div>
      ) : (
        <div className="table-responsive">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('subdomain')} aria-sort={getAriaSort('subdomain')}>{t('subdomain', 'Subdomain')}{getSortIndicator('subdomain')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>{t('status', 'Status')}{getSortIndicator('status')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px', cursor: 'pointer' }} onClick={() => requestSort('expires_at')} aria-sort={getAriaSort('expires_at')}>{t('expires', 'Expires')}{getSortIndicator('expires_at')}</th>
                <th style={{ padding: '12px 16px', color: 'var(--text-muted)', fontWeight: 600, fontSize: '13px', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{t('actions', 'Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {sortedReservations.map(r => {
                const host = `${r.subdomain}.${r.domain}`;
                const isExpired = r.expires_at && new Date(r.expires_at) < new Date();
                const canExtend = !!(r.expires_at && !r.extension_requested && !isExpired);
                return (
                  <tr key={r.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)', transition: 'background 0.2s' }}>
                    <td style={{ padding: '16px' }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                        <a href={`https://${host}`} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', fontWeight: 600, textDecoration: 'none', fontFamily: 'monospace', fontSize: '14px' }}>
                          {host}
                        </a>
                        <button 
                          onClick={() => copyText(host, 'Host copied to clipboard')}
                          style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: '2px', fontSize: '14px' }}
                          title="Copy Host"
                        >
                          📋
                        </button>
                        <button 
                          onClick={() => copyText(`lfr-tunnel -subdomain ${r.subdomain} -server ${window.location.origin}`, 'CLI command copied')}
                          style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: '2px', fontSize: '14px' }}
                          title="Copy CLI Connection Command"
                        >
                          🔌
                        </button>
                      </div>
                    </td>
                    <td style={{ padding: '16px' }}>
                      {isExpired ? (
                        <span className="badge danger">Quarantined</span>
                      ) : r.extension_requested ? (
                        <span className="badge warning">Extension Requested</span>
                      ) : (
                        <span className="badge success">Active</span>
                      )}
                    </td>
                    <td style={{ padding: '16px', fontSize: '14px' }}>
                      {r.expires_at ? formatDate(r.expires_at) : 'Never (Permanent)'}
                    </td>
                    <td style={{ padding: '16px' }}>
                      <div style={{ display: 'flex', gap: '8px' }}>
                        {canExtend && (
                          <button 
                            className="btn btn-secondary" 
                            style={{ padding: '4px 8px', fontSize: '12px' }} 
                            onClick={() => requestExtension(r.id)}
                          >
                            Extend
                          </button>
                        )}
                        <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => deleteReservation(r.id)}>
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
      )}
    </div>
  );
}
