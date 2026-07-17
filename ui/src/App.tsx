import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Layout from './components/Layout';
import AdminRoute from './components/AdminRoute';
import AdminSubdomains from './pages/AdminSubdomains';
import AdminUsers from './pages/AdminUsers';

function App() {
  return (
    <BrowserRouter basename="/portal-v2">
      <Routes>
        <Route path="/login" element={<Login />} />
        
        {/* Protected Routes wrapped in Layout */}
        <Route element={<Layout />}>
          <Route path="/dashboard" element={<Dashboard />} />
          
          <Route element={<AdminRoute />}>
            <Route path="/admin/subdomains" element={<AdminSubdomains />} />
            <Route path="/admin/users" element={<AdminUsers />} />
          </Route>
        </Route>

        <Route path="/" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
