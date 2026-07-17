import { useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';

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
}

export default function AdminUsers() {
  const { user: currentUser } = useOutletContext<{ user: any }>();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchUsers = async () => {
    try {
      const res = await axios.get('/api/admin/users');
      setUsers(res.data || []);
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
      alert(`Failed to mark user as ${newStatus}`);
    }
  };

  const changeRole = async (email: string, newRole: string) => {
    if (!confirm(`Are you sure you want to change ${email} to ${newRole}?`)) return;
    try {
      await axios.patch(`/api/admin/users/${encodeURIComponent(email)}`, { role: newRole });
      fetchUsers();
    } catch {
      alert(`Failed to change role to ${newRole}`);
    }
  };

  const deleteUser = async (email: string) => {
    const confirmation = prompt(`Type "DELETE" to permanently remove ${email}`);
    if (confirmation !== "DELETE") return;
    try {
      await axios.delete(`/api/admin/users/${encodeURIComponent(email)}`);
      fetchUsers();
    } catch {
      alert('Failed to delete user');
    }
  };

  if (loading) return <div>Loading users...</div>;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h3>User Management</h3>
      </div>
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
                      <div style={{ fontWeight: 500 }}>{u.first_name} {u.last_name}</div>
                      <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>{u.email}</div>
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
                              
                              {u.role === 'admin' ? (
                                <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeRole(u.email, 'user')}>Demote</button>
                              ) : (
                                <button className="btn" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => changeRole(u.email, 'admin')}>Promote</button>
                              )}
                              
                              <button className="btn btn-danger" style={{ padding: '4px 8px', fontSize: '12px' }} onClick={() => deleteUser(u.email)}>Delete</button>
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
    </div>
  );
}
