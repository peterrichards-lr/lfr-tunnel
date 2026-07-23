import React, { useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';

export default function AccountSettings() {
  const { user } = useOutletContext<{ user: any }>();
  const { themePreference, setThemePreference, useUTC, toggleUTC } = useSettings();
  const { language, setLanguage, t, availableLanguages } = useI18n();

  const [firstName, setFirstName] = useState(user?.first_name || '');
  const [lastName, setLastName] = useState(user?.last_name || '');
  const [preferredName, setPreferredName] = useState(user?.preferred_name || '');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');
  
  const [emailNotifications, setEmailNotifications] = useState(user?.notification_prefs === 'enabled' || !user?.notification_prefs);
  const [mfaEnabled, setMfaEnabled] = useState(user?.totp_enabled || false);
  const [setupData, setSetupData] = useState<{ secret: string, qr: string } | null>(null);
  const [mfaCode, setMfaCode] = useState('');
  const [mfaError, setMfaError] = useState('');

  const [domains, setDomains] = useState<string[]>([]);
  const [allocationRule, setAllocationRule] = useState('contextual');
  const [subdomainStyle, setSubdomainStyle] = useState(user?.subdomain_style || 'liferay');
  const [preferredDomain, setPreferredDomain] = useState(user?.preferred_domain || '');
  const [disablingMfa, setDisablingMfa] = useState(false);
  const [disableCode, setDisableCode] = useState('');
  const [disableError, setDisableError] = useState('');

  React.useEffect(() => {
    axios.get('/api/domains')
      .then(res => setDomains(res.data || []))
      .catch(err => console.error('Failed to fetch domains', err));
    axios.get('/api/version')
      .then(res => setAllocationRule(res.data?.domain_allocation_rule || 'contextual'))
      .catch(err => console.error('Failed to fetch version/rule', err));
  }, []);

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
        first_name: firstName,
        last_name: lastName,
        preferred_name: preferredName,
        language_preference: language,
        theme_preference: themePreference,
        notification_prefs: emailNotifications ? 'enabled' : 'disabled',
        preferred_domain: preferredDomain,
        subdomain_style: subdomainStyle
      });
      if (user) {
        user.first_name = firstName;
        user.last_name = lastName;
        user.preferred_name = preferredName;
        user.language_preference = language;
        user.theme_preference = themePreference;
        user.notification_prefs = emailNotifications ? 'enabled' : 'disabled';
        user.preferred_domain = preferredDomain;
        user.subdomain_style = subdomainStyle;
      }
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
      <div className="mb-2xl">
        <h1 className="text-2xl fw-extrabold tracking-tight mb-xs">
          {t('account_title', 'Account Settings')}
        </h1>
        <p className="text-muted text-base">
          {t('account_desc', 'Update your personal information and security preferences.')}
        </p>
      </div>

      <div className="col-2">
        
        {/* Profile Card */}
        <div className="card mb-xl">
          <h3 className="section-title mb-lg">{t('profile_details', 'Personal Details')}</h3>
          <form onSubmit={handleSaveProfile}>
            <div className="form-group mb-lg">
              <label className="form-label">
                {t('label_email', 'Email Address')}
              </label>
              <input type="email" className="input-field opacity-70" value={user?.email || ''} disabled />
            </div>

            <div className="grid grid-cols-2 gap-md mb-lg">
              <div className="form-group m-0">
                <label className="form-label">
                  {t('label_first_name', 'First Name')}
                </label>
                <input 
                  type="text" 
                  className="input-field" 
                  value={firstName} 
                  onChange={(e) => setFirstName(e.target.value)}
                  placeholder={t('label_first_name', 'First Name')}
                />
              </div>
              <div className="form-group m-0">
                <label className="form-label">
                  {t('label_last_name', 'Last Name')}
                </label>
                <input 
                  type="text" 
                  className="input-field" 
                  value={lastName} 
                  onChange={(e) => setLastName(e.target.value)}
                  placeholder={t('label_last_name', 'Last Name')}
                />
              </div>
            </div>
            
            <div className="form-group mb-xl">
              <label className="form-label">
                {t('label_preferred_name', 'Preferred Name')}
              </label>
              <input 
                type="text" 
                className="input-field" 
                value={preferredName} 
                onChange={(e) => setPreferredName(e.target.value)}
                placeholder={t('first_name_eg_placeholder', 'e.g. John')}
              />
            </div>

            {message && <div className={message.includes('success') ? 'alert-banner alert-banner--success mb-lg' : 'alert-banner alert-banner--danger mb-lg'}>{message}</div>}

            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t('saving', 'Saving...') : t('btn_save_changes', 'Save Profile')}
            </button>
          </form>
        </div>

        {/* Preferences & Defaults Card */}
        <div className="card mb-xl">
          <h3 className="section-title mb-lg">{t('preferences_defaults', 'Preferences & Defaults')}</h3>
          <form onSubmit={handleSaveProfile}>
            <div className="form-group mb-lg">
              <label className="form-label">
                {t('label_language', 'Language')}
              </label>
              <select className="input-field" value={language} onChange={(e) => setLanguage(e.target.value)}>
                {availableLanguages.map(l => (
                  <option key={l.code} value={l.code}>{l.label}</option>
                ))}
              </select>
            </div>

            <div className="form-group mb-xl">
              <label className="form-label">
                {t('label_theme', 'Theme Preference')}
              </label>
              <select className="input-field" value={themePreference} onChange={(e) => {
                setThemePreference(e.target.value as any);
              }}>
                <option value="light">{t('theme_light', 'Light')}</option>
                <option value="dark">{t('theme_dark', 'Dark')}</option>
                <option value="liferay">{t('theme_liferay', 'Liferay Waffle 🧇')}</option>
                <option value="system">{t('theme_system', 'System Default')}</option>
                <option value="time">{t('theme_time', 'Time of Day')}</option>
              </select>
            </div>

            <div className="flex items-center justify-between mb-lg">
              <label className="form-label m-0">
                {t('label_notifications', 'Email Notifications')}
              </label>
              <label className="switch">
                <input type="checkbox" checked={emailNotifications} onChange={(e) => setEmailNotifications(e.target.checked)} />
                <span className="slider round"></span>
              </label>
            </div>

            <div className="flex items-center justify-between mb-xl">
              <label className="form-label m-0">
                {t('utc_time', 'UTC Time')}
              </label>
              <label className="switch">
                <input type="checkbox" checked={useUTC} onChange={toggleUTC} />
                <span className="slider round"></span>
              </label>
            </div>

            <div className="form-group mb-lg">
              <label className="form-label">
                {t('default_subdomain_style', 'Default Subdomain Style')}
              </label>
              <select className="input-field" value={subdomainStyle} onChange={(e) => setSubdomainStyle(e.target.value)}>
                <option value="liferay">{t('style_liferay', 'Liferay SE Style')} — e.g. micro-tomcat-387</option>
                <option value="words">{t('style_words', 'Words Style')} — e.g. falcon-orange-apple</option>
                <option value="heroku">{t('style_heroku', 'Heroku Style')} — e.g. silent-owl-4319</option>
                <option value="ngrok">{t('style_ngrok', 'Ngrok Style')} — e.g. 8f4b-tunnel</option>
                <option value="random">{t('style_random', 'Alphanumeric')} — e.g. 4wq0kgyl</option>
              </select>
              <p className="form-hint">
                {t('subdomain_style_hint', 'The style used to generate a default subdomain when you connect your CLI without specifying one.')}
              </p>
            </div>

            <div className="form-group mb-lg">
              <div className="flex items-center justify-between gap-xs mb-xs">
                <label className="form-label m-0">
                  {t('preferred_domain', 'Preferred Domain')}
                </label>
                <span className={`badge ${allocationRule === 'user-preference' ? 'badge-success' : 'badge-warning'} text-2xs`}>
                  {allocationRule === 'user-preference' ? t('active', 'Active') : t('admin_controlled', 'Admin Controlled')}
                </span>
              </div>
              <select
                className="input-field"
                value={preferredDomain}
                onChange={(e) => setPreferredDomain(e.target.value)}
                disabled={allocationRule !== 'user-preference'}
              >
                <option value="">{t('none_auto', 'None (Auto)')}</option>
                {domains.map(d => (
                  <option key={d} value={d}>{d}</option>
                ))}
              </select>
              <p className="form-hint">
                {allocationRule === 'user-preference'
                  ? t('pref_domain_enabled', 'Your preferred domain will be used when you connect your CLI.')
                  : t('pref_domain_disabled_generic', 'The administrator has configured automatic domain allocation. Your preference cannot be applied.')}
              </p>
            </div>

            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t('saving', 'Saving...') : t('btn_save_preferences', 'Save Preferences')}
            </button>
          </form>
        </div>

        {/* Security / MFA Card */}
        <div className="card">
          <h3 className="section-title mb-lg">{t('security', 'Security')}</h3>
          
          <div className="card p-lg mb-xl border" style={{ background: 'rgba(0,0,0,0.1)' }}>
            <div className="flex justify-between items-center">
              <div>
                <h4 className="m-0 mb-2xs">{t('mfa_title', 'Multi-Factor Authentication')}</h4>
                <p className="m-0 text-xs text-muted">
                  {mfaEnabled 
                    ? t('mfa_enabled_desc', 'Your account is secured with 2FA.') 
                    : t('mfa_disabled_desc', 'Add an extra layer of security to your account.')}
                </p>
              </div>
              <div>
                <span className={`badge ${mfaEnabled ? 'badge-success' : 'badge-warning'}`}>
                  {mfaEnabled ? t('enabled', 'Enabled') : t('disabled', 'Disabled')}
                </span>
              </div>
            </div>

            {mfaEnabled && (
              <div className="mt-lg">
                {!disablingMfa ? (
                  <button type="button" className="btn btn-outline-danger" onClick={() => setDisablingMfa(true)}>
                    {t('btn_disable_mfa', 'Disable MFA')}
                  </button>
                ) : (
                  <div className="mt-md" style={{ animation: 'fadeInUp 0.3s ease-out' }}>
                    <p className="text-xs text-muted mb-xs">
                      {t('mfa_deactivate_desc', 'To deactivate MFA, please enter your 6-digit authenticator code below:')}
                    </p>
                    <div className="flex gap-sm items-center">
                      <input 
                        type="text" 
                        className="input-field text-center font-bold mb-0" 
                        placeholder="123456" 
                        maxLength={6} 
                        value={disableCode}
                        onChange={(e) => setDisableCode(e.target.value)}
                        style={{ width: '120px' }}
                      />
                      <button type="button" className="btn btn-primary" onClick={async () => {
                        setDisableError('');
                        try {
                          await axios.post('/api/mfa/disable', { code: disableCode });
                          setMfaEnabled(false);
                          setDisablingMfa(false);
                          setDisableCode('');
                          if (user) user.totp_enabled = false;
                        } catch {
                          setDisableError(t('error_mfa_invalid', 'Invalid passcode, please try again.'));
                        }
                      }}>
                        {t('confirm', 'Confirm')}
                      </button>
                      <button type="button" className="btn btn-secondary" onClick={() => { setDisablingMfa(false); setDisableCode(''); setDisableError(''); }}>
                        {t('cancel', 'Cancel')}
                      </button>
                    </div>
                    {disableError && <div className="alert-banner alert-banner--danger mt-md">{disableError}</div>}
                  </div>
                )}
              </div>
            )}

            {!mfaEnabled && !setupData && (
              <button type="button" className="btn btn-primary mt-lg" onClick={startMfaSetup}>
                {t('setup_mfa', 'Setup MFA')}
              </button>
            )}

            {setupData && !mfaEnabled && (
              <div className="mt-xl" style={{ animation: 'fadeInUp 0.3s ease-out' }}>
                <div className="bg-white p-lg rounded-md inline-block mb-lg">
                  <img src={setupData.qr} alt="QR Code" width="150" height="150" />
                </div>
                <div className="copy-box text-xs mb-lg">
                  {setupData.secret}
                </div>
                
                <label className="form-label">
                  {t('verify_passcode', 'Enter 6-digit code from authenticator app')}
                </label>
                <div className="flex gap-sm">
                  <input 
                    type="text" 
                    className="input-field mb-0" 
                    placeholder={t('mfa_otp_placeholder', '000000')} 
                    value={mfaCode}
                    onChange={(e) => setMfaCode(e.target.value)}
                  />
                  <button type="button" className="btn btn-primary" onClick={enableMfa}>
                    {t('verify', 'Verify')}
                  </button>
                </div>
                {mfaError && <div className="alert-banner alert-banner--danger mt-md">{mfaError}</div>}
              </div>
            )}
          </div>

          {user?.role !== 'owner' && (
            <div className="card p-xl mt-xl border" style={{ borderColor: 'rgba(239, 68, 68, 0.2)' }}>
              <h3 className="m-0 mb-xs text-md text-danger">
                ⚠️ {t('danger_zone_title', 'Danger Zone (GDPR / Right to Be Forgotten)')}
              </h3>
              <p className="text-muted text-sm mb-lg">
                {t('danger_zone_desc', 'Deleting your account will instantly and permanently revoke all of your personal access tokens, kick any active tunnel connections, and permanently purge your profile records from our systems. Any historical bandwidth metrics and logs will be permanently anonymised to protect your privacy.')}
              </p>
              <button 
                type="button"
                className="btn btn-outline-danger w-auto" 
                onClick={() => setIsDeleteModalOpen(true)}
              >
                {t('delete_account', 'Delete Account...')}
              </button>
            </div>
          )}

        </div>

      </div>

      {isDeleteModalOpen && (
        <div className="modal-backdrop">
          <div 
            className="modal-card modal-card--sm"
            role="dialog"
            aria-modal="true"
            aria-labelledby="delete-account-title"
          >
            <div className="modal-header">
              <h3 id="delete-account-title" className="modal-title text-danger">{t('confirm_delete_title', 'Delete Account?')}</h3>
              <button type="button" onClick={() => { setIsDeleteModalOpen(false); setDeleteConfirmEmail(''); setDeleteError(''); }} className="modal-close" aria-label={t('close', 'Close')}>✕</button>
            </div>
            <div className="modal-body">
              <p className="text-muted text-sm mb-lg">
                {t('confirm_delete_desc', 'This action is absolutely irreversible. Please type your email address to confirm.')}
                <br /><br />
                <strong className="text-main">{user?.email}</strong>
              </p>
              
              <input 
                type="email" 
                className="input-field w-full mb-lg" 
                placeholder={user?.email} 
                value={deleteConfirmEmail}
                onChange={(e) => setDeleteConfirmEmail(e.target.value)}
              />
              
              {deleteError && <div className="alert-banner alert-banner--danger mb-lg">{deleteError}</div>}
            </div>

            <div className="modal-footer">
              <button type="button" className="btn btn-secondary w-auto" onClick={() => { setIsDeleteModalOpen(false); setDeleteConfirmEmail(''); setDeleteError(''); }}>
                {t('cancel', 'Cancel')}
              </button>
              <button 
                type="button"
                className="btn btn-danger w-auto" 
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
