import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';

interface ReleaseNote {
  version: string;
  release_date?: string;
  features: string[];
}

export default function WhatsNewPanel() {
  const [releases, setReleases] = useState<ReleaseNote[]>([]);
  const { t } = useI18n();

  useEffect(() => {
    const fetchWhatsNew = async () => {
      try {
        const res = await axios.get('/static/whats-new.json');
        if (Array.isArray(res.data)) {
          setReleases(res.data);
        } else if (res.data.version && Array.isArray(res.data.features)) {
          setReleases([res.data]);
        }
      } catch (err) {
        console.error('Failed to load whats-new.json', err);
      }
    };
    fetchWhatsNew();
  }, []);

  if (releases.length === 0) {
    return null; // Do not render if there's no release notes
  }

  const renderFeatureItem = (feature: string) => {
    const colonIdx = feature.indexOf(':');
    if (colonIdx !== -1) {
      const boldPart = feature.substring(0, colonIdx + 1);
      const regularPart = feature.substring(colonIdx + 1);
      return (
        <>
          <strong>{boldPart}</strong>
          {regularPart}
        </>
      );
    }
    return <>{feature}</>;
  };

  return (
    <div className="card" style={{ marginBottom: '24px', animationDelay: '0.3s' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <div>
          <h3 style={{ margin: 0, fontSize: '20px', fontWeight: 700 }}>{t('whats_new', "What's New")}</h3>
        </div>
      </div>
      
      <div style={{ maxHeight: '400px', overflowY: 'auto', paddingRight: '8px' }}>
        {releases.map((release, i) => (
          <div key={i} style={{ marginBottom: i === releases.length - 1 ? '0' : '20px' }}>
            <h4 style={{ margin: '0 0 8px 0', fontSize: '15px', color: 'var(--text-main)' }}>
              {release.version}{' '}
              {release.release_date && (
                <span style={{ fontSize: '13px', color: 'var(--text-muted)', fontWeight: 'normal' }}>
                  ({release.release_date})
                </span>
              )}
            </h4>
            <ul style={{ margin: 0, paddingLeft: '20px', color: 'var(--text-secondary)', fontSize: '14px', lineHeight: '1.6' }}>
              {release.features && release.features.length > 0 ? (
                release.features.map((feature, j) => (
                  <li key={j} style={{ marginBottom: '4px' }}>
                    {renderFeatureItem(feature)}
                  </li>
                ))
              ) : (
                <li>No changes documented.</li>
              )}
            </ul>
          </div>
        ))}
      </div>
    </div>
  );
}
