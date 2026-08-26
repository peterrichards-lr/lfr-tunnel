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
  showPrompt: (
    title: string,
    message: string,
    defaultValue?: string,
  ) => Promise<string | null>;
}

const UIContext = createContext<UIContextType | undefined>(undefined);

export const useUI = () => {
  const context = useContext(UIContext);
  if (!context) {
    throw new Error('useUI must be used within a UIProvider');
  }
  return context;
};

export const UIProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
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

  const showToast = (
    message: string,
    type: 'success' | 'error' | 'info' = 'info',
  ) => {
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

  const showPrompt = (
    title: string,
    message: string,
    defaultValue: string = '',
  ): Promise<string | null> => {
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
    <UIContext.Provider
      value={{ showToast, showAlert, showConfirm, showPrompt }}
    >
      {children}

      {/* Floating Toasts container */}
      <div className="toast-stack">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`toast-card${toast.type === 'success' ? ' is-success' : toast.type === 'error' ? ' is-error' : ''}`}
          >
            <span className="toast-message">{toast.message}</span>
            <button
              type="button"
              className="toast-dismiss"
              aria-label={t('dismiss_notification', 'Dismiss notification')}
              onClick={() =>
                setToasts((prev) => prev.filter((t) => t.id !== toast.id))
              }
            >
              ×
            </button>
          </div>
        ))}
      </div>

      {/* Async Custom Dialog Modal overlay */}
      {activeDialog && (
        <div className="dialog-overlay">
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="global-dialog-title"
            className="dialog-card"
          >
            <div>
              <h3 id="global-dialog-title" className="dialog-title">
                {activeDialog.title}
              </h3>
              <p className="dialog-message">{activeDialog.message}</p>
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
                className="form-control"
              />
            )}

            <div className="dialog-actions">
              {activeDialog.type !== 'alert' && (
                <button
                  type="button"
                  onClick={handleCancel}
                  className="btn btn-outline w-auto m-0"
                >
                  {t('cancel', 'Cancel')}
                </button>
              )}
              <button
                type="button"
                onClick={handleConfirm}
                className="btn btn-primary w-auto m-0"
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
