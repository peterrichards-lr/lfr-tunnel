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
import ModalShell from '../components/ModalShell';

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
  max_custom_domains?: number;
  max_tunnels?: number;
  // The per-user bandwidth override, and the standing the enforcer computed for this user
  // (#1959). used_bytes is the ENFORCED measure (in + out); used_out_bytes is the egress
  // within it, carried separately because that is the half that maps to the AWS invoice.
  bandwidth_quota_bytes?: number | null;
  bandwidth_quota?: {
    state: string;
    allowance_bytes: number;
    used_bytes: number;
    used_out_bytes: number;
    throttle_at_bytes: number;
    period_start: string;
    measured: boolean;
  };
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

// One user's bandwidth-quota standing, rendered for the table (#1959).
//
// Both numbers, always. The cap is enforced on the total because that is the fairness
// measure and the one a user cannot dodge by pulling heavily inbound; the egress is shown
// beside it because that is what the AWS invoice charges for. A "not measured yet" standing
// renders as an em dash rather than as 0, because a gateway that has not swept and a user
// who sent nothing are different facts.
// `t` is threaded in rather than read from the hook: this is a module-level helper, so
// useI18n() cannot be called here (#2248).
const renderQuotaUsage = (
  q: User['bandwidth_quota'],
  t: (key: string, fallback: string) => string,
) => {
  if (!q) return null;
  if (!q.allowance_bytes || q.allowance_bytes <= 0) {
    return (
      <div>
        <span className="text-2xs text-muted">Bandwidth:</span>{' '}
        <strong>∞</strong>
      </div>
    );
  }
  const badge =
    q.state === 'throttled' ? (
      <span className="badge badge-warning">throttled</span>
    ) : q.state === 'stopped' ? (
      <span className="badge badge-danger">stopped</span>
    ) : null;
  return (
    <div
      title={t(
        'bandwidth_quota_total_note',
        'Enforced on the total (in + out). Egress is shown separately because that is the figure that maps to the AWS invoice.',
      )}
    >
      <span className="text-2xs text-muted">Bandwidth:</span>{' '}
      <strong>
        {q.measured ? formatBytes(q.used_bytes) : '—'} /{' '}
        {formatBytes(q.allowance_bytes)}
      </strong>{' '}
      <span className="text-2xs text-muted">
        ({q.measured ? formatBytes(q.used_out_bytes) : '—'} out)
      </span>{' '}
      {badge}
    </div>
  );
};

// Status -> badge class, as data rather than as a ternary (#1851).
//
// There were two ternaries and they defaulted in OPPOSITE directions: the table fell through to
// badge-danger, the detail panel to badge-warning. So the same user rendered red in one and amber
// in the other -- which #1847 fixed for 'rejected' by naming it in both, while leaving the
// structure that caused it. `unverified` was still doing exactly this: red in the table, amber in
// the panel, for a registration that has merely not confirmed its email yet.
//
// A map has no default to disagree about, and an unknown status is now visibly unknown rather
// than silently taking whichever branch happened to be last. The keys must match
// pkg/db/user_status.go's UserStatuses exactly; scripts/check-status-vocabulary.cjs enforces that.
const STATUS_BADGE: Record<string, string> = {
  // Terminal and good.
  approved: 'badge-success',
  // In progress: the registration is moving, nobody has declined anything.
  unverified: 'badge-warning',
  pending: 'badge-warning',
  // Terminal and not good. Amber would read as "in progress", which is the opposite of a
  // declined or withdrawn account.
  rejected: 'badge-danger',
  revoked: 'badge-danger',
};

// An unrecognised status is a bug -- either the server grew one the portal has not learned, or a
// literal is misspelled. Neutral styling makes it visible instead of dressing it as success or
// failure; the gate is what stops it reaching here in the first place.
const statusBadgeClass = (status: string): string =>
  STATUS_BADGE[status] ?? 'badge-secondary';

// A queued collection is not synchronous (#1763): the command waits for the client's next
// tunnel-status heartbeat -- up to 5s -- and only then does the client collect, redact and
// upload its logs. So the bundle cannot exist when the POST returns, and a single re-fetch
// would show an admin exactly what the bug in #1944 showed them: nothing.
const DIAG_POLL_INTERVAL_MS = 3000;
// A ceiling, not an estimate of how long an upload takes. A client that is asleep, or whose
// upload never completes, has to end at a message saying so -- a spinner that never resolves is
// worse than the snapshot it replaced.
const DIAG_POLL_TIMEOUT_MS = 60000;

