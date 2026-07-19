import React, { useState, useEffect } from 'react';
import axios from 'axios';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';

export default function Login() {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const { language, setLanguage, t, availableLanguages } = useI18n();

  const [email, setEmail] = useState('');
  const [statusMsg, setStatusMsg] = useState({ text: '', isError: false });
  const [isSending, setIsSending] = useState(false);
  const [countdown, setCountdown] = useState(0);

  const [mfaRequired, setMfaRequired] = useState(false);
  const [mfaToken, setMfaToken] = useState('');
  const [mfaCode, setMfaCode] = useState('');

  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [firstName, setFirstName] = useState('');
  const [lastName, setLastName] = useState('');

  // Handle magic link token in URL
  useEffect(() => {
    const magicToken = searchParams.get('token');
    const langParam = searchParams.get('lang') || 'en';

    if (magicToken) {
      // Clear token from URL
      searchParams.delete('token');
      setSearchParams(searchParams);

      axios.post('/api/auth/verify', { token: magicToken, lang: langParam })
        .then((res) => {
          if (res.data.status === 'mfa_required') {
            setMfaRequired(true);
            setMfaToken(res.data.temp_token);
          } else {
            navigate('/dashboard');
          }
        })
        .catch((err) => {
          setStatusMsg({ text: err.response?.data?.error || t('error_magic_link_invalid', 'Invalid or expired magic link.'), isError: true });
        });
    }
  }, [searchParams, setSearchParams, navigate, t]);

  useEffect(() => {
    let timer: number;
    if (countdown > 0) {
      timer = window.setInterval(() => {
        setCountdown((prev) => prev - 1);
      }, 1000);
    }
    return () => clearInterval(timer);
  }, [countdown]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (countdown > 0) return;

    setIsSending(true);
    setStatusMsg({ text: '', isError: false });

    if (mode === 'login') {
      try {
        await axios.post(`/api/auth/magic-link?lang=${language}`, { email });
        
        setStatusMsg({ text: t('magic_link_sent', 'Magic link sent! Check your email.'), isError: false });
        setCountdown(60);
      } catch (err: any) {
        const errorText = err.response?.data?.error || t('error_send_link', 'Failed to send link.');
        setStatusMsg({ text: errorText, isError: true });
      } finally {
        setIsSending(false);
      }
    } else {
      // Register
      try {
        await axios.post('/api/register-request', { 
          email,
          first_name: firstName,
          last_name: lastName
        });
        setStatusMsg({ text: t('register_request_sent', 'Registration request sent! Please check your email.'), isError: false });
        setMode('login'); // Switch back to login after successful request
      } catch (err: any) {
        const errorText = err.response?.data?.error || t('error_register_request', 'Failed to submit registration request.');
        setStatusMsg({ text: errorText, isError: true });
      } finally {
        setIsSending(false);
      }
    }
  };

  const handleMfaSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (mfaCode.length !== 6) {
      setStatusMsg({ text: t('error_mfa_length', 'Please enter a 6-digit code.'), isError: true });
      return;
    }

    setIsSending(true);
    setStatusMsg({ text: '', isError: false });

    try {
      await axios.post('/api/auth/mfa-verify', { temp_token: mfaToken, code: mfaCode });
      navigate('/dashboard');
    } catch (err: any) {
      setStatusMsg({ text: err.response?.data?.error || t('error_mfa_invalid', 'Invalid verification code.'), isError: true });
    } finally {
      setIsSending(false);
    }
  };

  return (
    <div id="login-screen" style={{ 
      display: 'flex', 
      flexDirection: 'column',
      minHeight: '100vh', 
      alignItems: 'center', 
      justifyContent: 'center', 
      background: 'var(--login-gradient)',
      position: 'relative',
      overflow: 'hidden'
    }}>
      {/* Premium ambient animated orb */}
      <div style={{
        position: 'absolute',
        width: '600px',
        height: '600px',
        background: 'radial-gradient(circle, var(--primary-glow) 0%, transparent 60%)',
        borderRadius: '50%',
        top: '50%',
        left: '50%',
        transform: 'translate(-50%, -50%)',
        animation: 'floatOrb 8s ease-in-out infinite',
        zIndex: 0,
        pointerEvents: 'none'
      }} />

      <div className="login-card" style={{ zIndex: 10 }}>
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: '24px' }}>
          <img src="/static/logo.svg" alt="Liferay Tunnel" width="56" height="56" style={{ filter: 'drop-shadow(0 4px 12px rgba(0,0,0,0.5))' }} />
        </div>

        {mfaRequired ? (
          <form id="mfa-form" onSubmit={handleMfaSubmit}>
            <h1 style={{ marginTop: 0, textAlign: 'center', fontSize: '28px', fontWeight: 700, letterSpacing: '-0.5px' }}>{t('mfa_title', 'Two-Factor Auth')}</h1>
            <p style={{ color: 'var(--text-muted)', marginBottom: '24px', fontSize: '15px', textAlign: 'center' }}>
              {t('mfa_login_desc', 'Enter the 6-digit verification code generated by your authenticator app.')}
            </p>
            <input 
              type="text" 
              className="input-field" 
              placeholder="123456" 
              pattern="[0-9]{6}" 
              maxLength={6} 
              inputMode="numeric"
              style={{ textAlign: 'center', fontSize: '24px', fontWeight: 'bold', letterSpacing: '8px', marginBottom: '24px', width: '100%' }}
              value={mfaCode}
              onChange={(e) => setMfaCode(e.target.value)}
            />
            <button 
              type="submit" 
              className="btn btn-primary" 
              disabled={isSending || mfaCode.length !== 6}
              style={{ width: '100%', padding: '14px', fontSize: '16px' }}
            >
              {isSending ? t('verifying', 'Verifying...') : t('verify_and_login', 'Verify & Log In')}
            </button>
            {statusMsg.text && (
              <div style={{ marginTop: '16px', textAlign: 'center', fontSize: '14px', fontWeight: 500, color: statusMsg.isError ? 'var(--danger)' : 'var(--success)' }}>
                {statusMsg.text}
              </div>
            )}
          </form>
        ) : (
          <>
            <h1 style={{ marginTop: 0, textAlign: 'center', fontSize: '28px', fontWeight: 700, letterSpacing: '-0.5px' }}>
              {mode === 'login' ? t('welcome_back', 'Welcome Back') : t('create_account', 'Create Account')}
            </h1>
            <p style={{ textAlign: 'center', color: 'var(--text-muted)', marginBottom: '24px', fontSize: '15px' }}>
              {mode === 'login' ? t('login_desc', 'Securely access your Liferay Tunnel') : t('register_desc', 'Request access to Liferay Tunnel')}
            </p>
            
            <form id="email-form" style={{ marginTop: '24px' }} onSubmit={handleSubmit}>
              
              {mode === 'register' && (
                <div style={{ display: 'flex', gap: '12px', marginBottom: '16px' }}>
                  <input 
                    type="text" 
                    name="first_name" 
                    className="input-field" 
                    placeholder={t('first_name', 'First Name')} 
                    required 
                    value={firstName}
                    onChange={(e) => setFirstName(e.target.value)}
                    style={{ width: '100%' }}
                  />
                  <input 
                    type="text" 
                    name="last_name" 
                    className="input-field" 
                    placeholder={t('last_name', 'Last Name')} 
                    required 
                    value={lastName}
                    onChange={(e) => setLastName(e.target.value)}
                    style={{ width: '100%' }}
                  />
                </div>
              )}

              <div style={{ marginBottom: '16px' }}>
                <input 
                  type="email" 
                  id="email-input" 
                  name="email" 
                  autoComplete="email" 
                  className="input-field" 
                  placeholder="name@liferay.com" 
                  required 
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  style={{ width: '100%' }}
                />
              </div>
              <button 
                type="submit" 
                className="btn btn-primary" 
                id="btn-magic-link"
                disabled={isSending || (mode === 'login' && countdown > 0)}
                style={{ width: '100%', padding: '14px', fontSize: '16px', display: 'flex', justifyContent: 'center', alignItems: 'center', gap: '8px' }}
              >
                {isSending ? (
                  <>
                    <span className="spinner" style={{ width: '18px', height: '18px', borderWidth: '2px' }} />
                    {t('sending', 'Sending...')}
                  </>
                ) : mode === 'login' ? (
                  countdown > 0 ? t('resend_in', `Resend in ${countdown}s`).replace('60s', `${countdown}s`) : t('send_magic_link', 'Send Magic Link')
                ) : t('submit_request', 'Submit Request')}
              </button>
              
              {statusMsg.text && (
                <div id="email-msg" style={{ marginTop: '16px', textAlign: 'center', fontSize: '14px', fontWeight: 500, color: statusMsg.isError ? 'var(--danger)' : 'var(--success)' }}>
                  {statusMsg.text}
                </div>
              )}

              <div style={{ marginTop: '24px', textAlign: 'center' }}>
                <button 
                  type="button" 
                  onClick={() => { setMode(mode === 'login' ? 'register' : 'login'); setStatusMsg({ text: '', isError: false }); }}
                  style={{ background: 'none', border: 'none', color: 'var(--text-muted)', fontSize: '14px', cursor: 'pointer', textDecoration: 'underline' }}
                >
                  {mode === 'login' ? t('no_account', "Don't have an account? Create one") : t('already_have_account', "Already have an account? Log in")}
                </button>
              </div>
            </form>
          </>
        )}
      </div>

      {/* Footer Links & Language */}
      <div style={{ marginTop: '32px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '16px', zIndex: 10 }}>
        <select 
          className="input-field" 
          style={{ padding: '6px 12px', background: 'rgba(255,255,255,0.1)', color: 'rgba(255,255,255,0.9)', border: '1px solid rgba(255,255,255,0.2)', cursor: 'pointer', width: 'auto' }}
          value={language}
          onChange={(e) => setLanguage(e.target.value)}
        >
          {availableLanguages.map(l => (
            <option key={l.code} value={l.code} style={{ color: '#000' }}>{l.label}</option>
          ))}
        </select>
        <div style={{ display: 'flex', gap: '24px', fontSize: '13px' }}>
          <a href={`/privacy?lang=${language}`} target="_blank" style={{ color: 'rgba(255, 255, 255, 0.6)', textDecoration: 'none', transition: 'color 0.2s' }} onMouseOver={e => e.currentTarget.style.color='rgba(255,255,255,0.9)'} onMouseOut={e => e.currentTarget.style.color='rgba(255,255,255,0.6)'}>{t('privacy_title', 'Privacy Policy')}</a>
          <a href={`/cookies?lang=${language}`} target="_blank" style={{ color: 'rgba(255, 255, 255, 0.6)', textDecoration: 'none', transition: 'color 0.2s' }} onMouseOver={e => e.currentTarget.style.color='rgba(255,255,255,0.9)'} onMouseOut={e => e.currentTarget.style.color='rgba(255,255,255,0.6)'}>{t('cookie_title', 'Cookie Disclosure')}</a>
        </div>
      </div>
    </div>
  );
}
