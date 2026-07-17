import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Layout from './components/Layout';
import AdminRoute from './components/AdminRoute';
import AdminSubdomains from './pages/AdminSubdomains';
import AdminUsers from './pages/AdminUsers';
import AdminSettings from './pages/AdminSettings';
import AdminAuditLog from './pages/AdminAuditLog';
import AdminBlacklist from './pages/AdminBlacklist';
import AdminExtensions from './pages/AdminExtensions';

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        
        {/* Protected Routes wrapped in Layout */}
        <Route element={<Layout />}>
          <Route path="/dashboard" element={<Dashboard />} />
          
          <Route element={<AdminRoute />}>
            <Route path="/admin/subdomains" element={<AdminSubdomains />} />
            <Route path="/admin/extensions" element={<AdminExtensions />} />
            <Route path="/admin/users" element={<AdminUsers />} />
            <Route path="/admin/audit" element={<AdminAuditLog />} />
            <Route path="/admin/blacklist" element={<AdminBlacklist />} />
            <Route path="/admin/settings" element={<AdminSettings />} />
          </Route>
        </Route>

        <Route path="/" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
