import React, { createContext, useContext, useState, useEffect } from 'react';

export type ThemePreference = 'light' | 'dark' | 'liferay' | 'system' | 'time';
export type ActiveTheme = 'light' | 'dark' | 'liferay';

interface SettingsContextType {
  themePreference: ThemePreference;
  setThemePreference: (pref: ThemePreference) => void;
  theme: ActiveTheme;
  useUTC: boolean;
  toggleUTC: () => void;
  formatDate: (dateString: string | Date | undefined | null) => string;
}

const SettingsContext = createContext<SettingsContextType | undefined>(undefined);

export function SettingsProvider({ children }: { children: React.ReactNode }) {
  const [themePreference, setThemePreference] = useState<ThemePreference>(() => {
    return (localStorage.getItem('theme_preference') as ThemePreference) || 'dark';
  });

  const [theme, setTheme] = useState<ActiveTheme>('dark');

  const [useUTC, setUseUTC] = useState<boolean>(() => {
    return localStorage.getItem('useUTC') === 'true';
  });

  useEffect(() => {
    localStorage.setItem('theme_preference', themePreference);

    const resolveTheme = (): ActiveTheme => {
      if (themePreference === 'light') return 'light';
      if (themePreference === 'dark') return 'dark';
      if (themePreference === 'liferay') return 'liferay';
      if (themePreference === 'time') {
        const hour = new Date().getHours();
        return (hour >= 6 && hour < 18) ? 'light' : 'dark';
      }
      // 'system'
      if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
        return 'light';
      }
      return 'dark';
    };

    const active = resolveTheme();
    setTheme(active);
    document.documentElement.setAttribute('data-theme', active);

    let intervalId: any;
    let mediaQuery: MediaQueryList | null = null;
    let themeChangeHandler: ((e: MediaQueryListEvent) => void) | null = null;

    if (themePreference === 'system') {
      mediaQuery = window.matchMedia('(prefers-color-scheme: light)');
      themeChangeHandler = (e: MediaQueryListEvent) => {
        const nextTheme = e.matches ? 'light' : 'dark';
        setTheme(nextTheme);
        document.documentElement.setAttribute('data-theme', nextTheme);
      };
      mediaQuery.addEventListener('change', themeChangeHandler);
    } else if (themePreference === 'time') {
      intervalId = setInterval(() => {
        const nextTheme = resolveTheme();
        setTheme(nextTheme);
        document.documentElement.setAttribute('data-theme', nextTheme);
      }, 60000);
    }

    return () => {
      if (mediaQuery && themeChangeHandler) {
        mediaQuery.removeEventListener('change', themeChangeHandler);
      }
      if (intervalId) {
        clearInterval(intervalId);
      }
    };
  }, [themePreference]);

  useEffect(() => {
    localStorage.setItem('useUTC', String(useUTC));
  }, [useUTC]);

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
    <SettingsContext.Provider value={{ themePreference, setThemePreference, theme, useUTC, toggleUTC, formatDate }}>
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
