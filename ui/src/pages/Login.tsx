import React, { useState, useEffect } from 'react';
import axios from 'axios';

export default function Login() {
  const [email, setEmail] = useState('');
  const [statusMsg, setStatusMsg] = useState({ text: '', isError: false });
  const [isSending, setIsSending] = useState(false);
  const [countdown, setCountdown] = useState(0);

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

    try {
      // Use standard language code or fetch from context. Defaulting to 'en'
      const lang = 'en'; 
      await axios.post(`/api/auth/magic-link?lang=${lang}`, { email });
      
      setStatusMsg({ text: 'Magic link sent! Check your email.', isError: false });
      setCountdown(60);
    } catch (err: any) {
      const errorText = err.response?.data?.error || 'Failed to send link.';
      setStatusMsg({ text: errorText, isError: true });
    } finally {
      setIsSending(false);
    }
  };

  return (
    <div id="login-screen">
      <div className="glass login-card" style={{ position: 'relative' }}>
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: '12px', marginTop: '8px' }}>
          {/* Ensure the static logo can be resolved from the Go backend */}
          <img src="/static/logo.svg" alt="Liferay Tunnel" width="48" height="48" />
        </div>

        <h1 style={{ marginTop: 0 }}>Welcome Back</h1>
        <p>Log in to access your dashboard</p>

        <div className="divider" id="sso-divider">or</div>

        <button className="btn" id="btn-show-email">Login with Email</button>
        <button className="btn btn-outline" id="btn-show-register" style={{ marginTop: '8px' }}>Create Account</button>
        
        <form id="email-form" style={{ marginTop: '16px' }} onSubmit={handleSubmit}>
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
          />
          <button 
            type="submit" 
            className="btn btn-primary" 
            id="btn-magic-link"
            disabled={isSending || countdown > 0}
          >
            {isSending ? 'Sending...' : countdown > 0 ? `Resend in ${countdown}s` : 'Send Magic Link'}
          </button>
          
          {statusMsg.text && (
            <div id="email-msg" style={{ marginTop: '12px', fontSize: '13px', color: statusMsg.isError ? 'var(--danger)' : 'var(--success)' }}>
              {statusMsg.text}
            </div>
          )}
        </form>

      </div>
    </div>
  );
}