export default function AdminUsers() {
  const { t } = useI18n();
  const { user: currentUser } = useOutletContext<{ user: any }>();
  const { showToast, showConfirm, showPrompt } = useUI();
  const { formatDate } = useSettings();
  const [loadError, setLoadError] = useState('');
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [serverConfig, setServerConfig] = useState<any>(null);
  const [activeTab, setActiveTab] = useState<'users' | 'registrations'>(
    'users',
  );
  const [selectedUserPATs, setSelectedUserPATs] = useState<any[]>([]);
  const [diagBundles, setDiagBundles] = useState<any[]>([]);
  const [diagRetention, setDiagRetention] = useState<number>(0);
  const [diagBusy, setDiagBusy] = useState(false);
  // Non-null while waiting for a queued collection to arrive; the value is the wall-clock
  // instant the wait gives up at (#1944).
  const [diagPollDeadline, setDiagPollDeadline] = useState<number | null>(null);
  const [diagPollTimedOut, setDiagPollTimedOut] = useState(false);
  const [_domains, _setDomains] = useState<string[]>([]);

  useEffect(() => {
    // A wait belongs to the dialog that started it: opening a different user must not inherit
    // the previous one's "waiting" or "nothing arrived" state (#1944).
    setDiagPollDeadline(null);
    setDiagPollTimedOut(false);
    if (!selectedUser?.email) {
      setSelectedUserPATs([]);
      return;
    }
    const fetchUserDetails = async () => {
      try {
        const res = await axios.get(
          `/api/admin/users/${encodeURIComponent(selectedUser.email)}`,
        );
        setSelectedUserPATs(res.data.pats || []);
        // Collected diagnostic logs (#1894). A separate call, and a tolerated failure: a
        // gateway that has never collected anything is the normal case, and it must not make
        // the rest of this panel look broken.
        try {
          const bundles = await axios.get(
            `/api/admin/diagnostics/bundles?email=${encodeURIComponent(selectedUser.email)}`,
          );
          setDiagBundles(bundles.data?.bundles || []);
          setDiagRetention(bundles.data?.retention_days || 0);
        } catch {
          setDiagBundles([]);
        }
      } catch (err: any) {
        // An empty PAT list is indistinguishable from "this user has none" (#1868).
        console.error('Failed to fetch user details', err);
        setLoadError(
          err.response?.data?.error ||
            t(
              'admin_load_failed',
              'Could not load this page. The server may be unreachable — what you see is not current.',
            ),
        );
      }
    };
    fetchUserDetails();
  }, [selectedUser?.email]);

  // Wait for a queued collection to turn into a bundle (#1944).
  //
  // Keyed on the selected user as well as the deadline, so React's own cleanup is what stops it:
  // closing the dialog or picking another user unmounts/re-runs this effect and clears the
  // interval. There is no path where the poll can outlive the panel it writes into.
  useEffect(() => {
    const email = selectedUser?.email;
    if (diagPollDeadline === null || !email) return;
    let cancelled = false;
    // The count this wait is looking to exceed. Established by the first read below rather than
    // taken from the list the dialog opened with: that snapshot can be minutes old -- long
    // enough for another administrator's collection to have landed -- and counting it would end
    // the wait on somebody else's bundle.
    let baseline: number | null = null;
    const tick = async () => {
      try {
        const res = await axios.get(
          `/api/admin/diagnostics/bundles?email=${encodeURIComponent(email)}`,
        );
        // Re-checked after the await as well: the dialog can close while a read is in flight.
        if (cancelled) return;
        const bundles = res.data?.bundles || [];
        if (baseline === null) {
          baseline = bundles.length;
        } else if (bundles.length > baseline) {
          setDiagBundles(bundles);
          setDiagRetention(res.data?.retention_days || 0);
          setDiagPollDeadline(null);
          return;
        }
      } catch {
        // A failed read is not an answer, so it does not end the wait -- only the deadline
        // below does. Otherwise one gateway blip reads to the admin as "nothing arrived".
      }
      if (cancelled) return;
      if (Date.now() >= diagPollDeadline) {
        setDiagPollDeadline(null);
        setDiagPollTimedOut(true);
      }
    };
    // Immediately, to fix the baseline; the interval is what waits for it to be exceeded.
    void tick();
    const id = setInterval(tick, DIAG_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [diagPollDeadline, selectedUser?.email]);

  // Ask this user's client for its logs (#1763). The endpoint answers on two axes: whether the
  // request was permitted (consent) and whether it could be delivered (is a client connected),
  // and both are surfaced -- a consenting user whose laptop is shut is not a refusal.
  const collectDiagnostics = async () => {
    if (!selectedUser) return;
    setDiagBusy(true);
    setDiagPollDeadline(null);
    setDiagPollTimedOut(false);
    try {
      const res = await axios.post('/api/admin/diagnostics/collect', {
        email: selectedUser.email,
      });
      const d = res.data || {};
      const queued = d.delivery === 'queued';
      showToast(
        d.delivery_detail || t('diag_requested', 'Collection requested.'),
        queued ? 'success' : 'info',
      );
      // Only a QUEUED command can ever produce a bundle. A request that was authorised but
      // delivered nowhere -- no client connected, or the client served by another gateway --
      // never will, so waiting on one would be a lie dressed as patience. Those cases keep the
      // delivery_detail the toast just showed and leave the button usable (#1944).
      if (queued) {
        setDiagPollDeadline(Date.now() + DIAG_POLL_TIMEOUT_MS);
      }
    } catch (err: any) {
      showToast(
        err.response?.data?.error ||
          t('diag_refused', 'The collection could not be requested.'),
        'error',
      );
    } finally {
      setDiagBusy(false);
    }
  };

  const extendUserToken = async (tokenId: number, days: number) => {
    if (!selectedUser) return;
    try {
      await axios.post(`/api/admin/tokens/${tokenId}/extend`, { days });
      showToast('Token updated successfully', 'success');
      const res = await axios.get(
        `/api/admin/users/${encodeURIComponent(selectedUser.email)}`,
      );
      setSelectedUserPATs(res.data.pats || []);
    } catch {
      showToast('Failed to extend token', 'error');
    }
  };

  const revokeUserToken = async (tokenId: number) => {
    if (!selectedUser) return;
    if (
      !(await showConfirm(
        'Revoke Token',
        'Are you sure you want to revoke this Personal Access Token? This will permanently disable it.',
      ))
    )
      return;
    try {
      await axios.delete(`/api/admin/tokens/${tokenId}`);
      showToast('Token revoked successfully', 'success');
      const res = await axios.get(
        `/api/admin/users/${encodeURIComponent(selectedUser.email)}`,
      );
      setSelectedUserPATs(res.data.pats || []);
    } catch {
      showToast('Failed to revoke token', 'error');
    }
  };

  // Targeted Message State
  const [targetedUserId, setTargetedUserId] = useState('');
  const [targetedMessage, setTargetedMessage] = useState('');
  const [isSendingTargeted, setIsSendingTargeted] = useState(false);

  const [showInviteModal, setShowInviteModal] = useState(false);
  const [modalRateLimit, setModalRateLimit] = useState(0);
  const [modalMaxReservations, setModalMaxReservations] = useState(3);
  const [modalMaxCustomDomains, setModalMaxCustomDomains] = useState(1);
  const [modalMaxTunnels, setModalMaxTunnels] = useState(3);
  // Held in GiB because that is the unit an administrator thinks in; converted to bytes on
  // the way out, which is the unit the API and the database use (#1959).
  const [modalQuotaGiB, setModalQuotaGiB] = useState(0);
  const [updatingLimits, setUpdatingLimits] = useState(false);

  useEffect(() => {
    if (selectedUser) {
      setModalRateLimit(selectedUser.rate_limit || 0);
      setModalMaxReservations(
        selectedUser.max_reservations !== undefined &&
          selectedUser.max_reservations !== null
          ? selectedUser.max_reservations
          : 3,
      );
      setModalMaxCustomDomains(
        selectedUser.max_custom_domains !== undefined &&
          selectedUser.max_custom_domains !== null
          ? selectedUser.max_custom_domains
          : 1,
      );
      setModalMaxTunnels(
        selectedUser.max_tunnels !== undefined &&
          selectedUser.max_tunnels !== null
          ? selectedUser.max_tunnels
          : 3,
      );
      // The resolved allowance, not just the override, so the field shows what is in force
      // rather than a blank that reads as "no limit" when a role or fleet default applies.
      setModalQuotaGiB(
        Math.round(
          ((selectedUser.bandwidth_quota_bytes ??
            selectedUser.bandwidth_quota?.allowance_bytes ??
            0) /
            (1024 * 1024 * 1024)) *
            100,
        ) / 100,
      );
    }
  }, [selectedUser]);

  const updateQuotas = async () => {
    if (!selectedUser) return;
    try {
      setUpdatingLimits(true);
      await axios.patch(
        `/api/admin/users/${encodeURIComponent(selectedUser.email)}`,
        {
          rate_limit: Number(modalRateLimit),
          max_reservations: Number(modalMaxReservations),
          max_custom_domains: Number(modalMaxCustomDomains),
          max_tunnels: Number(modalMaxTunnels),
          bandwidth_quota_bytes: Math.round(
            Number(modalQuotaGiB) * 1024 * 1024 * 1024,
          ),
        },
      );
      showToast('User settings updated successfully', 'success');
      setUsers((prev) =>
        prev.map((u) =>
          u.email === selectedUser.email
            ? {
                ...u,
                rate_limit: Number(modalRateLimit),
                max_reservations: Number(modalMaxReservations),
                max_custom_domains: Number(modalMaxCustomDomains),
                max_tunnels: Number(modalMaxTunnels),
                bandwidth_quota_bytes: Math.round(
                  Number(modalQuotaGiB) * 1024 * 1024 * 1024,
                ),
              }
            : u,
        ),
      );
      setSelectedUser((prev) =>
        prev
          ? {
              ...prev,
              rate_limit: Number(modalRateLimit),
              max_reservations: Number(modalMaxReservations),
              max_custom_domains: Number(modalMaxCustomDomains),
              max_tunnels: Number(modalMaxTunnels),
              bandwidth_quota_bytes: Math.round(
                Number(modalQuotaGiB) * 1024 * 1024 * 1024,
              ),
            }
          : null,
      );
      fetchUsers();
    } catch (e: any) {
      showToast(
        e.response?.data?.error || 'Failed to update user quotas',
        'error',
      );
    } finally {
      setUpdatingLimits(false);
    }
  };

  const resetUserMFA = async () => {
    if (!selectedUser) return;
    if (
      !(await showConfirm(
        'Reset MFA',
        `Are you sure you want to reset Multi-Factor Authentication (MFA) for ${selectedUser.email}? This will force the user to re-register their TOTP auth device on next login.`,
      ))
    )
      return;
    try {
      await axios.patch(
        `/api/admin/users/${encodeURIComponent(selectedUser.email)}`,
        {
          reset_mfa: true,
        },
      );
      showToast('Multi-Factor Authentication reset successfully', 'success');
      setUsers((prev) =>
        prev.map((u) =>
          u.email === selectedUser.email ? { ...u, totp_enabled: false } : u,
        ),
      );
      setSelectedUser((prev) =>
        prev ? { ...prev, totp_enabled: false } : null,
      );
      fetchUsers();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to reset MFA', 'error');
    }
  };
  const [inviteForm, setInviteForm] = useState({
    email: '',
    first_name: '',
    last_name: '',
    language_preference: 'en',
  });
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
        axios.get('/api/domains').catch(() => ({ data: [] })),
      ]);
      setUsers(res.data);
      if (confRes.data) setServerConfig(confRes.data);
      if (domRes.data) _setDomains(domRes.data);
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
    fetchUsers();
    const interval = setInterval(fetchUsers, 5000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const changeStatus = async (email: string, newStatus: string) => {
    if (
      !(await showConfirm(
        'Change Status',
        `Are you sure you want to mark ${email} as ${newStatus}?`,
      ))
    )
      return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, {
        status: newStatus,
      });
      fetchUsers();
      showToast(`User status marked as ${newStatus}`, 'success');
    } catch {
      showMessage('error', `Failed to mark user as ${newStatus}`);
    }
  };

  const changeRole = async (email: string, newRole: string) => {
    if (
      !(await showConfirm(
        'Change Role',
        `Are you sure you want to change ${email} to ${newRole}?`,
      ))
    )
      return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, {
        role: newRole,
      });
      fetchUsers();
      showToast(`User role updated to ${newRole}`, 'success');
    } catch {
      showMessage('error', `Failed to change role to ${newRole}`);
    }
  };

  const deleteUser = async (email: string) => {
    const confirmation = await showPrompt(
      'Delete User',
      `Type "DELETE" to permanently remove ${email}`,
    );
    if (confirmation !== 'DELETE') {
      if (confirmation !== null)
        showToast(
          'Deletion cancelled: confirmation word did not match.',
          'info',
        );
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
        message: targetedMessage,
      });
      showMessage('success', 'Message sent successfully.');
      setTargetedUserId('');
    } catch (e: any) {
      showMessage(
        'error',
        `Failed to send message: ${e.response?.data?.error || 'Unknown error'}`,
      );
    } finally {
      setIsSendingTargeted(false);
    }
  };

  const kickTunnel = async (subdomain: string) => {
    if (
      !(await showConfirm(
        'Kick Lease',
        `Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`,
      ))
    )
      return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
      // Update selectedUser if open
      setSelectedUser((prev) =>
        prev
          ? {
              ...prev,
              active_tunnels: prev.active_tunnels?.filter(
                (t) => t.subdomain_prefix !== subdomain,
              ),
            }
          : null,
      );
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
      setInviteForm({
        email: '',
        first_name: '',
        last_name: '',
        language_preference: 'en',
      });
      fetchUsers();
    } catch (err: any) {
      setInviteError(err.response?.data?.error || 'Failed to invite user');
    } finally {
      setIsInviting(false);
    }
  };
  const pendingCount = users.filter((u) => u.status === 'pending').length;
  const filteredUsers = useMemo(
    () =>
      users.filter((u) => {
        if (activeTab === 'users') {
          return u.status !== 'pending';
        } else {
          return u.status === 'pending';
        }
      }),
    [users, activeTab],
  );

  const columns: ColumnDef<User>[] = useMemo(
    () => [
      { key: 'email', label: t('th_user', 'User'), sortable: true },
      { key: 'role', label: t('th_role', 'Role'), sortable: true },
      { key: 'status', label: t('status', 'Status'), sortable: true },
      {
        key: 'auth_method',
        label: t('th_auth_method', 'Auth Method'),
        sortable: true,
      },
      { key: 'quotas', label: t('th_quotas', 'Quotas'), sortable: false },
      {
        key: 'last_login_at',
        label: t('th_last_seen', 'Last Seen'),
        sortable: true,
      },
      {
        key: 'created_at',
        label: t('created_at', 'Created Date'),
        sortable: true,
      },
    ],
    [t],
  );

  const statusOptions = useMemo(
    () => [
      { value: 'approved', label: t('status_approved', 'Approved') },
      { value: 'pending', label: t('status_pending', 'Pending') },
      { value: 'unverified', label: t('status_unverified', 'Unverified') },
      // A registration declined by an admin (#1830). This list is both the status filter and
      // the dropdown used to CHANGE a status, so omitting it meant a rejected user could not be
      // filtered for and -- the part that actually mattered -- could not be un-rejected here,
      // which is the control reversing a rejection is meant to use (#1847).
      { value: 'rejected', label: t('status_rejected', 'Rejected') },
      { value: 'revoked', label: t('status_revoked', 'Revoked') },
    ],
    [t],
  );

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
    allColumns,
  } = useDataTable<User>(
    'admin_users',
    filteredUsers,
    ['email', 'first_name', 'last_name', 'role', 'status', 'auth_method'],
    columns,
    10,
    ['created_at'],
    'status',
    statusOptions,
    'all',
  );

  if (loading) {
    return (
      <div className="animate-fade-in">
        <div className="page-header mb-xl">
          <Skeleton width={180} height={28} />
          <Skeleton width={320} height={16} className="mt-sm" />
        </div>

        <div className="card p-xl">
          <div className="table-responsive">
            <table className="w-full">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={100} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={60} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={120} />
                  </th>
                  <th className="th-col">
                    <Skeleton width={80} />
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

      <div className="flex items-center justify-between mb-xl">
        <div>
          <h1 className="page-header__title">
            {t('user_management', 'User Management')}
          </h1>
          <p className="page-header__desc">
            {t(
              'user_management_desc',
              'Manage users, promotion, roles, and pending registration approvals.',
            )}
          </p>
        </div>
        <button
          onClick={() => setShowInviteModal(true)}
          className="btn btn-primary flex items-center gap-xs"
        >
          <span>➕</span>
          <span>{t('invite_user', 'Invite User')}</span>
        </button>
      </div>

      <div className="sub-tabs mb-xl">
        <button
          onClick={() => setActiveTab('users')}
          className={`sub-tab ${activeTab === 'users' ? 'sub-tab--active' : ''}`}
        >
          {t('users_tab_active', 'Active Users')} (
          {users.filter((u) => u.status !== 'pending').length})
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
                {isColumnVisible('email') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('email')}
                    aria-sort={getAriaSort('email')}
                  >
                    {t('th_user', 'User')}
                    {getSortIndicator('email')}
                  </th>
                )}
                {isColumnVisible('role') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('role')}
                    aria-sort={getAriaSort('role')}
                  >
                    {t('th_role', 'Role')}
                    {getSortIndicator('role')}
                  </th>
                )}
                {isColumnVisible('status') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('status')}
                    aria-sort={getAriaSort('status')}
                  >
                    {t('status', 'Status')}
                    {getSortIndicator('status')}
                  </th>
                )}
                {isColumnVisible('auth_method') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('auth_method')}
                    aria-sort={getAriaSort('auth_method')}
                  >
                    {t('th_auth_method', 'Auth Method')}
                    {getSortIndicator('auth_method')}
                  </th>
                )}
                {isColumnVisible('quotas') && (
                  <th className="th-col">{t('th_quotas', 'Quotas')}</th>
                )}
                {isColumnVisible('last_login_at') && (
                  <th
                    className="th-col th-col--sortable"
                    onClick={() => requestSort('last_login_at')}
                    aria-sort={getAriaSort('last_login_at')}
                  >
                    {t('th_last_seen', 'Last Seen')}
                    {getSortIndicator('last_login_at')}
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
              {paginatedUsers.length === 0 ? (
                <tr>
                  <td
                    colSpan={8}
                    className="td-cell text-center text-muted py-xl"
                  >
                    {activeTab === 'users'
                      ? t('no_users_found', 'No users found.')
                      : t(
                          'no_pending_registrations',
                          'No pending registrations.',
                        )}
                  </td>
                </tr>
              ) : (
                paginatedUsers.map((u) => {
                  const isSelf = currentUser && u.email === currentUser.email;
                  return (
                    <tr
                      key={u.email}
                      className={`border-b transition-colors ${isSelf ? 'opacity-60' : ''}`}
                    >
                      {isColumnVisible('email') && (
                        <td className="td-cell">
                          <div className="flex items-center gap-xs">
                            {u.portal_active ? (
                              <div
                                className="status-dot status-dot--online"
                                title={t('status_online', 'Online')}
                              />
                            ) : (
                              <div
                                className="status-dot status-dot--offline"
                                title={t('status_offline', 'Offline')}
                              />
                            )}
                            <div>
                              <div className="fw-medium">
                                {u.first_name || u.last_name
                                  ? `${u.first_name || ''} ${u.last_name || ''}`.trim()
                                  : u.email}
                              </div>
                              <div className="text-muted text-2xs">
                                {u.email}
                              </div>
                              <div className="flex gap-xs mt-2xs">
                                {u.active_tunnels &&
                                  u.active_tunnels.length > 0 && (
                                    <span className="badge badge-info text-2xs px-xs py-0">
                                      🔌 {u.active_tunnels.length} Tunnel
                                      {u.active_tunnels.length > 1 ? 's' : ''}
                                    </span>
                                  )}
                              </div>
                            </div>
                          </div>
                        </td>
                      )}
                      {isColumnVisible('role') && (
                        <td className="td-cell">
                          <span className="badge">{u.role.toLowerCase()}</span>
                        </td>
                      )}
                      {isColumnVisible('status') && (
                        <td className="td-cell">
                          <span
                            className={`badge ${statusBadgeClass(u.status)}`}
                          >
                            {u.status.toLowerCase()}
                          </span>
                        </td>
                      )}
                      {isColumnVisible('auth_method') && (
                        <td className="td-cell">
                          {u.auth_method || 'password'}
                        </td>
                      )}
                      {isColumnVisible('quotas') && (
                        <td className="td-cell text-xs">
                          <div className="flex flex-col gap-2xs">
                            <div>
                              <span className="text-2xs text-muted">RPS:</span>{' '}
                              <strong>
                                {u.rate_limit ? u.rate_limit : '∞'}
                              </strong>
                            </div>
                            <div>
                              <span className="text-2xs text-muted">Subs:</span>{' '}
                              <strong>
                                {u.max_reservations !== undefined &&
                                u.max_reservations !== null
                                  ? u.max_reservations < 0
                                    ? '∞'
                                    : u.max_reservations
                                  : '3'}
                              </strong>
                            </div>
                            <div>
                              <span className="text-2xs text-muted">
                                Domains:
                              </span>{' '}
                              <strong>
                                {u.max_custom_domains !== undefined &&
                                u.max_custom_domains !== null
                                  ? u.max_custom_domains < 0
                                    ? '∞'
                                    : u.max_custom_domains
                                  : '1'}
                              </strong>
                            </div>
                            <div>
                              <span className="text-2xs text-muted">
                                Tunnels:
                              </span>{' '}
                              <strong>
                                {u.max_tunnels !== undefined &&
                                u.max_tunnels !== null
                                  ? u.max_tunnels < 0
                                    ? '∞'
                                    : u.max_tunnels
                                  : '3'}
                              </strong>
                            </div>
                            {renderQuotaUsage(u.bandwidth_quota, t)}
                          </div>
                        </td>
                      )}
                      {isColumnVisible('last_login_at') && (
                        <td className="td-cell">
                          {u.portal_active ? (
                            <span className="text-success fw-semibold">
                              Active Now
                            </span>
                          ) : u.last_login_at ? (
                            formatDate(u.last_login_at)
                          ) : (
                            <span className="text-muted">
                              {t('never', 'Never')}
                            </span>
                          )}
                        </td>
                      )}
                      {isColumnVisible('created_at') && (
                        <td className="td-cell whitespace-nowrap">
                          {u.created_at ? formatDate(u.created_at) : '—'}
                        </td>
                      )}
                      <td className="td-cell text-right whitespace-nowrap">
                        {/* One row of actions. Details is read-only and IS offered for your
                            own row; every mutating action is not (#1898).

                            The guard used to wrap this whole cell, which also hid Details
                            from yourself -- and that is the only route to the detail modal,
                            so an admin could not reach their own tunnels, tokens or
                            diagnostic logs at all. #1894 made that bite: requesting your own
                            logs is the natural way to validate the feature and there was no
                            route to it. What this guard actually protects is self-revocation,
                            and that is still protected. */}
                        <div className="flex gap-xs justify-end">
                          <button
                            className="btn btn-secondary py-xs px-sm text-xs"
                            onClick={() => setSelectedUser(u)}
                          >
                            {t('details', 'Details')}
                          </button>
                          {!isSelf && (
                            <>
                              {u.status === 'pending' ||
                              u.status === 'unverified' ? (
                                <>
                                  <button
                                    className="btn btn-primary py-xs px-sm text-xs"
                                    onClick={() =>
                                      changeStatus(u.email, 'approved')
                                    }
                                  >
                                    Approve
                                  </button>
                                  <button
                                    className="btn btn-danger py-xs px-sm text-xs"
                                    onClick={() =>
                                      changeStatus(u.email, 'revoked')
                                    }
                                  >
                                    Reject
                                  </button>
                                </>
                              ) : (
                                <>
                                  {u.status === 'approved' ? (
                                    <button
                                      className="btn py-xs px-sm text-xs"
                                      onClick={() =>
                                        changeStatus(u.email, 'revoked')
                                      }
                                    >
                                      Suspend
                                    </button>
                                  ) : (
                                    <button
                                      className="btn py-xs px-sm text-xs"
                                      onClick={() =>
                                        changeStatus(u.email, 'approved')
                                      }
                                    >
                                      Unsuspend
                                    </button>
                                  )}

                                  {(currentUser.role === 'owner' ||
                                    u.role !== 'owner') && (
                                    <>
                                      {u.role === 'admin' ||
                                      u.role === 'owner' ? (
                                        <button
                                          className="btn py-xs px-sm text-xs"
                                          onClick={() =>
                                            changeRole(u.email, 'user')
                                          }
                                        >
                                          Demote
                                        </button>
                                      ) : (
                                        <button
                                          className="btn py-xs px-sm text-xs"
                                          onClick={() =>
                                            changeRole(u.email, 'admin')
                                          }
                                        >
                                          Promote
                                        </button>
                                      )}

                                      {u.email.toLowerCase() !==
                                        serverConfig?.owner_email?.toLowerCase() && (
                                        <button
                                          className="btn btn-danger py-xs px-sm text-xs"
                                          onClick={() => deleteUser(u.email)}
                                        >
                                          Delete
                                        </button>
                                      )}
                                    </>
                                  )}
                                </>
                              )}
                            </>
                          )}
                        </div>
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
        <ModalShell
          isOpen
          onClose={() => setSelectedUser(null)}
          labelledBy="user-details-modal-title"
          cardClassName="modal-card modal-card--lg overflow-y-auto"
        >
          <div className="modal-header">
            <h3 id="user-details-modal-title" className="modal-title">
              User Details & Tunnels
            </h3>
            <button
              type="button"
              onClick={() => setSelectedUser(null)}
              className="modal-close"
              aria-label={t('close', 'Close')}
            >
              ✕
            </button>
          </div>

          <div className="auto-grid-md gap-lg mb-xl">
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                {t('name', 'Name')}
              </div>
              <div className="fw-medium">
                {selectedUser.first_name} {selectedUser.last_name}
              </div>
            </div>
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                {t('email', 'Email')}
              </div>
              <div className="fw-medium font-mono">{selectedUser.email}</div>
            </div>
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                Status & Role
              </div>
              <div>
                <span
                  // Same mapping as the table, which is the point: these two used to be separate
                  // ternaries with opposite defaults (#1851).
                  className={`badge ${statusBadgeClass(selectedUser.status)} mr-sm`}
                >
                  {selectedUser.status}
                </span>
                <span
                  className={`badge ${selectedUser.role === 'admin' ? 'badge-success' : ''}`}
                >
                  {selectedUser.role}
                </span>
              </div>
            </div>
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                Origin
              </div>
              <div className="fw-medium capitalize">
                {selectedUser.auth_method || 'Magic Link'}
              </div>
            </div>
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                {t('user_lbl_joined', 'Joined Date')}
              </div>
              <div className="fw-medium">
                {selectedUser.created_at
                  ? formatDate(selectedUser.created_at)
                  : 'N/A'}
              </div>
            </div>
            <div>
              <div className="text-2xs text-muted tracking-wider uppercase mb-2xs">
                {t('user_lbl_quota', 'API Quota')}
              </div>
              <div className="fw-medium">
                {selectedUser.rate_limit
                  ? `${selectedUser.rate_limit} RPS`
                  : 'Unlimited'}
              </div>
            </div>
          </div>

          <div className="flex justify-start mb-xl">
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => setTargetedUserId(selectedUser.id)}
            >
              💬 Direct Message
            </button>
          </div>

          <h4 className="section-title mb-lg border-b pb-xs">
            Quotas & Security
          </h4>
          <div className="auto-grid-md gap-lg p-md rounded border">
            <div>
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="field"
              >
                Rate Limit (RPS)
              </label>
              <div>
                <input
                  id="field"
                  type="number"
                  className="input-field w-full py-xs px-sm text-sm"
                  min={0}
                  value={modalRateLimit}
                  onChange={(e) => setModalRateLimit(Number(e.target.value))}
                  placeholder={t('unlimited', 'Unlimited')}
                />
              </div>
            </div>

            <div>
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="field-2"
              >
                Max Subdomains
              </label>
              <div>
                <input
                  id="field-2"
                  type="number"
                  className="input-field w-full py-xs px-sm text-sm"
                  min={-1}
                  value={modalMaxReservations}
                  onChange={(e) =>
                    setModalMaxReservations(Number(e.target.value))
                  }
                  placeholder="3"
                />
              </div>
            </div>

            <div>
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="field-3"
              >
                Max Custom Domains
              </label>
              <div>
                <input
                  id="field-3"
                  type="number"
                  className="input-field w-full py-xs px-sm text-sm"
                  min={-1}
                  value={modalMaxCustomDomains}
                  onChange={(e) =>
                    setModalMaxCustomDomains(Number(e.target.value))
                  }
                  placeholder="1"
                />
              </div>
            </div>

            <div>
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="field-4"
              >
                Max Tunnels
              </label>
              <div>
                <input
                  id="field-4"
                  type="number"
                  className="input-field w-full py-xs px-sm text-sm"
                  min={-1}
                  value={modalMaxTunnels}
                  onChange={(e) => setModalMaxTunnels(Number(e.target.value))}
                  placeholder="3"
                />
              </div>
            </div>

            <div>
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="field-quota"
              >
                Bandwidth Quota (GiB)
              </label>
              <div>
                <input
                  id="field-quota"
                  type="number"
                  className="input-field w-full py-xs px-sm text-sm"
                  min={0}
                  step={0.01}
                  value={modalQuotaGiB}
                  onChange={(e) => setModalQuotaGiB(Number(e.target.value))}
                  placeholder="50"
                />
                <span className="text-2xs text-muted">
                  Enforced on the total of inbound and outbound traffic per
                  period. 0 exempts this user.
                </span>
              </div>
            </div>

            <div className="flex flex-col justify-center">
              <label
                className="form-label text-2xs text-muted mb-2xs tracking-wider uppercase"
                htmlFor="close"
              >
                MFA Security Status
              </label>
              <div className="flex items-center gap-md">
                {selectedUser.totp_enabled ? (
                  <>
                    <span className="badge badge-success">
                      {t('enabled', 'Enabled')}
                    </span>
                    <button
                      type="button"
                      className="btn btn-danger py-xs px-sm text-xs"
                      onClick={resetUserMFA}
                    >
                      Reset MFA
                    </button>
                  </>
                ) : (
                  <span className="badge neutral">Inactive</span>
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
            {t('user_lbl_connected_tunnels', 'Connected Tunnels')}{' '}
            <span className="badge ml-sm">
              {(selectedUser.active_tunnels || []).length}
            </span>
          </h4>

          <div className="table-responsive border rounded">
            <table className="w-full m-0">
              <tbody>
                {!(selectedUser.active_tunnels || []).length && (
                  <tr>
                    <td colSpan={4} className="td-empty">
                      No active tunnels connected.
                    </td>
                  </tr>
                )}
                {(selectedUser.active_tunnels || []).map((tunnel) => {
                  const publicUrl = `https://${tunnel.full_host}`;
                  return (
                    <tr key={tunnel.subdomain_prefix} className="border-b">
                      <td className="td-cell align-middle">
                        <div className="fw-semibold font-mono text-sm">
                          {tunnel.subdomain_prefix}
                        </div>
                        <div className="text-2xs text-muted mt-2xs">
                          Local Port: {tunnel.local_port}
                        </div>
                      </td>
                      <td className="td-cell align-middle">
                        <a
                          href={publicUrl}
                          target="_blank"
                          rel="noreferrer"
                          className="text-primary no-underline text-sm font-mono break-all"
                        >
                          {publicUrl}
                        </a>
                        {tunnel.node_id && tunnel.node_id !== 'control' ? (
                          <span className="badge badge-node text-2xs ml-xs">
                            🌍 {tunnel.node_id}
                          </span>
                        ) : (
                          <span className="badge badge-control text-2xs ml-xs">
                            🇬🇧 Control
                          </span>
                        )}
                        <div className="text-2xs text-muted mt-2xs">
                          IP: {tunnel.client_ip} | Connected:{' '}
                          {formatDate(tunnel.created_at)}
                        </div>
                      </td>
                      <td className="td-cell align-middle text-xs text-muted">
                        <div>
                          📥 In: <strong>{formatBytes(tunnel.bytes_in)}</strong>
                        </div>
                        <div className="mt-2xs">
                          📤 Out:{' '}
                          <strong>{formatBytes(tunnel.bytes_out)}</strong>
                        </div>
                      </td>
                      <td className="td-cell align-middle text-right">
                        <button
                          type="button"
                          className="btn btn-danger py-xs px-md text-xs"
                          onClick={() => kickTunnel(tunnel.subdomain_prefix)}
                        >
                          {t('action_kick', 'Kick')}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <h4 className="section-title mb-lg border-b pb-xs flex items-center mt-xl">
            {t('pat_title', 'Personal Access Tokens')}{' '}
            <span className="badge ml-sm">{selectedUserPATs.length}</span>
          </h4>

          <div className="table-responsive border rounded mb-xl">
            <table className="w-full m-0">
              <thead>
                <tr className="border-b text-left">
                  <th className="th-col text-xs">{t('name', 'Name')}</th>
                  <th className="th-col text-xs">{t('prefix', 'Prefix')}</th>
                  <th className="th-col text-xs">{t('expires', 'Expires')}</th>
                  <th className="th-col text-xs">{t('status', 'Status')}</th>
                  <th className="th-col text-xs text-right">
                    {t('actions', 'Actions')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {!selectedUserPATs.length && (
                  <tr>
                    <td colSpan={5} className="td-empty">
                      No tokens found for this user.
                    </td>
                  </tr>
                )}
                {selectedUserPATs.map((pat) => {
                  const isRevoked =
                    pat.revoked_at != null &&
                    !pat.revoked_at.startsWith('0001-01-01');
                  const isExpired =
                    pat.expires_at &&
                    !pat.expires_at.startsWith('0001-01-01') &&
                    new Date(pat.expires_at) < new Date();

                  let statusBadge = (
                    <span className="badge badge-success">
                      {t('status_active', 'active')}
                    </span>
                  );
                  if (isRevoked) {
                    statusBadge = (
                      <span className="badge badge-danger">
                        {t('revoked', 'revoked')}
                      </span>
                    );
                  } else if (isExpired) {
                    statusBadge = (
                      <span className="badge badge-warning">
                        {t('status_expired', 'expired')}
                      </span>
                    );
                  }

                  return (
                    <tr key={pat.id} className="border-b">
                      <td className="td-cell align-middle text-sm">
                        {pat.name}
                      </td>
                      <td className="td-cell align-middle text-sm font-mono">
                        {pat.token_prefix}...
                      </td>
                      <td className="td-cell align-middle text-sm">
                        {pat.expires_at &&
                        !pat.expires_at.startsWith('0001-01-01')
                          ? formatDate(pat.expires_at)
                          : 'Never'}
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
                              {t('revoke', 'Revoke')}
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

          {/* Diagnostic logs (#1894, completing #1763).

              Consent is the user's, not the admin's: the button is offered either way and the
              server refuses when consent is absent, because hiding it would leave an admin
              unable to tell "not allowed" from "feature missing". The response says which. */}
          <h4 className="section-title mb-lg border-b pb-xs flex items-center mt-xl">
            {t('diag_logs', 'Diagnostic Logs')}{' '}
            <span className="badge ml-sm">{diagBundles.length}</span>
          </h4>

          <div className="mb-lg">
            {/* Disabled while waiting as well as while posting (#1944): clicking again queues a
                second collection, which the consenting user has to be bothered by all over
                again -- and repeating it is exactly what a flow that looks inert invites. */}
            <button
              className="btn btn-secondary"
              onClick={collectDiagnostics}
              disabled={diagBusy || diagPollDeadline !== null}
            >
              {diagBusy || diagPollDeadline !== null
                ? t('diag_collecting', 'Requesting...')
                : t('diag_collect', 'Request logs from this client')}
            </button>
            <p className="text-muted text-xs mt-sm mb-0">
              {t(
                'diag_consent_note',
                'Only collected if this user has turned diagnostic log sharing on. Every request, and every download below, is recorded in the audit log.',
              )}
            </p>
            {/* "Still waiting" and "nothing arrived" are different statements, and both have to
                stay on screen -- a toast scrolls away, and the admin is waiting on a person. */}
            {diagPollDeadline !== null && (
              <p className="text-muted text-xs mt-sm mb-0">
                {t(
                  'diag_waiting',
                  'Waiting for this client to send its logs. It picks the request up on its next heartbeat, so this can take up to a minute.',
                )}
              </p>
            )}
            {diagPollTimedOut && (
              <p className="text-warning text-xs mt-sm mb-0">
                {t(
                  'diag_timeout',
                  'No logs arrived. The client may be offline, or its upload did not complete. You can request them again.',
                )}
              </p>
            )}
          </div>

          {diagBundles.length > 0 && (
            <div className="table-responsive border rounded mb-xl">
              <table className="w-full m-0">
                <thead>
                  <tr className="border-b text-left">
                    <th className="th-col text-xs">{t('diag_kind', 'Log')}</th>
                    <th className="th-col text-xs">
                      {t('diag_collected', 'Collected')}
                    </th>
                    <th className="th-col text-xs">{t('diag_size', 'Size')}</th>
                    <th className="th-col text-xs text-right">
                      {t('diag_download', 'Download')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {diagBundles.map((b: any) => (
                    <tr key={b.id} className="border-b">
                      <td className="p-md fw-semibold">{b.kind}</td>
                      <td className="p-md text-muted">
                        {new Date(b.collected_at).toLocaleString()}
                      </td>
                      <td className="p-md text-muted">
                        {Math.max(1, Math.round(b.bytes / 1024))} KB
                        {b.truncated ? ' *' : ''}
                      </td>
                      <td className="p-md text-right">
                        {/* A plain link, so the browser handles the download and the
                            audit-on-read fires server-side exactly once per open. */}
                        <a
                          className="btn btn-secondary"
                          href={`/api/admin/diagnostics/bundles?id=${encodeURIComponent(b.id)}`}
                        >
                          {t('diag_download', 'Download')}
                        </a>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {diagRetention > 0 && (
                <p className="text-muted text-xs p-md m-0">
                  {t('diag_retention', 'Kept for at most')} {diagRetention}{' '}
                  {t(
                    'diag_retention_days',
                    'days, and deleted immediately if this user withdraws consent.',
                  )}
                </p>
              )}
            </div>
          )}
        </ModalShell>
      )}

      {targetedUserId && (
        <ModalShell
          isOpen
          onClose={() => setTargetedUserId('')}
          labelledBy="direct-message-modal-title"
          cardClassName="modal-card modal-card--sm"
        >
          <div className="modal-header">
            <h3 id="direct-message-modal-title" className="modal-title">
              Send Direct Message
            </h3>
            <button
              type="button"
              onClick={() => setTargetedUserId('')}
              className="modal-close"
              aria-label={t('close', 'Close')}
            >
              ✕
            </button>
          </div>
          <p className="text-sm text-muted mb-lg">
            Push a real-time banner alert to this specific active developer
            session.
          </p>
          <div className="form-group m-0">
            <textarea
              className="input-field"
              placeholder={t(
                'enter_your_message_placeholder',
                'Enter your message...',
              )}
              rows={3}
              value={targetedMessage}
              onChange={(e) => setTargetedMessage(e.target.value)}
              aria-label={t('message_to_user', 'Message to user')}
            />
          </div>
          <div className="flex justify-end gap-sm">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setTargetedUserId('')}
            >
              {t('btn_cancel', 'Cancel')}
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={isSendingTargeted || !targetedMessage.trim()}
              onClick={sendTargetedMessage}
            >
              {isSendingTargeted ? 'Sending...' : 'Send Message'}
            </button>
          </div>
        </ModalShell>
      )}

      {showInviteModal && (
        <ModalShell
          isOpen
          onClose={() => setShowInviteModal(false)}
          labelledBy="invite-user-modal-title"
          cardClassName="modal-card modal-card--sm"
        >
          <div className="modal-header">
            <h3 id="invite-user-modal-title" className="modal-title">
              {t('invite_user', 'Invite User')}
            </h3>
            <button
              type="button"
              onClick={() => setShowInviteModal(false)}
              className="modal-close"
              aria-label={t('close', 'Close')}
            >
              ✕
            </button>
          </div>
          {inviteError && (
            <div className="alert-banner alert-banner--danger">
              {inviteError}
            </div>
          )}
          <form onSubmit={submitInvite}>
            <div className="form-group m-0">
              <label className="form-label text-xs">
                {t('email_address', 'Email Address')}
              </label>
              <input
                id="close"
                type="email"
                required
                className="input-field"
                value={inviteForm.email}
                onChange={(e) =>
                  setInviteForm({ ...inviteForm, email: e.target.value })
                }
                placeholder={t('invite_email_placeholder', 'user@company.com')}
              />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs" htmlFor="first-name">
                {t('first_name', 'First Name')}
              </label>
              <input
                id="first-name"
                type="text"
                required
                className="input-field"
                value={inviteForm.first_name}
                onChange={(e) =>
                  setInviteForm({ ...inviteForm, first_name: e.target.value })
                }
                placeholder={t('first_name_placeholder', 'John')}
              />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs" htmlFor="last-name">
                {t('last_name', 'Last Name')}
              </label>
              <input
                id="last-name"
                type="text"
                required
                className="input-field"
                value={inviteForm.last_name}
                onChange={(e) =>
                  setInviteForm({ ...inviteForm, last_name: e.target.value })
                }
                placeholder={t('last_name_placeholder', 'Doe')}
              />
            </div>
            <div className="form-group m-0">
              <label
                className="form-label text-xs"
                htmlFor="language-preference"
              >
                {t('language_preference', 'Language Preference')}
              </label>
              <select
                id="language-preference"
                className="input-field"
                value={inviteForm.language_preference}
                onChange={(e) =>
                  setInviteForm({
                    ...inviteForm,
                    language_preference: e.target.value,
                  })
                }
              >
                <option value="en">English (UK)</option>
                <option value="en-us">English (US)</option>
                <option value="de">Deutsch (DE)</option>
                <option value="es">Español (ES)</option>
                <option value="fr">Français (FR)</option>
              </select>
            </div>
            <div className="flex justify-end gap-sm">
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setShowInviteModal(false)}
              >
                {t('cancel', 'Cancel')}
              </button>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={isInviting}
              >
                {isInviting
                  ? t('sending', 'Sending...')
                  : t('send_invitation', 'Send Invitation')}
              </button>
            </div>
          </form>
        </ModalShell>
      )}
    </div>
  );
}
