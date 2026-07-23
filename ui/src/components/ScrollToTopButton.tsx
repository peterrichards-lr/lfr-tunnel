import { useState, useEffect } from 'react';
import { useI18n } from '../contexts/I18nContext';

export default function ScrollToTopButton() {
  const [visible, setVisible] = useState(false);
  const { t } = useI18n();

  useEffect(() => {
    const handleScroll = () => {
      if (window.scrollY > 300) {
        setVisible(true);
      } else {
        setVisible(false);
      }
    };
    window.addEventListener('scroll', handleScroll);
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  const scrollToTop = () => {
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };

  if (!visible) return null;

  return (
    <button
      onClick={scrollToTop}
      className="btn btn-secondary scroll-to-top-btn"
      aria-label={t('return_to_top', 'Return to Top')}
      title={t('return_to_top', 'Return to Top')}
    >
      <span className="text-xs font-semibold flex items-center gap-xs">
        ↑ {t('top', 'Top')}
      </span>
    </button>
  );
}
