import React, { createContext, useContext, useState, useEffect } from 'react';

interface SettingsContextType {
  theme: 'light' | 'dark';
  toggleTheme: () => void;
  useUTC: boolean;
  toggleUTC: () => void;
  formatDate: (dateString: string | Date | undefined | null) => string;
}

const SettingsContext = createContext<SettingsContextType | undefined>(undefined);

export function SettingsProvider({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    return (localStorage.getItem('theme') as 'light' | 'dark') || 'dark';
  });

  const [useUTC, setUseUTC] = useState<boolean>(() => {
    return localStorage.getItem('useUTC') === 'true';
  });

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem('theme', theme);
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('useUTC', String(useUTC));
  }, [useUTC]);

  const toggleTheme = () => setTheme(prev => prev === 'dark' ? 'light' : 'dark');
  const toggleUTC = () => setUseUTC(prev => !prev);

  const formatDate = (dateString: string | Date | undefined | null): string => {
    if (!dateString) return '';
    const date = new Date(dateString);
    if (isNaN(date.getTime())) return 'Never';
    
    const options: Intl.DateTimeFormatOptions = { 
      year: 'numeric', month: 'short', day: 'numeric', 
      hour: '2-digit', minute: '2-digit', second: '2-digit',
      timeZoneName: 'short'
    };

    if (useUTC) {
      options.timeZone = 'UTC';
    } else {
      options.timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    }

    return date.toLocaleString(undefined, options);
  };

  return (
    <SettingsContext.Provider value={{ theme, toggleTheme, useUTC, toggleUTC, formatDate }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings() {
  const context = useContext(SettingsContext);
  if (context === undefined) {
    throw new Error('useSettings must be used within a SettingsProvider');
  }
  return context;
}
