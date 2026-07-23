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
    <div id="login-screen" className="flex flex-col min-h-screen items-center justify-center relative overflow-hidden" style={{ background: 'var(--login-gradient)' }}>
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

      {/* V1 Promo Banner */}
      <div className="z-10 bg-primary text-white py-md px-xl rounded-md mb-xl max-w-sm w-full text-center box-border shadow-md">
        <p className="m-0 text-sm fw-medium">
          {t('banner_classic_look', 'Prefer the classic look?')} <a href="/" className="text-white underline fw-bold ml-xs">{t('btn_return_v1', 'Return to V1 →')}</a>
        </p>
      </div>

      <div className="login-card z-10">
        <div className="flex justify-center mb-xl">
          <img src="/static/logo.svg" alt="Liferay Tunnel" width="56" height="56" className="drop-shadow-md" />
        </div>

        {mfaRequired ? (
          <form id="mfa-form" onSubmit={handleMfaSubmit}>
            <h1 className="mt-0 text-center text-xl fw-bold tracking-tight">{t('mfa_verify_title', 'Multi-Factor Verification')}</h1>
            <p className="text-muted mb-xl text-base text-center">
              {t('mfa_verify_desc', 'Please enter the 6-digit code from your authenticator app:')}
            </p>
            <input 
              type="text" 
              className="input-field text-center text-2xl font-bold tracking-widest mb-xl w-full" 
              placeholder={t('mfa_otp_placeholder', '123456')} 
              pattern="[0-9]{6}" 
              maxLength={6} 
              inputMode="numeric"
              value={mfaCode}
              onChange={(e) => setMfaCode(e.target.value)}
            />
            <button 
              type="submit" 
              className="btn btn-primary w-full p-md text-base" 
              disabled={isSending || mfaCode.length !== 6}
            >
              {isSending ? t('verifying', 'Verifying...') : t('btn_verify_login', 'Verify & Login')}
            </button>
            {statusMsg.text && (
              <div className={`mt-lg text-center text-sm fw-medium ${statusMsg.isError ? 'text-danger' : 'text-success'}`}>
                {statusMsg.text}
              </div>
            )}
          </form>
        ) : (
          <>
            <h1 className="mt-0 text-center text-xl fw-bold tracking-tight">
              {mode === 'login' ? t('portal_welcome', 'Welcome Back') : t('btn_register_account', 'Register Account')}
            </h1>
            <p className="text-center text-muted mb-xl text-base">
              {mode === 'login' ? t('portal_login_desc', 'Enter your email to request a secure passwordless login link.') : t('portal_register_desc', 'Don\'t have an account yet? Register here:')}
            </p>
            
            <form id="email-form" className="mt-xl" onSubmit={handleSubmit}>
              
              {mode === 'register' && (
                <div className="flex gap-md mb-lg">
                  <input 
                    type="text" 
                    name="first_name" 
                    className="input-field w-full" 
                    placeholder={t('label_first_name', 'First Name')} 
                    required 
                    value={firstName}
                    onChange={(e) => setFirstName(e.target.value)}
                  />
                  <input 
                    type="text" 
                    name="last_name" 
                    className="input-field w-full" 
                    placeholder={t('label_last_name', 'Last Name')} 
                    required 
                    value={lastName}
                    onChange={(e) => setLastName(e.target.value)}
                  />
                </div>
              )}

              <div className="mb-lg">
                <input 
                  type="email" 
                  id="email-input" 
                  name="email" 
                  autoComplete="email" 
                  className="input-field w-full" 
                  placeholder={t('label_email', 'Email Address')} 
                  required 
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
              <button 
                type="submit" 
                className="btn btn-primary w-full p-md text-base flex justify-center items-center gap-sm" 
                id="btn-magic-link"
                disabled={isSending || (mode === 'login' && countdown > 0)}
              >
                {isSending ? (
                  <>
                    <span className="spinner w-4 h-4 border-2" />
                    {t('sending', 'Sending...')}
                  </>
                ) : mode === 'login' ? (
                  countdown > 0 ? t('resend_in', `Resend in ${countdown}s`).replace('60s', `${countdown}s`) : t('btn_send_magic_link', 'Send Magic Link')
                ) : t('btn_register_account', 'Register Account')}
              </button>
              
              {statusMsg.text && (
                <div id="email-msg" className={`mt-lg text-center text-sm fw-medium ${statusMsg.isError ? 'text-danger' : 'text-success'}`}>
                  {statusMsg.text}
                </div>
              )}

              <div className="mt-xl text-center">
                <button 
                  type="button" 
                  onClick={() => { setMode(mode === 'login' ? 'register' : 'login'); setStatusMsg({ text: '', isError: false }); }}
                  className="btn-text text-muted text-sm underline cursor-pointer"
                >
                  {mode === 'login' ? t('portal_register_desc', "Don't have an account yet? Register here:") : t('btn_login_with_email', "Login with Email")}
                </button>
              </div>
            </form>
          </>
        )}
      </div>

      {/* Footer Links & Language */}
      <div className="mt-2xl flex flex-col items-center gap-lg z-10">
        <select 
          className="input-field py-xs px-md bg-white/10 text-white/90 border border-white/20 cursor-pointer w-auto"
          value={language}
          onChange={(e) => setLanguage(e.target.value)}
        >
          {availableLanguages.map(l => (
            <option key={l.code} value={l.code} className="text-black">{l.label}</option>
          ))}
        </select>
        <div className="flex gap-xl text-xs">
          <a href={`/privacy?lang=${language}`} target="_blank" className="login-footer-link">{t('privacy_title', 'Privacy Policy')}</a>
          <a href={`/cookies?lang=${language}`} target="_blank" className="login-footer-link">{t('cookie_title', 'Cookie Disclosure')}</a>
        </div>
      </div>
    </div>
  );
}
