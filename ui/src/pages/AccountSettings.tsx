import React, { useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';

export default function AccountSettings() {
  const { user } = useOutletContext<{ user: any }>();
  const { theme, toggleTheme, useUTC, toggleUTC } = useSettings();
  const { language, setLanguage, t, availableLanguages } = useI18n();

  const [preferredName, setPreferredName] = useState(user?.preferred_name || '');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');
  
  const [mfaEnabled, setMfaEnabled] = useState(user?.totp_enabled || false);
  const [setupData, setSetupData] = useState<{ secret: string, qr: string } | null>(null);
  const [mfaCode, setMfaCode] = useState('');
  const [mfaError, setMfaError] = useState('');

  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
  const [deleteConfirmEmail, setDeleteConfirmEmail] = useState('');
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setMessage('');
    try {
      await axios.put('/api/me', {
        preferred_name: preferredName,
        language_preference: language,
        theme_preference: theme
      });
      setMessage(t('success_profile_saved', 'Profile updated successfully'));
    } catch {
      setMessage(t('error_profile_save', 'Failed to update profile'));
    } finally {
      setSaving(false);
    }
  };

  const startMfaSetup = async () => {
    try {
      const res = await axios.post('/api/mfa/setup');
      setSetupData(res.data);
      setMfaError('');
    } catch {
      setMfaError(t('error_mfa_setup', 'Failed to initialize MFA setup'));
    }
  };

  const enableMfa = async () => {
    try {
      await axios.post('/api/mfa/enable', { passcode: mfaCode });
      setMfaEnabled(true);
      setSetupData(null);
      setMfaError('');
    } catch {
      setMfaError(t('error_mfa_invalid', 'Invalid passcode, please try again.'));
    }
  };

  const handleDeleteAccount = async () => {
    setDeleteError('');
    if (deleteConfirmEmail !== user?.email) {
      setDeleteError(t('error_email_mismatch', 'Email does not match.'));
      return;
    }
    
    setIsDeleting(true);
    try {
      await axios.post('/api/me/delete-account');
      window.location.href = '/login'; // Force full redirect to clear state and re-auth
    } catch (err: any) {
      setDeleteError(err.response?.data?.error || t('error_delete_account', 'Failed to delete account.'));
      setIsDeleting(false);
    }
  };

  return (
    <div style={{ animation: 'fadeInUp 0.6s ease-out' }}>
      <div style={{ marginBottom: '32px' }}>
        <h1 style={{ fontSize: '32px', fontWeight: 800, letterSpacing: '-1px', marginBottom: '8px' }}>
          {t('account_settings', 'Account Settings')}
        </h1>
        <p style={{ color: 'var(--text-muted)', fontSize: '16px' }}>
          {t('account_desc', 'Update your personal information and security preferences.')}
        </p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: '24px' }}>
        
        {/* Profile Card */}
        <div className="card">
          <h3 style={{ margin: '0 0 16px 0', fontSize: '20px' }}>{t('profile_details', 'Profile Details')}</h3>
          <form onSubmit={handleSaveProfile}>
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', marginBottom: '8px', fontSize: '14px', color: 'var(--text-muted)' }}>
                {t('email_address', 'Email Address')}
              </label>
              <input type="email" className="input-field" value={user?.email || ''} disabled style={{ opacity: 0.7 }} />
            </div>
            
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', marginBottom: '8px', fontSize: '14px', color: 'var(--text-muted)' }}>
                {t('preferred_name', 'Preferred Name')}
              </label>
              <input 
                type="text" 
                className="input-field" 
                value={preferredName} 
                onChange={(e) => setPreferredName(e.target.value)}
                placeholder="e.g. John"
              />
            </div>

            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', marginBottom: '8px', fontSize: '14px', color: 'var(--text-muted)' }}>
                {t('language', 'Language')}
              </label>
              <select className="input-field" value={language} onChange={(e) => setLanguage(e.target.value)}>
                {availableLanguages.map(l => (
                  <option key={l.code} value={l.code}>{l.label}</option>
                ))}
              </select>
            </div>

            <div style={{ marginBottom: '24px' }}>
              <label style={{ display: 'block', marginBottom: '8px', fontSize: '14px', color: 'var(--text-muted)' }}>
                {t('theme', 'Theme Preference')}
              </label>
              <select className="input-field" value={theme} onChange={(e) => {
                if(e.target.value !== theme) toggleTheme();
              }}>
                <option value="light">{t('theme_light', 'Light')}</option>
                <option value="dark">{t('theme_dark', 'Dark')}</option>
              </select>
            </div>

            <div style={{ marginBottom: '24px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <label style={{ fontSize: '14px', color: 'var(--text-muted)' }}>
                {t('utc_time', 'UTC Time')}
              </label>
              <label className="switch">
                <input type="checkbox" checked={useUTC} onChange={toggleUTC} />
                <span className="slider round"></span>
              </label>
            </div>

            {message && <div className={message.includes('success') ? 'alert alert-success' : 'alert alert-error'}>{message}</div>}

            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t('saving', 'Saving...') : t('save_changes', 'Save Changes')}
            </button>
          </form>
        </div>

        {/* Security / MFA Card */}
        <div className="card">
          <h3 style={{ margin: '0 0 16px 0', fontSize: '20px' }}>{t('security', 'Security')}</h3>
          
          <div style={{ marginBottom: '24px', padding: '16px', background: 'rgba(0,0,0,0.1)', border: '1px solid var(--border)', borderRadius: '8px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <h4 style={{ margin: '0 0 4px 0' }}>{t('mfa_title', 'Multi-Factor Authentication')}</h4>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)' }}>
                  {mfaEnabled 
                    ? t('mfa_enabled_desc', 'Your account is secured with 2FA.') 
                    : t('mfa_disabled_desc', 'Add an extra layer of security to your account.')}
                </p>
              </div>
              <div>
                <span className={`badge ${mfaEnabled ? 'success' : 'warning'}`}>
                  {mfaEnabled ? t('enabled', 'Enabled') : t('disabled', 'Disabled')}
                </span>
              </div>
            </div>

            {!mfaEnabled && !setupData && (
              <button className="btn btn-primary" style={{ marginTop: '16px' }} onClick={startMfaSetup}>
                {t('setup_mfa', 'Setup MFA')}
              </button>
            )}

            {setupData && !mfaEnabled && (
              <div style={{ marginTop: '24px', animation: 'fadeInUp 0.3s ease-out' }}>
                <div style={{ background: '#fff', padding: '16px', borderRadius: '8px', display: 'inline-block', marginBottom: '16px' }}>
                  <img src={setupData.qr} alt="QR Code" width="150" height="150" />
                </div>
                <div className="copy-box" style={{ fontSize: '12px' }}>
                  {setupData.secret}
                </div>
                
                <label style={{ display: 'block', marginBottom: '8px', fontSize: '14px' }}>
                  {t('verify_passcode', 'Enter 6-digit code from authenticator app')}
                </label>
                <div style={{ display: 'flex', gap: '8px' }}>
                  <input 
                    type="text" 
                    className="input-field" 
                    placeholder="000000" 
                    value={mfaCode}
                    onChange={(e) => setMfaCode(e.target.value)}
                    style={{ marginBottom: 0 }}
                  />
                  <button className="btn btn-primary" onClick={enableMfa}>
                    {t('verify', 'Verify')}
                  </button>
                </div>
                {mfaError && <div className="alert alert-error" style={{ marginTop: '12px' }}>{mfaError}</div>}
              </div>
            )}
          </div>

          {user?.role !== 'owner' && (
            <div style={{ padding: '24px', borderRadius: '8px', marginTop: '24px', border: '1px solid rgba(239, 68, 68, 0.2)', background: 'var(--bg-card)' }}>
              <h3 style={{ margin: '0 0 8px 0', fontSize: '18px', color: '#f43f5e' }}>
                ⚠️ {t('danger_zone_title', 'Danger Zone (GDPR / Right to Be Forgotten)')}
              </h3>
              <p style={{ color: 'var(--text-muted)', marginBottom: '16px', fontSize: '14px' }}>
                {t('danger_zone_desc', 'Deleting your account will instantly and permanently revoke all of your personal access tokens, kick any active tunnel connections, and permanently purge your profile records from our systems. Any historical bandwidth metrics and logs will be permanently anonymised to protect your privacy.')}
              </p>
              <button 
                className="btn btn-outline" 
                style={{ width: 'auto', color: '#f43f5e', borderColor: '#f43f5e', background: 'transparent' }}
                onMouseOver={e => e.currentTarget.style.background = 'rgba(244,63,94,0.1)'}
                onMouseOut={e => e.currentTarget.style.background = 'transparent'}
                onClick={() => setIsDeleteModalOpen(true)}
              >
                {t('delete_account', 'Delete Account...')}
              </button>
            </div>
          )}

        </div>

      </div>

      {isDeleteModalOpen && (
        <div className="modal-overlay" style={{ display: 'flex', position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(0,0,0,0.6)', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div className="glass" style={{ maxWidth: '500px', width: '100%', padding: '32px', borderRadius: '16px', border: '1px solid var(--border)' }}>
            <h3 style={{ margin: '0 0 16px 0', fontSize: '20px', color: '#f43f5e' }}>{t('confirm_delete_title', 'Delete Account?')}</h3>
            <p style={{ color: 'var(--text-muted)', marginBottom: '24px', fontSize: '15px' }}>
              {t('confirm_delete_desc', 'This action is absolutely irreversible. Please type your email address to confirm.')}
              <br /><br />
              <strong>{user?.email}</strong>
            </p>
            
            <input 
              type="email" 
              className="input-field" 
              placeholder={user?.email} 
              value={deleteConfirmEmail}
              onChange={(e) => setDeleteConfirmEmail(e.target.value)}
              style={{ marginBottom: '16px', width: '100%' }}
            />
            
            {deleteError && <div className="alert alert-error" style={{ marginBottom: '16px' }}>{deleteError}</div>}

            <div style={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
              <button className="btn btn-secondary" style={{ width: 'auto' }} onClick={() => { setIsDeleteModalOpen(false); setDeleteConfirmEmail(''); setDeleteError(''); }}>
                {t('cancel', 'Cancel')}
              </button>
              <button 
                className="btn btn-primary" 
                style={{ width: 'auto', background: '#f43f5e', color: 'white', borderColor: '#f43f5e' }}
                onClick={handleDeleteAccount}
                disabled={isDeleting || deleteConfirmEmail !== user?.email}
              >
                {isDeleting ? t('deleting', 'Deleting...') : t('confirm_delete', 'Confirm Deletion')}
              </button>
            </div>
          </div>
        </div>
      )}

    </div>
  );
}
