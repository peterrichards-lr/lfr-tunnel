import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';

function Dashboard() {
  return (
    <div className="container">
      <nav className="sidebar">
        {/* Placeholder for sidebar */}
        <h2>Liferay Tunnel</h2>
      </nav>
      <main className="content">
        <h1>Dashboard</h1>
        <p>Welcome to the React Portal.</p>
      </main>
    </div>
  );
}

function App() {
  // Use basename to match the Vite base URL configuration
  return (
    <BrowserRouter basename="/portal-v2">
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
