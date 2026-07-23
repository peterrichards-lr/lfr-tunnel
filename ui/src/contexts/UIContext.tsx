import React, { createContext, useContext, useState, useEffect } from 'react';
import { useI18n } from './I18nContext';

interface Toast {
  id: string;
  message: string;
  type: 'success' | 'error' | 'info';
}

interface DialogConfig {
  type: 'alert' | 'confirm' | 'prompt';
  title: string;
  message: string;
  defaultValue?: string;
  resolve: (value: any) => void;
}

interface UIContextType {
  showToast: (message: string, type?: 'success' | 'error' | 'info') => void;
  showAlert: (title: string, message: string) => Promise<void>;
  showConfirm: (title: string, message: string) => Promise<boolean>;
  showPrompt: (title: string, message: string, defaultValue?: string) => Promise<string | null>;
}

const UIContext = createContext<UIContextType | undefined>(undefined);

export const useUI = () => {
  const context = useContext(UIContext);
  if (!context) {
    throw new Error('useUI must be used within a UIProvider');
  }
  return context;
};

export const UIProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { t } = useI18n();
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [activeDialog, setActiveDialog] = useState<DialogConfig | null>(null);
  const [promptValue, setPromptValue] = useState('');

  // Auto-dismiss toasts after 4 seconds
  useEffect(() => {
    if (toasts.length > 0) {
      const timer = setTimeout(() => {
        setToasts((prev) => prev.slice(1));
      }, 4000);
      return () => clearTimeout(timer);
    }
  }, [toasts]);

  // Set default value when prompt active dialog changes
  useEffect(() => {
    if (activeDialog && activeDialog.type === 'prompt') {
      setPromptValue(activeDialog.defaultValue || '');
    }
  }, [activeDialog]);

  const showToast = (message: string, type: 'success' | 'error' | 'info' = 'info') => {
    const id = Math.random().toString(36).substring(2, 9);
    setToasts((prev) => [...prev, { id, message, type }]);
  };

  const showAlert = (title: string, message: string): Promise<void> => {
    return new Promise<void>((resolve) => {
      setActiveDialog({
        type: 'alert',
        title,
        message,
        resolve: () => {
          setActiveDialog(null);
          resolve();
        },
      });
    });
  };

  const showConfirm = (title: string, message: string): Promise<boolean> => {
    return new Promise<boolean>((resolve) => {
      setActiveDialog({
        type: 'confirm',
        title,
        message,
        resolve: (val) => {
          setActiveDialog(null);
          resolve(val);
        },
      });
    });
  };

  const showPrompt = (title: string, message: string, defaultValue: string = ''): Promise<string | null> => {
    return new Promise<string | null>((resolve) => {
      setActiveDialog({
        type: 'prompt',
        title,
        message,
        defaultValue,
        resolve: (val) => {
          setActiveDialog(null);
          resolve(val);
        },
      });
    });
  };

  const handleConfirm = () => {
    if (!activeDialog) return;
    if (activeDialog.type === 'prompt') {
      activeDialog.resolve(promptValue);
    } else {
      activeDialog.resolve(true);
    }
  };

  const handleCancel = () => {
    if (!activeDialog) return;
    if (activeDialog.type === 'prompt') {
      activeDialog.resolve(null);
    } else {
      activeDialog.resolve(false);
    }
  };

  return (
    <UIContext.Provider value={{ showToast, showAlert, showConfirm, showPrompt }}>
      {children}

      {/* Floating Toasts container */}
      <div style={{
        position: 'fixed',
        top: '24px',
        right: '24px',
        zIndex: 9999,
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--spacing-sm)',
        maxWidth: '350px',
        width: '100%'
      }}>
        {toasts.map((toast) => (
          <div
            key={toast.id}
            style={{
              padding: 'var(--spacing-md) var(--spacing-lg)',
              borderRadius: '8px',
              background: 'var(--bg-card)',
              color: 'var(--text-main)',
              borderLeft: `4px solid ${
                toast.type === 'success' ? 'var(--success)' : toast.type === 'error' ? 'var(--danger)' : 'var(--primary)'
              }`,
              borderTop: '1px solid var(--border)',
              borderRight: '1px solid var(--border)',
              borderBottom: '1px solid var(--border)',
              boxShadow: 'var(--shadow-glass)',
              backdropFilter: 'blur(12px)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              animation: 'slideIn 0.3s ease-out'
            }}
          >
            <span style={{ fontSize: '14px', fontWeight: 500 }}>{toast.message}</span>
            <button
              type="button"
              aria-label={t('dismiss_notification', 'Dismiss notification')}
              onClick={() => setToasts((prev) => prev.filter((t) => t.id !== toast.id))}
              style={{
                background: 'transparent',
                border: 'none',
                color: 'var(--text-muted)',
                cursor: 'pointer',
                fontSize: '16px',
                padding: '0 var(--spacing-xs)',
                marginLeft: 'var(--spacing-md)'
              }}
            >
              ×
            </button>
          </div>
        ))}
      </div>

      {/* Async Custom Dialog Modal overlay */}
      {activeDialog && (
        <div style={{
          position: 'fixed',
          inset: 0,
          background: 'var(--modal-overlay)',
          backdropFilter: 'blur(8px)',
          zIndex: 9998,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 'var(--spacing-xl)'
        }}>
          <div 
            role="dialog"
            aria-modal="true"
            aria-labelledby="global-dialog-title"
            style={{
              background: 'var(--bg-card)',
              border: '1px solid var(--border)',
              borderRadius: '12px',
              padding: 'var(--spacing-xl)',
              maxWidth: '420px',
              width: '100%',
              boxShadow: 'var(--shadow-glass)',
              backdropFilter: 'blur(16px)',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--spacing-lg)'
            }}
          >
            <div>
              <h3 id="global-dialog-title" style={{ margin: 0, fontSize: '18px', fontWeight: 700, color: 'var(--text-main)' }}>
                {activeDialog.title}
              </h3>
              <p style={{ margin: 'var(--spacing-sm) 0 0 0', fontSize: '14px', color: 'var(--text-muted)', lineHeight: '1.5' }}>
                {activeDialog.message}
              </p>
            </div>

            {activeDialog.type === 'prompt' && (
              <input
                type="text"
                value={promptValue}
                onChange={(e) => setPromptValue(e.target.value)}
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleConfirm();
                  if (e.key === 'Escape') handleCancel();
                }}
                style={{
                  width: '100%',
                  padding: 'var(--spacing-md)',
                  background: 'var(--input-bg)',
                  border: '1px solid var(--border)',
                  borderRadius: '6px',
                  color: 'var(--text-main)',
                  outline: 'none',
                  fontSize: '14px'
                }}
              />
            )}

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--spacing-md)', marginTop: 'var(--spacing-sm)' }}>
              {activeDialog.type !== 'alert' && (
                <button
                  type="button"
                  onClick={handleCancel}
                  style={{
                    padding: 'var(--spacing-md) var(--spacing-lg)',
                    background: 'transparent',
                    border: '1px solid var(--border)',
                    borderRadius: '6px',
                    color: 'var(--text-muted)',
                    cursor: 'pointer',
                    fontSize: '14px',
                    fontWeight: 600,
                    transition: 'all 0.2s'
                  }}
                >
                  {t('cancel', 'Cancel')}
                </button>
              )}
              <button
                type="button"
                onClick={handleConfirm}
                style={{
                  padding: 'var(--spacing-md) var(--spacing-lg)',
                  background: 'var(--primary)',
                  border: 'none',
                  borderRadius: '6px',
                  color: '#ffffff',
                  cursor: 'pointer',
                  fontSize: '14px',
                  fontWeight: 600,
                  boxShadow: 'var(--shadow-glow)',
                  transition: 'all 0.2s'
                }}
              >
                {t('confirm', 'Confirm')}
              </button>
            </div>
          </div>
        </div>
      )}
    </UIContext.Provider>
  );
};
