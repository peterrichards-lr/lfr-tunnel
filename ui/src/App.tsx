import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Layout from './components/Layout';
import AdminRoute from './components/AdminRoute';
import AdminSubdomains from './pages/AdminSubdomains';
import AdminUsers from './pages/AdminUsers';
import AdminSettings from './pages/AdminSettings';
import AdminMaintenance from './pages/AdminMaintenance';
import AdminTokens from './pages/AdminTokens';
import AdminAnalytics from './pages/AdminAnalytics';
import AdminAuditLog from './pages/AdminAuditLog';
import AdminBlacklist from './pages/AdminBlacklist';
import AdminExtensions from './pages/AdminExtensions';
import AdminEdgeHealth from './pages/AdminEdgeHealth';
import AdminMagicLinks from './pages/AdminMagicLinks';
import AdminTelemetry from './pages/AdminTelemetry';
import AdminVanityDomainStatus from './pages/AdminVanityDomainStatus';
import AccountSettings from './pages/AccountSettings';
import { SettingsProvider } from './contexts/SettingsContext';
import { I18nProvider } from './contexts/I18nContext';
import { UIProvider } from './contexts/UIContext';
import { MfaGateProvider } from './contexts/MfaGateContext';

function App() {
  return (
    <SettingsProvider>
      <I18nProvider>
        <UIProvider>
          <MfaGateProvider>
            <BrowserRouter basename="/portalv2">
              <Routes>
                <Route path="/login" element={<Login />} />

                {/* Protected Routes wrapped in Layout */}
                <Route element={<Layout />}>
                  <Route path="/dashboard" element={<Dashboard />} />
                  <Route path="/account" element={<AccountSettings />} />
                  {/* Analytics is not admin-only (#1512). The same component serves both:
                      it renders the personal section for everyone and the system sections
                      only when the API returns `global`, which it does only for an admin.
                      /admin/analytics stays mounted below so existing links and bookmarks
                      keep working. */}
                  <Route path="/analytics" element={<AdminAnalytics />} />

                  <Route element={<AdminRoute />}>
                    <Route
                      path="/admin/subdomains"
                      element={<AdminSubdomains />}
                    />
                    <Route
                      path="/admin/vanity-domain-status"
                      element={<AdminVanityDomainStatus />}
                    />
                    <Route
                      path="/admin/extensions"
                      element={<AdminExtensions />}
                    />
                    <Route path="/admin/users" element={<AdminUsers />} />
                    <Route
                      path="/admin/analytics"
                      element={<AdminAnalytics />}
                    />
                    <Route path="/admin/audit" element={<AdminAuditLog />} />
                    <Route
                      path="/admin/blacklist"
                      element={<AdminBlacklist />}
                    />
                    <Route
                      path="/admin/edge-health"
                      element={<AdminEdgeHealth />}
                    />
                    <Route
                      path="/admin/magic-links"
                      element={<AdminMagicLinks />}
                    />
                    <Route
                      path="/admin/telemetry"
                      element={<AdminTelemetry />}
                    />
                    <Route path="/admin/tokens" element={<AdminTokens />} />
                    <Route path="/admin/settings" element={<AdminSettings />} />
                    <Route
                      path="/admin/maintenance"
                      element={<AdminMaintenance />}
                    />
                  </Route>
                </Route>

                <Route
                  path="/"
                  element={<Navigate to="/dashboard" replace />}
                />
              </Routes>
            </BrowserRouter>
          </MfaGateProvider>
        </UIProvider>
      </I18nProvider>
    </SettingsProvider>
  );
}

export default App;
