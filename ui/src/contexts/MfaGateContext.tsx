import axios from 'axios';
import React, { useEffect, useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import { useI18n } from './I18nContext';
import { useUI } from './UIContext';

// #1055: force_mfa (server-side) blocks any authenticated request with
// 403 {"error": "MFA setup required", "mfa_required": true} until the user sets up
// TOTP. Before this, the ONLY place in the frontend that handled an mfa_required
// response was Login.tsx -- the login-time challenge for users who've *already*
// enabled TOTP. There was no handling for the case where an already-logged-in
// session hits that gate on its regular API calls: every panel on the page just
// failed independently with no redirect, so the whole app looked broken instead of
// prompting setup. This provider fixes that: a single global handler that catches
// mfa_required from anywhere and blocks the app behind a dedicated setup screen
// until it's resolved, then lets the user continue where they were.
//
// No React Context value is needed here -- nothing downstream needs to read
// whether the gate is active, so this is a plain wrapper component, not a
// createContext/useContext pair.

// Module-scope, not inside a component/effect -- registered exactly once when this
// module first loads, so React StrictMode's double-invoke of effects in dev can't
// double-register the interceptor. `notify` is wired up by the Provider below once
// it mounts; before that (or after it unmounts) this is a no-op.
let notify: (() => void) | null = null;

axios.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error?.response?.status;
    const data = error?.response?.data;
    const url: string = error?.config?.url || '';
    // Login.tsx's own login-time MFA challenge (POST /api/auth/mfa-verify, and the
    // "mfa_required" status Login.tsx checks for from /api/auth/verify) happens
    // before any session exists -- that flow already handles this response itself
    // and must not be intercepted here.
    const isLoginFlow = url.includes('/api/auth/verify') || url.includes('/api/auth/mfa-verify');
    if (status === 403 && data?.mfa_required && !isLoginFlow && notify) {
      notify();
    }
    return Promise.reject(error);
  }
);

interface MfaSetupData {
  otpauth_url: string;
  secret: string;
}

function MfaSetupGate() {
  const { t } = useI18n();
  const { showToast } = useUI();
  const [setupData, setSetupData] = useState<MfaSetupData | null>(null);
  const [loadError, setLoadError] = useState('');
  const [code, setCode] = useState('');
  const [verifyError, setVerifyError] = useState('');
  const [verifying, setVerifying] = useState(false);

  useEffect(() => {
    axios.get('/api/mfa/setup')
      .then((res) => setSetupData(res.data))
      .catch(() => setLoadError(t('error_mfa_setup', 'Failed to initialize MFA setup')));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const verify = async () => {
    if (!setupData) return;
    setVerifying(true);
    setVerifyError('');
    try {
      await axios.post('/api/mfa/enable', { secret: setupData.secret, code });
      showToast(t('mfa_setup_complete', 'MFA enabled -- welcome back!'), 'success');
      // Whatever page was mounted when force_mfa first blocked it already ran its
      // data-fetching effects and failed with 403 -- those never auto-retry. A full
      // reload is the simplest way to guarantee everything re-fetches fresh, rather
      // than tracking/retrying every possible failed request across the app (#1071).
      setTimeout(() => window.location.reload(), 600);
    } catch {
      setVerifyError(t('error_mfa_invalid', 'Invalid passcode, please try again.'));
    } finally {
      setVerifying(false);
    }
  };

  const logout = async () => {
    try {
      await axios.post('/api/auth/logout');
    } catch {
      // Best-effort -- redirect regardless.
    }
    window.location.href = '/portalv2/login';
  };

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'var(--modal-overlay)',
        backdropFilter: 'blur(8px)',
        zIndex: 10000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 'var(--spacing-xl)',
      }}
    >
      <div
        className="card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="mfa-gate-title"
        style={{ maxWidth: '440px', width: '100%', padding: 'var(--spacing-xl)', textAlign: 'center' }}
      >
        <h3 id="mfa-gate-title" className="m-0 mb-sm">
          {t('mfa_required_title', 'Set Up Multi-Factor Authentication to Continue')}
        </h3>
        <p className="text-muted text-sm mb-lg">
          {t('mfa_required_desc', 'Your administrator requires MFA on this account. Scan the code below with your authenticator app, then enter the 6-digit code to continue.')}
        </p>

        {loadError && <div className="alert-banner alert-banner--danger mb-md">{loadError}</div>}

        {setupData ? (
          <div className="animate-fade-in-fast">
            <div className="bg-white p-lg rounded-md inline-block mb-lg">
              <QRCodeSVG value={setupData.otpauth_url} size={150} />
            </div>
            <div className="copy-box text-xs mb-lg">{setupData.secret}</div>

            <label className="form-label">
              {t('verify_passcode', 'Enter 6-digit code from authenticator app')}
            </label>
            <div className="flex gap-sm">
              <input
                type="text"
                className="input-field mb-0"
                placeholder={t('mfa_otp_placeholder', '000000')}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') verify(); }}
                autoFocus
              />
              <button type="button" className="btn btn-primary" onClick={verify} disabled={verifying}>
                {verifying ? t('verifying', 'Verifying...') : t('verify', 'Verify')}
              </button>
            </div>
            {verifyError && <div className="alert-banner alert-banner--danger mt-md">{verifyError}</div>}
          </div>
        ) : !loadError ? (
          <div className="mb-lg">{t('loading', 'Loading...')}</div>
        ) : null}

        <button
          type="button"
          className="btn btn-secondary mt-lg"
          onClick={logout}
          style={{ fontSize: '13px' }}
        >
          {t('sign_out', 'Sign Out')}
        </button>
      </div>
    </div>
  );
}

export const MfaGateProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [required, setRequired] = useState(false);

  useEffect(() => {
    notify = () => setRequired(true);
    return () => { notify = null; };
  }, []);

  // No setRequired(false) path -- MfaSetupGate reloads the page itself once setup
  // succeeds (see #1071), which re-mounts everything including this provider.
  return (
    <>
      {children}
      {required && <MfaSetupGate />}
    </>
  );
};
