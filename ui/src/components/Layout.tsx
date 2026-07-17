import { useEffect, useState } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import axios from 'axios';
import Sidebar from './Sidebar';

export default function Layout() {
  const [user, setUser] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  useEffect(() => {
    // Fetch current user from /api/me
    axios.get('/api/me')
      .then(res => {
        setUser(res.data);
      })
      .catch(() => {
        // Not authenticated
        navigate('/login');
      })
      .finally(() => {
        setLoading(false);
      });
  }, [navigate]);

  if (loading) {
    return <div id="loader" style={{ display: 'flex' }}><div className="spinner"></div></div>;
  }

  if (!user) return null;

  return (
    <div id="dashboard-screen" style={{ display: 'flex' }}>
      <Sidebar user={user} />
      <div className="content">
        <header className="content-header">
          <div>
            <h2 style={{ margin: 0 }}>Dashboard</h2>
            <p style={{ margin: 0, color: 'var(--text-muted)' }}>Welcome back, {user.first_name}</p>
          </div>
        </header>
        <div style={{ padding: '24px' }}>
          <Outlet context={{ user }} />
        </div>
      </div>
    </div>
  );
}
