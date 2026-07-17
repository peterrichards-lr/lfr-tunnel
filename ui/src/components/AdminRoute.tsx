import { useOutletContext, Navigate, Outlet } from 'react-router-dom';

export default function AdminRoute() {
  const { user } = useOutletContext<{ user: any }>();

  if (user.role !== 'admin' && user.role !== 'owner') {
    return <Navigate to="/dashboard" replace />;
  }

  return <Outlet context={{ user }} />;
}
