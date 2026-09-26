import { createContext, useContext, useState, useEffect } from 'react';
import type { ReactNode } from 'react';
import axios from 'axios';

interface I18nContextType {
  language: string;
  setLanguage: (lang: string) => void;
  t: (key: string, fallback: string) => string;
  availableLanguages: { code: string; label: string }[];
}

const I18nContext = createContext<I18nContextType>({
  language: 'en',
  setLanguage: () => {},
  t: (_key, fallback) => fallback,
  availableLanguages: [],
});

export const useI18n = () => useContext(I18nContext);

const DEFAULT_LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'es', label: 'Español' },
  { code: 'de', label: 'Deutsch' },
  { code: 'fr', label: 'Français' },
  { code: 'ja', label: '日本語' },
  { code: 'ko', label: '한국어' },
  { code: 'pt', label: 'Português' },
  { code: 'ro', label: 'Română' },
  { code: 'zh', label: '中文' },
  { code: 'ar', label: 'العربية' },
];

export const I18nProvider = ({ children }: { children: ReactNode }) => {
  const [language, setLanguageState] = useState<string>(() => {
    return (
      localStorage.getItem('lfr_lang') ||
      (navigator.language || 'en').split('-')[0].toLowerCase()
    );
  });
  const [dictionary, setDictionary] = useState<Record<string, string>>({});

  useEffect(() => {
    // Save to local storage for persistent preference across sessions/unauth views
    localStorage.setItem('lfr_lang', language);

    // Set text direction for RTL languages
    if (language === 'ar') {
      document.documentElement.dir = 'rtl';
    } else {
      document.documentElement.dir = 'ltr';
    }

    // ...and the locale itself (#2262). Only `dir` was set here, so index.html's hardcoded
    // <html lang="en"> survived every switch: a screen reader picked an English synthesiser for
    // all nine non-English locales, and `aria-label` landmark names were announced half in each
    // language. This effect is keyed on `language`, so it also runs on mount with the preference
    // restored from localStorage -- the reload path is covered by the same line.
    //
    // Guarded against the supported set rather than assigned raw: `language` falls back to
    // navigator.language's primary subtag, which can be any tag at all ('it-IT' -> 'it'), and
    // /api/i18n answers an unknown locale with the English bundle (pkg/server/i18n.go:164).
    // Echoing it would declare a language the page is not rendering. Every code in
    // DEFAULT_LANGUAGES is an ISO 639-1 subtag and so valid BCP 47 as-is.
    const isSupported = DEFAULT_LANGUAGES.some((l) => l.code === language);
    document.documentElement.lang = isSupported ? language : 'en';

    // Fetch translations
    axios
      .get(`/api/i18n?lang=${language}`)
      .then((res) => {
        if (res.data) setDictionary(res.data);
      })
      .catch((err) => {
        console.error('Failed to fetch translations:', err);
      });
  }, [language]);

  const setLanguage = (lang: string) => {
    setLanguageState(lang);
  };

  const t = (key: string, fallback: string): string => {
    return dictionary[key] || fallback;
  };

  return (
    <I18nContext.Provider
      value={{
        language,
        setLanguage,
        t,
        availableLanguages: DEFAULT_LANGUAGES,
      }}
    >
      {children}
    </I18nContext.Provider>
  );
};
