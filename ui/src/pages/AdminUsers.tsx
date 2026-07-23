import { useEffect, useState, useMemo } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';
import Skeleton from '../components/Skeleton';
import { useUI } from '../contexts/UIContext';
import { useSettings } from '../contexts/SettingsContext';
import { useDataTable, type ColumnDef } from '../hooks/useDataTable';
import DataTableToolbar from '../components/DataTableToolbar';
import DataTablePagination from '../components/DataTablePagination';

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
  quotas?: string;
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
  const [_domains, _setDomains] = useState<string[]>([]);

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
  const [updatingLimits, setUpdatingLimits] = useState(false);

  useEffect(() => {
    if (selectedUser) {
      setModalRateLimit(selectedUser.rate_limit || 0);
      setModalMaxReservations(selectedUser.max_reservations !== undefined && selectedUser.max_reservations !== null ? selectedUser.max_reservations : 3);
      setModalMaxTunnels(selectedUser.max_tunnels !== undefined && selectedUser.max_tunnels !== null ? selectedUser.max_tunnels : 3);
    }
  }, [selectedUser]);

  const updateQuotas = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.patch(`/api/admin/users/${encodeURIComponent(selectedUser.email)}`, {
        rate_limit: Number(modalRateLimit),
        max_reservations: Number(modalMaxReservations),
        max_tunnels: Number(modalMaxTunnels)
      });
      showToast('User settings updated successfully', 'success');
      setUsers(prev => prev.map(u => u.email === selectedUser.email ? { 
        ...u, 
        rate_limit: Number(modalRateLimit),
        max_reservations: Number(modalMaxReservations),
        max_tunnels: Number(modalMaxTunnels)
      } : u));
      setSelectedUser(prev => prev ? { 
        ...prev, 
        rate_limit: Number(modalRateLimit),
        max_reservations: Number(modalMaxReservations),
        max_tunnels: Number(modalMaxTunnels)
      } : null);
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update user quotas', 'error');
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
      if (domRes.data) _setDomains(domRes.data);
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
  const filteredUsers = useMemo(() => users.filter(u => {
    if (activeTab === 'users') {
      return u.status !== 'pending';
    } else {
      return u.status === 'pending';
    }
  }), [users, activeTab]);

  const columns: ColumnDef<User>[] = useMemo(() => [
    { key: 'email', label: 'User', sortable: true },
    { key: 'role', label: 'Role', sortable: true },
    { key: 'status', label: 'Status', sortable: true },
    { key: 'auth_method', label: 'Auth Method', sortable: true },
    { key: 'quotas', label: 'Quotas', sortable: false },
    { key: 'last_login_at', label: 'Last Seen', sortable: true },
    { key: 'created_at', label: 'Created Date', sortable: true },
  ], []);

  const statusOptions = useMemo(() => [
    { value: 'approved', label: t('status_approved', 'Approved') },
    { value: 'pending', label: t('status_pending', 'Pending') },
    { value: 'unverified', label: t('status_unverified', 'Unverified') },
    { value: 'revoked', label: t('status_revoked', 'Revoked') }
  ], [t]);

  const {
    paginatedItems: paginatedUsers,
    currentPage,
    totalPages,
    totalItems,
    pageSize,
    setCurrentPage,
    setPageSize,
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    requestSort,
    getSortIndicator,
    getAriaSort,
    isColumnVisible,
    toggleColumn,
    allColumns
  } = useDataTable<User>(
    'admin_users',
    filteredUsers,
    ['email', 'first_name', 'last_name', 'role', 'status', 'auth_method'],
    columns,
    10,
    ['created_at'],
    'status',
    statusOptions,
    'all'
  );

  if (loading) {
    return (
      <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
        <div className="page-header mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={320} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={100} /></th>
                  <th className="th-col"><Skeleton width={60} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                  <th className="th-col"><Skeleton width={120} /></th>
                  <th className="th-col"><Skeleton width={80} /></th>
                </tr>
              </thead>
              <tbody>
                {[...Array(5)].map((_, i) => (
                  <tr key={i} className="border-b">
                    <td className="td-cell"><Skeleton width="90%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="85%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="60%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="70%" height={16} /></td>
                    <td className="td-cell"><Skeleton width="80%" height={16} /></td>
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
      <div className="flex items-center justify-between mb-xl">
        <div>
          <h3 className="page-header__title">{t('user_management', 'User Management')}</h3>
          <p className="page-header__desc">{t('user_management_desc', 'Manage users, promotion, roles, and pending registration approvals.')}</p>
        </div>
        <button onClick={() => setShowInviteModal(true)} className="btn btn-primary flex items-center gap-xs">
          <span>➕</span>
          <span>{t('invite_user', 'Invite User')}</span>
        </button>
      </div>

      <div className="sub-tab-bar mb-xl">
        <button 
          onClick={() => setActiveTab('users')} 
          className={`sub-tab ${activeTab === 'users' ? 'sub-tab--active' : ''}`}
        >
          {t('users_tab_active', 'Active Users')} ({users.filter(u => u.status !== 'pending').length})
        </button>
        <button 
          onClick={() => setActiveTab('registrations')} 
          className={`sub-tab ${activeTab === 'registrations' ? 'sub-tab--active' : ''} flex items-center gap-xs`}
        >
          <span>{t('users_tab_registrations', 'Pending Registrations')}</span>
          {pendingCount > 0 && (
            <span className="badge badge-danger text-2xs fw-bold px-xs py-0">
              {pendingCount}
            </span>
          )}
        </button>
      </div>

      <div className="card p-0">
        <div className="p-md border-b">
          <DataTableToolbar
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            searchPlaceholder={t('search_users_placeholder', 'Search users...')}
            pageSize={pageSize}
            onPageSizeChange={setPageSize}
            columns={allColumns}
            isColumnVisible={isColumnVisible}
            onToggleColumn={toggleColumn}
            statusFilter={statusFilter}
            onStatusFilterChange={setStatusFilter}
            statusOptions={statusOptions}
          />
        </div>

        <div className="table-responsive">
          <table className="w-full">
            <thead>
              <tr className="border-b text-left">
                {isColumnVisible('email') && <th className="th-col th-col--sortable" onClick={() => requestSort('email')} aria-sort={getAriaSort('email')}>User{getSortIndicator('email')}</th>}
                {isColumnVisible('role') && <th className="th-col th-col--sortable" onClick={() => requestSort('role')} aria-sort={getAriaSort('role')}>Role{getSortIndicator('role')}</th>}
                {isColumnVisible('status') && <th className="th-col th-col--sortable" onClick={() => requestSort('status')} aria-sort={getAriaSort('status')}>Status{getSortIndicator('status')}</th>}
                {isColumnVisible('auth_method') && <th className="th-col th-col--sortable" onClick={() => requestSort('auth_method')} aria-sort={getAriaSort('auth_method')}>Auth Method{getSortIndicator('auth_method')}</th>}
                {isColumnVisible('quotas') && <th className="th-col">Quotas</th>}
                {isColumnVisible('last_login_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('last_login_at')} aria-sort={getAriaSort('last_login_at')}>Last Seen{getSortIndicator('last_login_at')}</th>}
                {isColumnVisible('created_at') && <th className="th-col th-col--sortable" onClick={() => requestSort('created_at')} aria-sort={getAriaSort('created_at')}>Created Date{getSortIndicator('created_at')}</th>}
                <th className="th-col text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {paginatedUsers.length === 0 ? (
                <tr>
                  <td colSpan={8} className="td-cell text-center text-muted py-xl">
                    {activeTab === 'users' ? t('no_users_found', 'No users found.') : t('no_pending_registrations', 'No pending registrations.')}
                  </td>
                </tr>
              ) : (
                paginatedUsers.map((u) => {
                  const isSelf = currentUser && u.email === currentUser.email;
                  return (
                    <tr key={u.email} className={`border-b hover:bg-white/5 transition-colors ${isSelf ? 'opacity-60' : ''}`}>
                      {isColumnVisible('email') && (
                        <td className="td-cell">
                          <div className="flex items-center gap-xs">
                            {u.portal_active ? (
                              <div className="status-dot status-dot--online" title="Online" />
                            ) : (
                              <div className="status-dot status-dot--offline" title="Offline" />
                            )}
                            <div>
                              <div className="fw-medium">{u.first_name || u.last_name ? `${u.first_name || ''} ${u.last_name || ''}`.trim() : u.email}</div>
                              <div className="text-muted text-2xs">{u.email}</div>
                              <div className="flex gap-xs mt-2xs">
                                {u.active_tunnels && u.active_tunnels.length > 0 && (
                                  <span className="badge badge-info text-2xs px-xs py-0">
                                    🔌 {u.active_tunnels.length} Tunnel{u.active_tunnels.length > 1 ? 's' : ''}
                                  </span>
                                )}
                              </div>
                            </div>
                          </div>
                        </td>
                      )}
                      {isColumnVisible('role') && <td className="td-cell"><span className="badge">{u.role.toLowerCase()}</span></td>}
                      {isColumnVisible('status') && (
                        <td className="td-cell">
                          <span className={`badge ${u.status === 'approved' ? 'badge-success' : u.status === 'pending' ? 'badge-warning' : 'badge-danger'}`}>
                            {u.status.toLowerCase()}
                          </span>
                        </td>
                      )}
                      {isColumnVisible('auth_method') && <td className="td-cell">{u.auth_method || 'password'}</td>}
                      {isColumnVisible('quotas') && (
                        <td className="td-cell text-xs">
                          <div className="flex flex-col gap-2xs">
                            <div><span className="text-2xs text-muted">RPS:</span> <strong>{u.rate_limit ? u.rate_limit : '∞'}</strong></div>
                            <div><span className="text-2xs text-muted">Subs:</span> <strong>{u.max_reservations !== undefined && u.max_reservations !== null ? (u.max_reservations < 0 ? '∞' : u.max_reservations) : '3'}</strong></div>
                            <div><span className="text-2xs text-muted">Tunnels:</span> <strong>{u.max_tunnels !== undefined && u.max_tunnels !== null ? (u.max_tunnels < 0 ? '∞' : u.max_tunnels) : '3'}</strong></div>
                          </div>
                        </td>
                      )}
                      {isColumnVisible('last_login_at') && (
                        <td className="td-cell">
                          {u.portal_active ? (
                            <span className="text-success fw-semibold">Active Now</span>
                          ) : (
                            u.last_login_at ? formatDate(u.last_login_at) : <span className="text-muted">Never</span>
                          )}
                        </td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell" style={{ whiteSpace: 'nowrap' }}>
                          {u.created_at ? formatDate(u.created_at) : '—'}
                        </td>
                      )}
                      <td className="td-cell text-right whitespace-nowrap">
                        {!isSelf && (
                          <div className="flex gap-xs justify-end">
                            <button 
                              className="btn btn-secondary py-xs px-sm text-xs" 
                              onClick={() => setSelectedUser(u)}
                            >
                              Details
                            </button>
                            {u.status === 'pending' || u.status === 'unverified' ? (
                              <>
                                <button className="btn btn-primary py-xs px-sm text-xs" onClick={() => changeStatus(u.email, 'approved')}>Approve</button>
                                <button className="btn btn-danger py-xs px-sm text-xs" onClick={() => changeStatus(u.email, 'revoked')}>Reject</button>
                              </>
                            ) : (
                              <>
                                {u.status === 'approved' ? (
                                  <button className="btn py-xs px-sm text-xs" onClick={() => changeStatus(u.email, 'revoked')}>Suspend</button>
                                ) : (
                                  <button className="btn py-xs px-sm text-xs" onClick={() => changeStatus(u.email, 'approved')}>Unsuspend</button>
                                )}
                                
                                {(currentUser.role === 'owner' || u.role !== 'owner') && (
                                    <>
                                      {u.role === 'admin' || u.role === 'owner' ? (
                                        <button className="btn py-xs px-sm text-xs" onClick={() => changeRole(u.email, 'user')}>Demote</button>
                                      ) : (
                                        <button className="btn py-xs px-sm text-xs" onClick={() => changeRole(u.email, 'admin')}>Promote</button>
                                      )}
                                      
                                      {u.email.toLowerCase() !== serverConfig?.owner_email?.toLowerCase() && (
                                        <button className="btn btn-danger py-xs px-sm text-xs" onClick={() => deleteUser(u.email)}>Delete</button>
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

        <DataTablePagination
          currentPage={currentPage}
          totalPages={totalPages}
          totalItems={totalItems}
          pageSize={pageSize}
          onPageChange={setCurrentPage}
        />
      </div>
      {selectedUser && (
        <div className="modal-backdrop">
          <div 
            className="modal-card modal-card--lg max-h-90vh overflow-y-auto"
            role="dialog"
            aria-modal="true"
            aria-labelledby="user-details-modal-title"
          >
            <div className="modal-header">
              <h3 id="user-details-modal-title" className="modal-title">User Details & Tunnels</h3>
              <button type="button" onClick={() => setSelectedUser(null)} className="modal-close" aria-label={t('close', 'Close')}>✕</button>
            </div>
            
            <div className="auto-grid-md gap-lg mb-xl">
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">Name</div>
                <div className="fw-medium">{selectedUser.first_name} {selectedUser.last_name}</div>
              </div>
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">Email</div>
                <div className="fw-medium font-mono">{selectedUser.email}</div>
              </div>
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">Status & Role</div>
                <div>
                  <span className={`badge ${selectedUser.status === 'approved' ? 'badge-success' : (selectedUser.status === 'revoked' ? 'badge-danger' : 'badge-warning')} mr-sm`}>
                    {selectedUser.status}
                  </span>
                  <span className={`badge ${selectedUser.role === 'admin' ? 'badge-success' : ''}`}>
                    {selectedUser.role}
                  </span>
                </div>
              </div>
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">Origin</div>
                <div className="fw-medium capitalize">{selectedUser.auth_method || 'Magic Link'}</div>
              </div>
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">Joined Date</div>
                <div className="fw-medium">{selectedUser.created_at ? formatDate(selectedUser.created_at) : 'N/A'}</div>
              </div>
              <div>
                <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">API Quota</div>
                <div className="fw-medium">{selectedUser.rate_limit ? `${selectedUser.rate_limit} RPS` : 'Unlimited'}</div>
              </div>
            </div>

            <div className="flex justify-start mb-xl">
              <button type="button" className="btn btn-primary" onClick={() => setTargetedUserId(selectedUser.id)}>💬 Direct Message</button>
            </div>

            <h4 className="section-title mb-lg border-b pb-xs">
              Quotas & Security
            </h4>
            <div className="auto-grid-md gap-lg p-md rounded border">
              <div>
                <label className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase">Rate Limit (RPS)</label>
                <div>
                  <input 
                    type="number" 
                    className="input-field w-full py-xs px-sm text-sm" 
                    min={0}
                    value={modalRateLimit} 
                    onChange={(e) => setModalRateLimit(Number(e.target.value))} 
                    placeholder="Unlimited"
                  />
                </div>
              </div>

              <div>
                <label className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase">Max Subdomains</label>
                <div>
                  <input 
                    type="number" 
                    className="input-field w-full py-xs px-sm text-sm" 
                    min={-1}
                    value={modalMaxReservations} 
                    onChange={(e) => setModalMaxReservations(Number(e.target.value))} 
                    placeholder="3"
                  />
                </div>
              </div>

              <div>
                <label className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase">Max Tunnels</label>
                <div>
                  <input 
                    type="number" 
                    className="input-field w-full py-xs px-sm text-sm" 
                    min={-1}
                    value={modalMaxTunnels} 
                    onChange={(e) => setModalMaxTunnels(Number(e.target.value))} 
                    placeholder="3"
                  />
                </div>
              </div>

              <div className="flex flex-col justify-center">
                <label className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase">MFA Security Status</label>
                <div className="flex items-center gap-md">
                  {selectedUser.totp_enabled ? (
                    <>
                      <span className="badge badge-success">Enabled</span>
                      <button type="button" className="btn btn-danger py-xs px-sm text-xs" onClick={resetUserMFA}>Reset MFA</button>
                    </>
                  ) : (
                    <span className="badge" style={{ background: 'rgba(255,255,255,0.05)', color: 'var(--text-muted)' }}>Inactive</span>
                  )}
                </div>
              </div>
            </div>

            <div className="flex justify-end mt-md mb-xl">
              <button 
                type="button"
                className="btn btn-primary py-sm px-lg text-sm w-auto" 
                onClick={updateQuotas} 
                disabled={updatingLimits} 
              >
                {updatingLimits ? 'Saving...' : 'Save Quotas'}
              </button>
            </div>

            <h4 className="section-title mb-lg border-b pb-xs flex items-center">
              Connected Tunnels <span className="badge ml-sm">{(selectedUser.active_tunnels || []).length}</span>
            </h4>
            
            <div className="table-responsive border rounded">
              <table className="w-full m-0">
                <tbody>
                  {!(selectedUser.active_tunnels || []).length && (
                    <tr>
                      <td colSpan={4} className="td-empty">No active tunnels connected.</td>
                    </tr>
                  )}
                  {(selectedUser.active_tunnels || []).map((t) => {
                    const publicUrl = `https://${t.full_host}`;
                    return (
                      <tr key={t.subdomain_prefix} className="border-b">
                        <td className="td-cell align-middle">
                          <div className="fw-semibold font-mono text-sm">{t.subdomain_prefix}</div>
                          <div className="text-2xs text-muted mt-2xs">Local Port: {t.local_port}</div>
                        </td>
                        <td className="td-cell align-middle">
                          <a href={publicUrl} target="_blank" rel="noreferrer" className="text-primary no-underline text-sm font-mono break-all">{publicUrl}</a>
                          {t.node_id && t.node_id !== 'control' ? (
                            <span className="badge badge-node text-2xs ml-xs">
                              🌍 {t.node_id}
                            </span>
                          ) : (
                            <span className="badge badge-control text-2xs ml-xs">
                              🇬🇧 Control
                            </span>
                          )}
                          <div className="text-2xs text-muted mt-2xs">
                            IP: {t.client_ip} | Connected: {formatDate(t.created_at)}
                          </div>
                        </td>
                        <td className="td-cell align-middle text-xs text-muted">
                          <div>📥 In: <strong>{formatBytes(t.bytes_in)}</strong></div>
                          <div className="mt-2xs">📤 Out: <strong>{formatBytes(t.bytes_out)}</strong></div>
                        </td>
                        <td className="td-cell align-middle text-right">
                          <button type="button" className="btn btn-danger py-xs px-md text-xs" onClick={() => kickTunnel(t.subdomain_prefix)}>Kick</button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <h4 className="section-title mb-lg border-b pb-xs flex items-center mt-xl">
              Personal Access Tokens <span className="badge ml-sm">{selectedUserPATs.length}</span>
            </h4>

            <div className="table-responsive border rounded mb-xl">
              <table className="w-full m-0">
                <thead>
                  <tr className="border-b text-left">
                    <th className="th-col text-xs">Name</th>
                    <th className="th-col text-xs">Prefix</th>
                    <th className="th-col text-xs">Expires</th>
                    <th className="th-col text-xs">Status</th>
                    <th className="th-col text-xs text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {!selectedUserPATs.length && (
                    <tr>
                      <td colSpan={5} className="td-empty">No tokens found for this user.</td>
                    </tr>
                  )}
                  {selectedUserPATs.map((pat) => {
                    const isRevoked = pat.revoked_at != null && !pat.revoked_at.startsWith('0001-01-01');
                    const isExpired = pat.expires_at && !pat.expires_at.startsWith('0001-01-01') && new Date(pat.expires_at) < new Date();
                    
                    let statusBadge = (
                      <span className="badge badge-success">active</span>
                    );
                    if (isRevoked) {
                      statusBadge = <span className="badge badge-danger">revoked</span>;
                    } else if (isExpired) {
                      statusBadge = <span className="badge badge-warning">expired</span>;
                    }

                    return (
                      <tr key={pat.id} className="border-b">
                        <td className="td-cell align-middle text-sm">{pat.name}</td>
                        <td className="td-cell align-middle text-sm font-mono">{pat.token_prefix}...</td>
                        <td className="td-cell align-middle text-sm">
                          {pat.expires_at && !pat.expires_at.startsWith('0001-01-01') ? formatDate(pat.expires_at) : 'Never'}
                        </td>
                        <td className="td-cell align-middle">{statusBadge}</td>
                        <td className="td-cell align-middle text-right">
                          {!isRevoked && (
                            <div className="flex gap-xs justify-end">
                              <button 
                                type="button"
                                className="btn btn-outline py-2xs px-xs text-2xs"
                                onClick={() => extendUserToken(pat.id, 30)}
                              >
                                +30d
                              </button>
                              <button 
                                type="button"
                                className="btn btn-outline py-2xs px-xs text-2xs"
                                onClick={() => extendUserToken(pat.id, 90)}
                              >
                                +90d
                              </button>
                              <button 
                                type="button"
                                className="btn btn-outline py-2xs px-xs text-2xs"
                                onClick={() => extendUserToken(pat.id, 0)}
                              >
                                Perm
                              </button>
                              <button 
                                type="button"
                                className="btn btn-danger py-2xs px-xs text-2xs"
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
        <div className="fixed inset-0 z-10 flex items-center justify-center p-md bg-black/50">
          <div 
            className="w-full max-w-sm p-lg bg-body border rounded shadow-lg"
            role="dialog"
            aria-modal="true"
            aria-labelledby="direct-message-modal-title"
          >
            <div className="flex items-center justify-between mb-md">
              <h3 id="direct-message-modal-title" className="text-lg font-bold">Send Direct Message</h3>
              <button type="button" onClick={() => setTargetedUserId('')} className="text-muted hover:text-white" aria-label={t('close', 'Close')}>✕</button>
            </div>
            <p className="text-sm text-muted mb-lg">
              Push a real-time banner alert to this specific active developer session.
            </p>
            <div className="mb-lg">
              <textarea
                className="w-full p-sm bg-surface border rounded text-sm"
                placeholder={t('enter_your_message_placeholder', 'Enter your message...')}
                rows={3}
                value={targetedMessage}
                onChange={(e) => setTargetedMessage(e.target.value)}
              />
            </div>
            <div className="flex justify-end gap-sm">
              <button type="button" className="btn btn-secondary" onClick={() => setTargetedUserId('')}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={isSendingTargeted || !targetedMessage.trim()} onClick={sendTargetedMessage}>
                {isSendingTargeted ? 'Sending...' : 'Send Message'}
              </button>
            </div>
          </div>
        </div>
      )}

      {showInviteModal && (
        <div className="fixed inset-0 z-10 flex items-center justify-center p-md bg-black/50">
          <div 
            className="w-full max-w-sm p-lg bg-body border rounded shadow-lg"
            role="dialog"
            aria-modal="true"
            aria-labelledby="invite-user-modal-title"
          >
            <div className="flex items-center justify-between mb-md">
              <h3 id="invite-user-modal-title" className="text-lg font-bold">{t('invite_user', 'Invite User')}</h3>
              <button type="button" onClick={() => setShowInviteModal(false)} className="text-muted hover:text-white" aria-label={t('close', 'Close')}>✕</button>
            </div>
            {inviteError && <div className="p-sm mb-lg bg-danger/10 text-danger border border-danger/20 rounded text-sm">{inviteError}</div>}
            <form onSubmit={submitInvite}>
              <div className="mb-md">
                <label className="block text-xs uppercase tracking-wider text-muted mb-xs">{t('email_address', 'Email Address')}</label>
                <input type="email" required className="w-full p-sm bg-surface border rounded text-sm" value={inviteForm.email} onChange={(e) => setInviteForm({...inviteForm, email: e.target.value})} placeholder={t('invite_email_placeholder', 'user@company.com')} />
              </div>
              <div className="mb-md">
                <label className="block text-xs uppercase tracking-wider text-muted mb-xs">{t('first_name', 'First Name')}</label>
                <input type="text" required className="w-full p-sm bg-surface border rounded text-sm" value={inviteForm.first_name} onChange={(e) => setInviteForm({...inviteForm, first_name: e.target.value})} placeholder={t('first_name_placeholder', 'John')} />
              </div>
              <div className="mb-md">
                <label className="block text-xs uppercase tracking-wider text-muted mb-xs">{t('last_name', 'Last Name')}</label>
                <input type="text" required className="w-full p-sm bg-surface border rounded text-sm" value={inviteForm.last_name} onChange={(e) => setInviteForm({...inviteForm, last_name: e.target.value})} placeholder={t('last_name_placeholder', 'Doe')} />
              </div>
              <div className="mb-lg">
                <label className="block text-xs uppercase tracking-wider text-muted mb-xs">{t('language_preference', 'Language Preference')}</label>
                <select className="w-full p-sm bg-surface border rounded text-sm" value={inviteForm.language_preference} onChange={(e) => setInviteForm({...inviteForm, language_preference: e.target.value})}>
                  <option value="en">English (UK)</option>
                  <option value="en-us">English (US)</option>
                  <option value="de">Deutsch (DE)</option>
                  <option value="es">Español (ES)</option>
                  <option value="fr">Français (FR)</option>
                </select>
              </div>
              <div className="flex justify-end gap-sm">
                <button type="button" className="btn btn-secondary" onClick={() => setShowInviteModal(false)}>{t('cancel', 'Cancel')}</button>
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
