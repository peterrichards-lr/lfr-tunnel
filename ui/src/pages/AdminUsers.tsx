import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';

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
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [serverConfig, setServerConfig] = useState<any>(null);
  
  // Targeted Message State
  const [targetedUserId, setTargetedUserId] = useState('');
  const [targetedMessage, setTargetedMessage] = useState('');
  const [isSendingTargeted, setIsSendingTargeted] = useState(false);

  const [showInviteModal, setShowInviteModal] = useState(false);
  const [inviteForm, setInviteForm] = useState({ email: '', first_name: '', last_name: '', language_preference: 'en' });
  const [inviteError, setInviteError] = useState('');
  const [isInviting, setIsInviting] = useState(false);
  const [pageMessage, setPageMessage] = useState<{type: 'error' | 'success', text: string} | null>(null);

  const showMessage = (type: 'error' | 'success', text: string) => {
    setPageMessage({ type, text });
    setTimeout(() => setPageMessage(null), 5000);
  };

  const fetchUsers = async () => {
    try {
      const [res, confRes] = await Promise.all([
        axios.get('/api/admin/users'),
        axios.get('/api/version').catch(() => ({ data: null }))
      ]);
      setUsers(res.data);
      if (confRes.data) setServerConfig(confRes.data);
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
  }, []);

  const changeStatus = async (email: string, newStatus: string) => {
    if (!confirm(`Are you sure you want to mark ${email} as ${newStatus}?`)) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, { status: newStatus });
      fetchUsers();
    } catch {
      showMessage('error', `Failed to mark user as ${newStatus}`);
    }
  };

  const changeRole = async (email: string, newRole: string) => {
    if (!confirm(`Are you sure you want to change ${email} to ${newRole}?`)) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, { role: newRole });
      fetchUsers();
    } catch {
      showMessage('error', `Failed to change role to ${newRole}`);
    }
  };

  const deleteUser = async (email: string) => {
    const confirmation = prompt(`Type "DELETE" to permanently remove ${email}`);
    if (confirmation !== "DELETE") return;
    try {
      await axios.delete(`/api/admin/users/${encodeURIComponent(email)}`);
      fetchUsers();
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
    if (!confirm(`Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`)) return;
    try {
      await axios.delete(`/api/admin/leases/${encodeURIComponent(subdomain)}`);
      // Update selectedUser if open
      setSelectedUser(prev => prev ? {
        ...prev,
        active_tunnels: prev.active_tunnels?.filter(t => t.subdomain_prefix !== subdomain)
      } : null);
      fetchUsers();
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

  if (loading) return <div>Loading users...</div>;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h3>{t('user_management', 'User Management')}</h3>
        <button className="btn btn-primary" onClick={() => setShowInviteModal(true)}>+ {t('invite_user', 'Invite User')}</button>
      </div>
      
      {pageMessage && (
        <div style={{
          padding: '12px 16px',
          marginBottom: '20px',
          borderRadius: '8px',
          background: pageMessage.type === 'error' ? 'rgba(239, 68, 68, 0.15)' : 'rgba(16, 185, 129, 0.15)',
          color: pageMessage.type === 'error' ? '#f87171' : '#34d399',
          border: `1px solid ${pageMessage.type === 'error' ? 'rgba(239, 68, 68, 0.3)' : 'rgba(16, 185, 129, 0.3)'}`
        }}>
          {pageMessage.text}
        </div>
      )}

      <div className="card" style={{ padding: '0' }}>
        <div className="table-responsive">
          <table>
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>Status</th>
                <th>Auth Method</th>
                <th>Portal Active</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const isSelf = currentUser && u.email === currentUser.email;
                return (
                  <tr key={u.email} style={isSelf ? { opacity: 0.6 } : {}}>
                    <td>
                      <div style={{ fontWeight: 500 }}>
                        {u.first_name} {u.last_name}
                      </div>
                      <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>
                        <a 
                          href="#" 
                          onClick={(e) => { e.preventDefault(); setSelectedUser(u); }}
                          style={{ fontWeight: 500, textDecoration: 'none', color: 'inherit', cursor: 'pointer', transition: 'opacity 0.2s' }}
                          onMouseOver={(e) => (e.currentTarget.style.opacity = '0.8')}
                          onMouseOut={(e) => (e.currentTarget.style.opacity = '1')}
                        >
                          {u.email}
                        </a>
                        {(u.active_tunnels?.length || 0) > 0 && (
                          <span 
                            className="badge" 
                            onClick={() => setSelectedUser(u)}
                            style={{ cursor: 'pointer', background: 'rgba(99,102,241,0.15)', color: '#818cf8', border: '1px solid rgba(99,102,241,0.3)', padding: '2px 6px', fontSize: '11px', marginLeft: '8px' }}
                            title="Click to view tunnels"
                          >
                            🔌 {u.active_tunnels!.length} Tunnel{(u.active_tunnels!.length) > 1 ? 's' : ''}
                          </span>
                        )}
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
                      {u.portal_active ? (
                        <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: '#10b981', boxShadow: '0 0 8px #10b981' }} />
                      ) : (
                        <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'rgba(255,255,255,0.2)' }} />
                      )}
                    </td>
                    <td>
                      {!isSelf && (
                        <div style={{ display: 'flex', gap: '8px' }}>
                          {u.status === 'pending' || u.status === 'unverified' ? (
                            <>
                              <button className="btn btn-primary" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'approved')}>Approve</button>
                              <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'revoked')}>Reject</button>
                            </>
                          ) : (
                            <>
                              {u.status === 'approved' ? (
                                <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'revoked')}>Suspend</button>
                              ) : (
                                <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeStatus(u.email, 'approved')}>Unsuspend</button>
                              )}
                              
                              {(currentUser.role === 'owner' || u.role !== 'owner') && (
                                <>
                                  {u.role === 'admin' || u.role === 'owner' ? (
                                    <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeRole(u.email, 'user')}>Demote</button>
                                  ) : (
                                    <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeRole(u.email, 'admin')}>Promote</button>
                                  )}
                                  
                                  {u.email.toLowerCase() !== serverConfig?.owner_email?.toLowerCase() && (
                                    <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => deleteUser(u.email)}>Delete</button>
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
              })}
            </tbody>
          </table>
        </div>
      </div>
      {selectedUser && (
        <div style={{
          position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', 
          backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1000, 
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: '20px'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '800px', maxHeight: '90vh', overflowY: 'auto' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
              <h3 style={{ margin: 0 }}>User Details & Tunnels</h3>
              <button onClick={() => setSelectedUser(null)} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '20px', marginBottom: '24px' }}>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>Name</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.first_name} {selectedUser.last_name}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>Email</div>
                <div style={{ fontWeight: 500, fontFamily: 'monospace' }}>{selectedUser.email}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>Status & Role</div>
                <div>
                  <span className={`badge ${selectedUser.status === 'approved' ? 'success' : (selectedUser.status === 'revoked' ? 'danger' : 'warning')}`} style={{ marginRight: '8px' }}>
                    {selectedUser.status}
                  </span>
                  <span className={`badge ${selectedUser.role === 'admin' ? 'success' : ''}`}>
                    {selectedUser.role}
                  </span>
                </div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>Origin</div>
                <div style={{ fontWeight: 500, textTransform: 'capitalize' }}>{selectedUser.auth_method || 'Magic Link'}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>Joined Date</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.created_at ? new Date(selectedUser.created_at).toLocaleString() : 'N/A'}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '1px', marginBottom: '4px' }}>API Quota</div>
                <div style={{ fontWeight: 500 }}>{selectedUser.rate_limit ? `${selectedUser.rate_limit} RPS` : 'Unlimited'}</div>
              </div>
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: '24px' }}>
              <button className="btn btn-primary" onClick={() => setTargetedUserId(selectedUser.id)}>💬 Direct Message</button>
            </div>

            <h4 style={{ marginTop: '24px', marginBottom: '16px', borderBottom: '1px solid var(--border-color)', paddingBottom: '8px' }}>
              Connected Tunnels <span className="badge" style={{ marginLeft: '8px' }}>{(selectedUser.active_tunnels || []).length}</span>
            </h4>
            
            <div className="table-responsive" style={{ border: '1px solid var(--border-color)', borderRadius: '6px' }}>
              <table style={{ margin: 0 }}>
                <tbody>
                  {!(selectedUser.active_tunnels || []).length && (
                    <tr>
                      <td colSpan={4} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-muted)' }}>No active tunnels connected.</td>
                    </tr>
                  )}
                  {(selectedUser.active_tunnels || []).map((t) => {
                    const publicUrl = `https://${t.full_host}`;
                    return (
                      <tr key={t.subdomain_prefix} style={{ borderBottom: '1px solid rgba(255,255,255,0.03)' }}>
                        <td style={{ padding: '12px', verticalAlign: 'middle' }}>
                          <div style={{ fontWeight: 600, fontFamily: 'monospace', fontSize: '13px', color: 'var(--text)' }}>{t.subdomain_prefix}</div>
                          <div style={{ fontSize: '11px', color: 'var(--text-muted)', marginTop: '2px' }}>Local Port: {t.local_port}</div>
                        </td>
                        <td style={{ padding: '12px', verticalAlign: 'middle' }}>
                          <a href={publicUrl} target="_blank" rel="noreferrer" style={{ color: 'var(--primary)', textDecoration: 'none', fontSize: '13px', fontFamily: 'monospace', wordBreak: 'break-all' }}>{publicUrl}</a>
                          {t.node_id && t.node_id !== 'control' ? (
                            <span className="badge" style={{ background: 'rgba(139, 92, 246, 0.15)', color: '#c084fc', border: '1px solid rgba(139, 92, 246, 0.3)', fontSize: '10px', marginLeft: '6px' }}>
                              🌍 {t.node_id}
                            </span>
                          ) : (
                            <span className="badge" style={{ background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)', fontSize: '10px', marginLeft: '6px' }}>
                              🇬🇧 Control
                            </span>
                          )}
                          <div style={{ fontSize: '11px', color: 'var(--text-muted)', marginTop: '2px' }}>
                            IP: {t.client_ip} | Connected: {new Date(t.created_at).toLocaleString()}
                          </div>
                        </td>
                        <td style={{ padding: '12px', verticalAlign: 'middle', fontSize: '12px', color: 'var(--text-muted)' }}>
                          <div>📥 In: <strong style={{ color: 'var(--text)' }}>{formatBytes(t.bytes_in)}</strong></div>
                          <div style={{ marginTop: '2px' }}>📤 Out: <strong style={{ color: 'var(--text)' }}>{formatBytes(t.bytes_out)}</strong></div>
                        </td>
                        <td style={{ padding: '12px', verticalAlign: 'middle', textAlign: 'right' }}>
                          <button className="btn btn-danger" style={{ padding: '4px 10px', fontSize: '12px' }} onClick={() => kickTunnel(t.subdomain_prefix)}>Kick</button>
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
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: '20px'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '500px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
              <h3 style={{ margin: 0 }}>Send Direct Message</h3>
              <button onClick={() => setTargetedUserId('')} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            <p style={{ fontSize: '14px', color: 'var(--text-muted)', marginBottom: '16px' }}>
              Push a real-time banner alert to this specific active developer session.
            </p>
            <div className="form-group" style={{ marginBottom: '16px' }}>
              <textarea
                className="input-field"
                placeholder="Enter your message..."
                rows={3}
                value={targetedMessage}
                onChange={(e) => setTargetedMessage(e.target.value)}
              />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px' }}>
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
          display: 'flex', justifyContent: 'center', alignItems: 'center', padding: '20px'
        }}>
          <div className="card" style={{ width: '100%', maxWidth: '400px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
              <h3 style={{ margin: 0 }}>{t('invite_user', 'Invite User')}</h3>
              <button onClick={() => setShowInviteModal(false)} style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '20px' }}>✕</button>
            </div>
            {inviteError && <div className="alert alert-danger" style={{ marginBottom: '16px' }}>{inviteError}</div>}
            <form onSubmit={submitInvite}>
              <div className="form-group" style={{ marginBottom: '16px' }}>
                <label>{t('email_address', 'Email Address')}</label>
                <input type="email" required className="input-field" value={inviteForm.email} onChange={(e) => setInviteForm({...inviteForm, email: e.target.value})} placeholder="user@company.com" />
              </div>
              <div className="form-group" style={{ marginBottom: '16px' }}>
                <label>{t('first_name', 'First Name')}</label>
                <input type="text" required className="input-field" value={inviteForm.first_name} onChange={(e) => setInviteForm({...inviteForm, first_name: e.target.value})} placeholder={t('first_name_placeholder', 'John')} />
              </div>
              <div className="form-group" style={{ marginBottom: '16px' }}>
                <label>{t('last_name', 'Last Name')}</label>
                <input type="text" required className="input-field" value={inviteForm.last_name} onChange={(e) => setInviteForm({...inviteForm, last_name: e.target.value})} placeholder={t('last_name_placeholder', 'Doe')} />
              </div>
              <div className="form-group" style={{ marginBottom: '24px' }}>
                <label>{t('language_preference', 'Language Preference')}</label>
                <select className="input-field" value={inviteForm.language_preference} onChange={(e) => setInviteForm({...inviteForm, language_preference: e.target.value})}>
                  <option value="en">English (UK)</option>
                  <option value="en-us">English (US)</option>
                  <option value="de">Deutsch (DE)</option>
                  <option value="es">Español (ES)</option>
                  <option value="fr">Français (FR)</option>
                </select>
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px' }}>
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
