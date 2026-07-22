import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';
import { useTableSort } from '../hooks/useTableSort';
import Skeleton from '../components/Skeleton';
import { useUI } from '../contexts/UIContext';
import { useSettings } from '../contexts/SettingsContext';

interface User {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  role: string;
  status: string;
  auth_method: string;
  portal_active: boolean;
  rate_limit?: number;
  max_reservations?: number;
  max_tunnels?: number;
  last_login_at?: string;
  totp_enabled?: boolean;
  preferred_domain?: string;
  created_at?: string;
  onboarding_status?: string;
  onboarding_last_step?: string;
  onboarding_reruns?: number;
  active_tunnels?: Array<{
    subdomain_prefix: string;
    full_host: string;
    local_port: number;
    client_ip: string;
    bytes_in: number;
    bytes_out: number;
    created_at: string;
    node_id: string;
  }>;
}

const formatBytes = (bytes: number, decimals = 2) => {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
};

export default function AdminUsers() {
  const { t } = useI18n();
  const { user: currentUser } = useOutletContext<{ user: any }>();
  const { showToast, showConfirm, showPrompt } = useUI();
  const { formatDate } = useSettings();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [serverConfig, setServerConfig] = useState<any>(null);
  const [activeTab, setActiveTab] = useState<'users' | 'registrations'>('users');
  const [selectedUserPATs, setSelectedUserPATs] = useState<any[]>([]);
  const [domains, setDomains] = useState<string[]>([]);

  useEffect(() => {
    if (!selectedUser?.email) {
      setSelectedUserPATs([]);
      return;
    }
    const fetchUserDetails = async () => {
      try {
        const res = await axios.get(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`);
        setSelectedUserPATs(res.data.pats || []);
      } catch (err) {
        console.error("Failed to fetch user details", err);
      }
    };
    fetchUserDetails();
  }, [selectedUser?.email]);

  const extendUserToken = async (tokenId: number, days: number) => {
    if (!selectedUser) return;
    try {
      await axios.post(`/api/admin/tokens/${tokenId}/extend`, { days });
      showToast("Token updated successfully", "success");
      const res = await axios.get(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`);
      setSelectedUserPATs(res.data.pats || []);
    } catch {
      showToast("Failed to extend token", "error");
    }
  };

  const revokeUserToken = async (tokenId: number) => {
    if (!selectedUser) return;
    if (!(await showConfirm('Revoke Token', 'Are you sure you want to revoke this Personal Access Token? This will permanently disable it.'))) return;
    try {
      await axios.delete(`/api/admin/tokens/${tokenId}`);
      showToast("Token revoked successfully", "success");
      const res = await axios.get(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`);
      setSelectedUserPATs(res.data.pats || []);
    } catch {
      showToast("Failed to revoke token", "error");
    }
  };
  
  // Targeted Message State
  const [targetedUserId, setTargetedUserId] = useState('');
  const [targetedMessage, setTargetedMessage] = useState('');
  const [isSendingTargeted, setIsSendingTargeted] = useState(false);

  const [showInviteModal, setShowInviteModal] = useState(false);
  const [modalRateLimit, setModalRateLimit] = useState(0);
  const [modalMaxReservations, setModalMaxReservations] = useState(3);
  const [modalMaxTunnels, setModalMaxTunnels] = useState(3);
  const [modalPreferredDomain, setModalPreferredDomain] = useState('');
  const [updatingLimits, setUpdatingLimits] = useState(false);

  useEffect(() => {
    if (selectedUser) {
      setModalRateLimit(selectedUser.rate_limit || 0);
      setModalMaxReservations(selectedUser.max_reservations !== undefined && selectedUser.max_reservations !== null ? selectedUser.max_reservations : 3);
      setModalMaxTunnels(selectedUser.max_tunnels !== undefined && selectedUser.max_tunnels !== null ? selectedUser.max_tunnels : 3);
      setModalPreferredDomain(selectedUser.preferred_domain || '');
    }
  }, [selectedUser]);

  const updateRateLimit = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.patch(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`, {
        rate_limit: Number(modalRateLimit)
      });
      showToast('Rate limit updated successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { ...u, rate_limit: Number(modalRateLimit) } : u));
      setSelectedUser(prev => prev ? { ...prev, rate_limit: Number(modalRateLimit) } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update rate limit', 'error');
    } finally {
      setUpdatingLimits(false);
    }
  };

  const updateSubdomainLimit = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.post(`/api/admin/users/${encodeURIComponent(selectedUser.email)}/limit`, {
        max_reservations: Number(modalMaxReservations)
      });
      showToast('Subdomain reservation limit updated successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { ...u, max_reservations: Number(modalMaxReservations) } : u));
      setSelectedUser(prev => prev ? { ...prev, max_reservations: Number(modalMaxReservations) } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update subdomain limit', 'error');
    } finally {
      setUpdatingLimits(false);
    }
  };

  const updateTunnelsLimit = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.post(`/api/admin/users/${encodeURIComponent(selectedUser.email)}/tunnels-limit`, {
        max_tunnels: Number(modalMaxTunnels)
      });
      showToast('Tunnels concurrency limit updated successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { ...u, max_tunnels: Number(modalMaxTunnels) } : u));
      setSelectedUser(prev => prev ? { ...prev, max_tunnels: Number(modalMaxTunnels) } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update tunnels limit', 'error');
    } finally {
      setUpdatingLimits(false);
    }
  };

  const updatePreferredDomain = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.put(`/api/admin/users/${encodeURIComponent(selectedUser.email)}/preferred-domain`, {
        preferred_domain: modalPreferredDomain
      });
      showToast('Preferred domain updated successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { ...u, preferred_domain: modalPreferredDomain } : u));
      setSelectedUser(prev => prev ? { ...prev, preferred_domain: modalPreferredDomain } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update preferred domain', 'error');
    } finally {
      setUpdatingLimits(false);
    }
  };

  const resetUserMFA = async () => {
    if (!selectedUser) return;
    if (!(await showConfirm('Reset MFA', `Are you sure you want to reset Multi-Factor Authentication (MFA) for ${selectedUser.email}? This will force the user to re-register their TOTP auth device on next login.`))) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`, {
        reset_mfa: true
      });
      showToast('Multi-Factor Authentication reset successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { ...u, totp_enabled: false } : u));
      setSelectedUser(prev => prev ? { ...prev, totp_enabled: false } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to reset MFA', 'error');
    }
  };
  const [inviteForm, setInviteForm] = useState({ email: '', first_name: '', last_name: '', language_preference: 'en' });
  const [inviteError, setInviteError] = useState('');
  const [isInviting, setIsInviting] = useState(false);

  const showMessage = (type: 'error' | 'success', text: string) => {
    showToast(text, type === 'error' ? 'error' : 'success');
  };

  const fetchUsers = async () => {
    try {
      const [res, confRes, domRes] = await Promise.all([
        axios.get('/api/admin/users'),
        axios.get('/api/version').catch(() => ({ data: null })),
        axios.get('/api/domains').catch(() => ({ data: [] }))
      ]);
      setUsers(res.data);
      if (confRes.data) setServerConfig(confRes.data);
      if (domRes.data) setDomains(domRes.data);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchUsers();
    const interval = setInterval(fetchUsers, 5000);
    return () => clearInterval(interval);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const changeStatus = async (email: string, newStatus: string) => {
    if (!(await showConfirm('Change Status', `Are you sure you want to mark ${email} as ${newStatus}?`))) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, { status: newStatus });
      fetchUsers();
      showToast(`User status marked as ${newStatus}`, 'success');
    } catch {
      showMessage('error', `Failed to mark user as ${newStatus}`);
    }
  };

  const changeRole = async (email: string, newRole: string) => {
    if (!(await showConfirm('Change Role', `Are you sure you want to change ${email} to ${newRole}?`))) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, { role: newRole });
      fetchUsers();
      showToast(`User role updated to ${newRole}`, 'success');
    } catch {
      showMessage('error', `Failed to change role to ${newRole}`);
    }
  };

  const deleteUser = async (email: string) => {
    const confirmation = await showPrompt('Delete User', `Type "DELETE" to permanently remove ${email}`);
    if (confirmation !== "DELETE") {
      if (confirmation !== null) showToast('Deletion cancelled: confirmation word did not match.', 'info');
      return;
    }
    try {
      await axios.delete(`/api/admin/users/${encodeURIComponent(email)}`);
      fetchUsers();
      showToast('User deleted successfully', 'success');
    } catch {
      showMessage('error', 'Failed to delete user');
    }
  };

  const sendTargetedMessage = async () => {
    if (!targetedMessage.trim()) return;
    try {
      setIsSendingTargeted(true);
      await axios.post('/api/admin/targeted-message', {
        user_id: targetedUserId,
        message: targetedMessage
      });
      showMessage('success', 'Message sent successfully.');
      setTargetedUserId('');
    } catch (e: any) {
      showMessage('error', `Failed to send message: ${e.response?.data?.error || 'Unknown error'}`);
    } finally {
      setIsSendingTargeted(false);
    }
  };

  const kickTunnel = async (subdomain: string) => {
    if (!(await showConfirm('Kick Lease', `Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`))) return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
      // Update selectedUser if open
      setSelectedUser(prev => prev ? {
        ...prev,
        active_tunnels: prev.active_tunnels?.filter(t => t.subdomain_prefix !== subdomain)
      } : null);
      fetchUsers();
      showToast('Tunnel kicked successfully', 'success');
    } catch {
      showMessage('error', `Failed to kick tunnel ${subdomain}`);
    }
  };

  const submitInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    setInviteError('');
    setIsInviting(true);
    try {
      await axios.post('/api/admin/invite', inviteForm);
      setShowInviteModal(false);
      setInviteForm({ email: '', first_name: '', last_name: '', language_preference: 'en' });
      fetchUsers();
    } catch (err: any) {
      setInviteError(err.response?.data?.error || 'Failed to invite user');
    } finally {
      setIsInviting(false);
    }
  };
  const pendingCount = users.filter(u => u.status === 'pending').length;
  const filteredUsers = users.filter(u => {
    if (activeTab === 'users') {
      return u.status !== 'pending';
    } else {
      return u.status === 'pending';
    }
  });

  const { items: sortedUsers, requestSort, getSortIndicator, searchQuery, setSearchQuery, getAriaSort } = useTableSort(filteredUsers, ['email', 'first_name', 'last_name', 'role', 'status', 'auth_method', 'last_login_at']);
  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xl)' }}>
          <Skeleton width={180} height={28} />
          <Skeleton width={120} height={40} />
        </div>
        
        <div className="card" style={{ padding: 'var(--spacing-xl)', marginBottom: 'var(--spacing-xl)' }}>
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center' }}>
            <Skeleton width="100%" height={40} style={{ maxWidth: '300px' }} />
          </div>
        </div>

        <div className="card" style={{ padding: 'var(--spacing-xl)' }}>
          <div className="table-responsive">
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={100} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={60} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={120} /></th>
                  <th style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="90%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="85%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="60%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="70%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="80%" height={16} /></td>
                    <td style={{ padding: 'var(--spacing-lg)' }}><Skeleton width="50%" height={16} /></td>
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xl)' }}>
        <h3>{t('user_management', 'User Management')}</h3>
        <button className="btn btn-primary" onClick={() => setShowInviteModal(true)}>+ {t('invite_user', 'Invite User')}</button>
      </div>

      {/* Sub-tabs for All Users vs Pending Registrations */}
      <div style={{ display: 'flex', gap: 'var(--spacing-md)', borderBottom: '1px solid var(--border)', marginBottom: 'var(--spacing-lg)', paddingBottom: 'var(--spacing-sm)' }}>
        <button 
          onClick={() => setActiveTab('users')} 
          style={{
            background: 'none',
            border: 'none',
            color: activeTab === 'users' ? 'var(--primary)' : 'var(--text-muted)',
            fontWeight: activeTab === 'users' ? '600' : '400',
            fontSize: '15px',
            cursor: 'pointer',
            padding: 'var(--spacing-xs) var(--spacing-md)',
            borderBottom: activeTab === 'users' ? '2px solid var(--primary)' : '2px solid transparent',
            marginBottom: '-10px',
            transition: '0.2s'
          }}
        >
          {t('users_tab_all', 'All Users')}
        </button>
        <button 
          onClick={() => setActiveTab('registrations')} 
          style={{
            background: 'none',
            border: 'none',
            color: activeTab === 'registrations' ? 'var(--primary)' : 'var(--text-muted)',
            fontWeight: activeTab === 'registrations' ? '600' : '400',
            fontSize: '15px',
            cursor: 'pointer',
            padding: 'var(--spacing-xs) var(--spacing-md)',
            borderBottom: activeTab === 'registrations' ? '2px solid var(--primary)' : '2px solid transparent',
            marginBottom: '-10px',
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
            transition: '0.2s'
          }}
        >
          <span>{t('users_tab_registrations', 'Pending Registrations')}</span>
          {pendingCount > 0 && (
            <span className="badge" style={{ 
              background: 'var(--danger, #ef4444)', 
              color: 'white', 
              borderRadius: 'var(--spacing-xs)', 
              padding: '2px var(--spacing-sm)', 
              fontSize: '11px', 
              fontWeight: 'bold',
              lineHeight: '1'
            }}>
              {pendingCount}
            </span>
          )}
        </button>
      </div>
      <div style={{ marginBottom: 'var(--spacing-lg)' }}>
        <input 
          type="text" 
          placeholder={t('search_users_placeholder', 'Search users...')} 
          value={searchQuery} 
          onChange={e => setSearchQuery(e.target.value)}
          style={{ padding: 'var(--spacing-sm) var(--spacing-md)', width: '100%', maxWidth: '300px', background: 'var(--input-bg)', color: 'var(--text-main)', border: '1px solid var(--border)', borderRadius: '6px' }}
        />
      </div>

      <div className="card" style={{ padding: '0' }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('first_name')} aria-sort={getAriaSort('first_name')}>User{getSortIndicator('first_name')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('role')} aria-sort={getAriaSort('role')}>Role{getSortIndicator('role')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>Status{getSortIndicator('status')}</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('auth_method')} aria-sort={getAriaSort('auth_method')}>Auth Method{getSortIndicator('auth_method')}</th>
                <th>Quotas</th>
                <th style={{ cursor: 'pointer' }} onClick={() => requestSort('last_login_at')} aria-sort={getAriaSort('last_login_at')}>Last Seen{getSortIndicator('last_login_at')}</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedUsers.length === 0 ? (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', padding: 'var(--spacing-xl)', color: 'var(--text-muted)' }}>
                    {activeTab === 'users' ? t('no_users_found', 'No users found.') : t('no_pending_registrations', 'No pending registrations.')}
                  </td>
                </tr>
              ) : (
                sortedUsers.map((u) => {
                  const isSelf = currentUser && u.email === currentUser.email;
                  return (
                    <tr key={u.email} style={isSelf ? { opacity: 0.6 } : {}}>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                          {u.portal_active ? (
                            <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--success)', boxShadow: '0 0 8px var(--success)', flexShrink: 0 }} title="Online" />
                          ) : (
                            <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--status-inactive)', flexShrink: 0 }} title="Offline" />
                          )}
                          <div>
                            <div style={{ fontWeight: 500 }}>
                              {u.first_name} {u.last_name}
                            </div>
                            <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>
                              <a 
                                href="#" 
                                onClick={(e) => { e.preventDefault(); setSelectedUser(u); }}
                                className="email-link"
                              >
                                {u.email}
                              </a>
                              {(u.active_tunnels?.length || 0) > 0 && (
                                <span 
                                  className="badge tunnels" 
                                  onClick={() => setSelectedUser(u)}
                                  style={{ cursor: 'pointer', padding: '2px var(--spacing-sm)', fontSize: '11px', marginLeft: 'var(--spacing-sm)' }}
                                  title="Click to view tunnels"
                                >
                                  🔌 {u.active_tunnels!.length} Tunnel{(u.active_tunnels!.length) > 1 ? 's' : ''}
                                </span>
                              )}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td><span className="badge">{u.role}</span></td>
                      <td>
                        <span className={`badge ${u.status === 'approved' ? 'success' : u.status === 'pending' ? 'warning' : 'danger'}`}>
                          {u.status}
                        </span>
                      </td>
                      <td>{u.auth_method || 'password'}</td>
                      <td>
                        <div style={{ fontSize: '12px', display: 'flex', flexDirection: 'column', gap: '2px' }}>
                          <div><span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>RPS:</span> <strong>{u.rate_limit ? u.rate_limit : '∞'}</strong></div>
                          <div><span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>Subs:</span> <strong>{u.max_reservations !== undefined && u.max_reservations !== null ? (u.max_reservations < 0 ? '∞' : u.max_reservations) : '3'}</strong></div>
                          <div><span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>Tunnels:</span> <strong>{u.max_tunnels !== undefined && u.max_tunnels !== null ? (u.max_tunnels < 0 ? '∞' : u.max_tunnels) : '3'}</strong></div>
                        </div>
                      </td>
                      <td>
                        {u.portal_active ? (
                          <span style={{ color: 'var(--success)', fontWeight: 600 }}>Active Now</span>
                        ) : (
                          u.last_login_at ? formatDate(u.last_login_at) : <span style={{ color: 'var(--text-muted)' }}>Never</span>
                        )}
                      </td>
                      <td>
                        {!isSelf && (
                          <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                            {u.status === 'pending' || u.status === 'unverified' ? (
                              <>
                                <button className="btn btn-primary" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'approved')}>Approve</button>
                                <button className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'revoked')}>Reject</button>
                              </>
                            ) : (
                              <>
                                {u.status === 'approved' ? (
                                  <button className="btn" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'revoked')}>Suspend</button>
                                ) : (
                                  <button className="btn" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'approved')}>Unsuspend</button>
                                )}
                                
                                {(currentUser.role === 'owner' || u.role !== 'owner') && (
                                    <>
                                      {u.role === 'admin' || u.role === 'owner' ? (
                                        <button className="btn" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeRole(u.email, 'user')}>Demote</button>
                                      ) : (
                                        <button className="btn" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => changeRole(u.email, 'admin')}>Promote</button>
                                      )}
                                      
                                      {u.email.toLowerCase() !== serverConfig?.owner_email?.toLowerCase() && (
                                        <button className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={() => deleteUser(u.email)}>Delete</button>
                                      )}
                                    </>
                                )}
                              </>
                            )}
                          </div>
                        )}
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>
      {selectedUser && (
        <div style={{
          position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', 
          backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1000, 
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 'var(--spacing-lg)'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '800px', maxHeight: '90vh', overflowY: 'auto' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-lg)' }}>
              <h3 style={{ margin: 0 }}>User Details & Tunnels</h3>
              <button onClick={() => setSelectedUser(null)} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 'var(--spacing-lg)', marginBottom: 'var(--spacing-xl)' }}>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>Name</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.first_name} {selectedUser.last_name}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>Email</div>
                <div style={{ fontWeight: 500, fontFamily: 'monospace' }}>{selectedUser.email}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>Status & Role</div>
                <div>
                  <span className={`badge ${selectedUser.status === 'approved' ? 'success' : (selectedUser.status === 'revoked' ? 'danger' : 'warning')}`} style={{ marginRight: 'var(--spacing-sm)' }}>
                    {selectedUser.status}
                  </span>
                  <span className={`badge ${selectedUser.role === 'admin' ? 'success' : ''}`}>
                    {selectedUser.role}
                  </span>
                </div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>Origin</div>
                <div style={{ fontWeight: 500, textTransform: 'capitalize' }}>{selectedUser.auth_method || 'Magic Link'}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>Joined Date</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.created_at ? new Date(selectedUser.created_at).toLocaleString() : 'N/A'}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 'var(--spacing-xs)' }}>API Quota</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.rate_limit ? `${selectedUser.rate_limit} RPS` : 'Unlimited'}</div>
              </div>
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 'var(--spacing-xl)' }}>
              <button className="btn btn-primary" onClick={() => setTargetedUserId(selectedUser.id)}>💬 Direct Message</button>
            </div>

            <h4 style={{ marginTop: 'var(--spacing-xl)', marginBottom: 'var(--spacing-lg)', borderBottom: '1px solid var(--border-color)', paddingBottom: 'var(--spacing-sm)' }}>
              Quotas & Security
            </h4>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--spacing-lg)', marginBottom: 'var(--spacing-xl)', background: 'rgba(255,255,255,0.02)', padding: 'var(--spacing-md)', borderRadius: '6px', border: '1px solid var(--border-color)' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-xs)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Rate Limit (RPS)</label>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                  <input 
                    type="number" 
                    className="input-field" 
                    style={{ flex: 1, padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }} 
                    min={0}
                    value={modalRateLimit} 
                    onChange={(e) => setModalRateLimit(Number(e.target.value))} 
                    placeholder="Unlimited"
                  />
                  <button className="btn btn-primary" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={updateRateLimit} disabled={updatingLimits}>Save</button>
                </div>
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-xs)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Max Subdomains</label>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                  <input 
                    type="number" 
                    className="input-field" 
                    style={{ flex: 1, padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }} 
                    min={-1}
                    value={modalMaxReservations} 
                    onChange={(e) => setModalMaxReservations(Number(e.target.value))} 
                    placeholder="3"
                  />
                  <button className="btn btn-primary" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={updateSubdomainLimit} disabled={updatingLimits}>Save</button>
                </div>
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-xs)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Max Tunnels</label>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                  <input 
                    type="number" 
                    className="input-field" 
                    style={{ flex: 1, padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }} 
                    min={-1}
                    value={modalMaxTunnels} 
                    onChange={(e) => setModalMaxTunnels(Number(e.target.value))} 
                    placeholder="3"
                  />
                  <button className="btn btn-primary" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={updateTunnelsLimit} disabled={updatingLimits}>Save</button>
                </div>
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-xs)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>Preferred Domain</label>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                  <select 
                    className="input-field" 
                    style={{ flex: 1, padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '14px' }} 
                    value={modalPreferredDomain} 
                    onChange={(e) => setModalPreferredDomain(e.target.value)} 
                  >
                    <option value="">None (Auto)</option>
                    {domains.map(d => (
                      <option key={d} value={d}>{d}</option>
                    ))}
                  </select>
                  <button className="btn btn-primary" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={updatePreferredDomain} disabled={updatingLimits}>Save</button>
                </div>
              </div>

              <div style={{ display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
                <label style={{ display: 'block', fontSize: '12px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-xs)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>MFA Security Status</label>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)' }}>
                  {selectedUser.totp_enabled ? (
                    <>
                      <span className="badge success">Enabled</span>
                      <button className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontSize: '12px' }} onClick={resetUserMFA}>Reset MFA</button>
                    </>
                  ) : (
                    <span className="badge" style={{ background: 'rgba(255,255,255,0.05)', color: 'var(--text-muted)' }}>Inactive</span>
                  )}
                </div>
              </div>
            </div>

            <h4 style={{ marginTop: 'var(--spacing-xl)', marginBottom: 'var(--spacing-lg)', borderBottom: '1px solid var(--border-color)', paddingBottom: 'var(--spacing-sm)' }}>
              Connected Tunnels <span className="badge" style={{ marginLeft: 'var(--spacing-sm)' }}>{(selectedUser.active_tunnels || []).length}</span>
            </h4>
            
            <div className="table-responsive" style={{ border: '1px solid var(--border-color)', borderRadius: '6px' }}>
              <table style={{ margin: 0 }}>
                <tbody>
                  {!(selectedUser.active_tunnels || []).length && (
                    <tr>
                      <td colSpan={4} style={{ textAlign: 'center', padding: 'var(--spacing-xl)', color: 'var(--text-muted)' }}>No active tunnels connected.</td>
                    </tr>
                  )}
                  {(selectedUser.active_tunnels || []).map((t) => {
                    const publicUrl = `https://${t.full_host}`;
                    return (
                      <tr key={t.subdomain_prefix} style={{ borderBottom: '1px solid rgba(255,255,255,0.03)' }}>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle' }}>
                          <div style={{ fontWeight: 600, fontFamily: 'monospace', fontSize: '13px', color: 'var(--text)' }}>{t.subdomain_prefix}</div>
                          <div style={{ fontSize: '11px', color: 'var(--text-muted)', marginTop: '2px' }}>Local Port: {t.local_port}</div>
                        </td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle' }}>
                          <a href={publicUrl} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none', fontSize: '13px', fontFamily: 'monospace', wordBreak: 'break-all' }}>{publicUrl}</a>
                          {t.node_id && t.node_id !== 'control' ? (
                            <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)', fontSize: '10px', marginLeft: 'var(--spacing-xs)' }}>
                              🌍 {t.node_id}
                            </span>
                          ) : (
                            <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)', fontSize: '10px', marginLeft: 'var(--spacing-xs)' }}>
                              🇬🇧 Control
                            </span>
                          )}
                          <div style={{ fontSize: '11px', color: 'var(--text-muted)', marginTop: '2px' }}>
                            IP: {t.client_ip} | Connected: {new Date(t.created_at).toLocaleString()}
                          </div>
                        </td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', fontSize: '12px', color: 'var(--text-muted)' }}>
                          <div>📥 In: <strong style={{ color: 'var(--text)' }}>{formatBytes(t.bytes_in)}</strong></div>
                          <div style={{ marginTop: '2px' }}>📤 Out: <strong style={{ color: 'var(--text)' }}>{formatBytes(t.bytes_out)}</strong></div>
                        </td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', textAlign: 'right' }}>
                          <button className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-md)', fontSize: '12px' }} onClick={() => kickTunnel(t.subdomain_prefix)}>Kick</button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <h4 style={{ marginTop: 'var(--spacing-xl)', marginBottom: 'var(--spacing-lg)', borderBottom: '1px solid var(--border-color)', paddingBottom: 'var(--spacing-sm)' }}>
              Personal Access Tokens <span className="badge" style={{ marginLeft: 'var(--spacing-sm)' }}>{selectedUserPATs.length}</span>
            </h4>

            <div className="table-responsive" style={{ border: '1px solid var(--border-color)', borderRadius: '6px', marginBottom: 'var(--spacing-xl)' }}>
              <table style={{ margin: 0 }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--border)', textAlign: 'left' }}>
                    <th style={{ padding: 'var(--spacing-md)', fontSize: '12px' }}>Name</th>
                    <th style={{ padding: 'var(--spacing-md)', fontSize: '12px' }}>Prefix</th>
                    <th style={{ padding: 'var(--spacing-md)', fontSize: '12px' }}>Expires</th>
                    <th style={{ padding: 'var(--spacing-md)', fontSize: '12px' }}>Status</th>
                    <th style={{ padding: 'var(--spacing-md)', fontSize: '12px', textAlign: 'right' }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {!selectedUserPATs.length && (
                    <tr>
                      <td colSpan={5} style={{ textAlign: 'center', padding: 'var(--spacing-xl)', color: 'var(--text-muted)' }}>No tokens found for this user.</td>
                    </tr>
                  )}
                  {selectedUserPATs.map((pat) => {
                    const isRevoked = pat.revoked_at != null && !pat.revoked_at.startsWith('0001-01-01');
                    const isExpired = pat.expires_at && !pat.expires_at.startsWith('0001-01-01') && new Date(pat.expires_at) < new Date();
                    
                    let statusBadge = (
                      <span className="badge success">Active</span>
                    );
                    if (isRevoked) {
                      statusBadge = <span className="badge danger">Revoked</span>;
                    } else if (isExpired) {
                      statusBadge = <span className="badge danger">Expired</span>;
                    }

                    return (
                      <tr key={pat.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.03)' }}>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', fontSize: '13px' }}>{pat.name}</td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', fontSize: '13px', fontFamily: 'monospace' }}>{pat.token_prefix}...</td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', fontSize: '13px' }}>
                          {pat.expires_at && !pat.expires_at.startsWith('0001-01-01') ? new Date(pat.expires_at).toLocaleDateString() : 'Never'}
                        </td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle' }}>{statusBadge}</td>
                        <td style={{ padding: 'var(--spacing-md)', verticalAlign: 'middle', textAlign: 'right' }}>
                          {!isRevoked && (
                            <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                              <button 
                                className="btn btn-outline" 
                                style={{ padding: '2px 8px', fontSize: '11px' }}
                                onClick={() => extendUserToken(pat.id, 30)}
                              >
                                +30d
                              </button>
                              <button 
                                className="btn btn-outline" 
                                style={{ padding: '2px 8px', fontSize: '11px' }}
                                onClick={() => extendUserToken(pat.id, 90)}
                              >
                                +90d
                              </button>
                              <button 
                                className="btn btn-outline" 
                                style={{ padding: '2px 8px', fontSize: '11px' }}
                                onClick={() => extendUserToken(pat.id, 0)}
                              >
                                Perm
                              </button>
                              <button 
                                className="btn btn-danger" 
                                style={{ padding: '2px 8px', fontSize: '11px' }}
                                onClick={() => revokeUserToken(pat.id)}
                              >
                                Revoke
                              </button>
                            </div>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

          </div>
        </div>
      )}

      {targetedUserId && (
        <div style={{
          position: 'fixed', top: 0, left: 0, width: '100%', height: '100%',
          backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1010,
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 'var(--spacing-lg)'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '500px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-lg)' }}>
              <h3 style={{ margin: 0 }}>Send Direct Message</h3>
              <button onClick={() => setTargetedUserId('')} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            <p style={{ fontSize: '14px', color: 'var(--text-muted)', marginBottom: 'var(--spacing-lg)' }}>
              Push a real-time banner alert to this specific active developer session.
            </p>
            <div className="form-group" style={{ marginBottom: 'var(--spacing-lg)' }}>
              <textarea
                className="input-field"
                placeholder={t('enter_your_message_placeholder', 'Enter your message...')}
                rows={3}
                value={targetedMessage}
                onChange={(e) => setTargetedMessage(e.target.value)}
              />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--spacing-sm)' }}>
              <button className="btn btn-secondary" onClick={() => setTargetedUserId('')}>Cancel</button>
              <button className="btn btn-primary" disabled={isSendingTargeted || !targetedMessage.trim()} onClick={sendTargetedMessage}>
                {isSendingTargeted ? 'Sending...' : 'Send Message'}
              </button>
            </div>
          </div>
        </div>
      )}

      {showInviteModal && (
        <div style={{
          position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', 
          backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1000, 
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 'var(--spacing-lg)'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '400px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-lg)' }}>
              <h3 style={{ margin: 0 }}>{t('invite_user', 'Invite User')}</h3>
              <button onClick={() => setShowInviteModal(false)} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            {inviteError && <div className="alert alert-danger" style={{ marginBottom: 'var(--spacing-lg)' }}>{inviteError}</div>}
            <form onSubmit={submitInvite}>
              <div className="form-group" style={{ marginBottom: 'var(--spacing-lg)' }}>
                <label>{t('email_address', 'Email Address')}</label>
                <input type="email" required className="input-field" value={inviteForm.email} onChange={(e) => setInviteForm({...inviteForm, email: e.target.value})} placeholder={t('invite_email_placeholder', 'user@company.com')} />
              </div>
              <div className="form-group" style={{ marginBottom: 'var(--spacing-lg)' }}>
                <label>{t('first_name', 'First Name')}</label>
                <input type="text" required className="input-field" value={inviteForm.first_name} onChange={(e) => setInviteForm({...inviteForm, first_name: e.target.value})} placeholder={t('first_name_placeholder', 'John')} />
              </div>
              <div className="form-group" style={{ marginBottom: 'var(--spacing-lg)' }}>
                <label>{t('last_name', 'Last Name')}</label>
                <input type="text" required className="input-field" value={inviteForm.last_name} onChange={(e) => setInviteForm({...inviteForm, last_name: e.target.value})} placeholder={t('last_name_placeholder', 'Doe')} />
              </div>
              <div className="form-group" style={{ marginBottom: 'var(--spacing-xl)' }}>
                <label>{t('language_preference', 'Language Preference')}</label>
                <select className="input-field" value={inviteForm.language_preference} onChange={(e) => setInviteForm({...inviteForm, language_preference: e.target.value})}>
                  <option value="en">English (UK)</option>
                  <option value="en-us">English (US)</option>
                  <option value="de">Deutsch (DE)</option>
                  <option value="es">Español (ES)</option>
                  <option value="fr">Français (FR)</option>
                </select>
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--spacing-sm)' }}>
                <button type="button" className="btn" onClick={() => setShowInviteModal(false)}>{t('cancel', 'Cancel')}</button>
                <button type="submit" className="btn btn-primary" disabled={isInviting}>
                  {isInviting ? t('sending', 'Sending...') : t('send_invitation', 'Send Invitation')}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
