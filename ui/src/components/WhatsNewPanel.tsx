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
    <div className="card mb-xl" style={{ animationDelay: '0.3s' }}>
      <div className="flex justify-between items-center mb-lg">
        <div>
          <h3 className="section-title m-0">{t('whats_new', "What's New")}</h3>
        </div>
      </div>
      
      <div className="max-h-96 overflow-y-auto pr-sm">
        {releases.map((release, i) => (
          <div key={i} className={i === releases.length - 1 ? '' : 'mb-lg'}>
            <h4 className="m-0 mb-xs text-sm text-main">
              {release.version}{' '}
              {release.release_date && (
                <span className="text-xs text-muted fw-normal">
                  ({release.release_date})
                </span>
              )}
            </h4>
            <ul className="m-0 pl-lg text-secondary text-sm leading-relaxed break-words">
              {release.features && release.features.length > 0 ? (
                release.features.map((feature, j) => (
                  <li key={j} className="mb-2xs min-w-0">
                    {renderFeatureItem(feature)}
                  </li>
                ))
              ) : (
                <li>{t('no_changes_documented', 'No changes documented.')}</li>
              )}
            </ul>
          </div>
        ))}
      </div>
    </div>
  );
}
