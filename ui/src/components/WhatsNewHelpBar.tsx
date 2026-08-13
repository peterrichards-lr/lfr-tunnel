import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';

interface ReleaseNote {
  version: string;
  release_date?: string;
  features: string[];
}

// Tracks which release the user has already expanded this bar for, so a genuinely
// new release auto-expands once (and shows a badge) without re-expanding every visit.
const SEEN_VERSION_KEY = 'whats_new_seen_version';

function renderFeatureItem(feature: string) {
  const colonIdx = feature.indexOf(':');
  if (colonIdx !== -1) {
    return (
      <>
        <strong>{feature.substring(0, colonIdx + 1)}</strong>
        {feature.substring(colonIdx + 1)}
      </>
    );
  }
  return <>{feature}</>;
}

// Replaces the old always-visible right-hand sidebar (WhatsNewPanel + a Help &
// Resources card) with a single collapsible bar at the top of the dashboard,
// minimized by default. Both were low-frequency-interaction content that didn't
// need permanent screen space -- worse, on narrow screens the old sidebar column
// collapsed to the *bottom* of a long single-column stack (after Reservations,
// Vanity Domains, Tunnels), so a fixed top bar is also the more consistent mobile
// layout, not just a desktop decluttering.
export default function WhatsNewHelpBar({ onInstallClick }: { onInstallClick: () => void }) {
  const { t } = useI18n();
  const [releases, setReleases] = useState<ReleaseNote[]>([]);
  const [expanded, setExpanded] = useState(false);
  const [hasUnseen, setHasUnseen] = useState(false);

  useEffect(() => {
    axios.get('/static/whats-new.json')
      .then((res) => {
        const data: ReleaseNote[] = Array.isArray(res.data)
          ? res.data
          : (res.data?.version && Array.isArray(res.data?.features) ? [res.data] : []);
        setReleases(data);

        const latest = data[0]?.version;
        if (latest && localStorage.getItem(SEEN_VERSION_KEY) !== latest) {
          setHasUnseen(true);
          setExpanded(true);
        }
      })
      .catch(() => {});
  }, []);

  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    if (next && releases[0]) {
      localStorage.setItem(SEEN_VERSION_KEY, releases[0].version);
      setHasUnseen(false);
    }
  };

  if (releases.length === 0) {
    return null;
  }

  return (
    <div className="card mb-xl">
      <button
        type="button"
        onClick={toggle}
        className="btn btn-outline flex justify-between items-center w-full"
        aria-expanded={expanded}
      >
        <span className="flex items-center gap-sm">
          {t('whats_new_help', "What's New & Help")}
          {hasUnseen && <span className="badge badge-primary">{t('new', 'New')}</span>}
        </span>
        <span className="flex items-center gap-xs text-sm">
          {expanded ? t('hide', 'Hide') : t('show', 'Show')}
          <span aria-hidden="true">{expanded ? '▾' : '▸'}</span>
        </span>
      </button>

      {expanded && (
        // Real flex-wrap, not a responsive-prefix utility class -- this project's
        // custom CSS system (Tailwind-style class *names* but hand-written, no
        // actual Tailwind build step) never implemented sm:/md:/lg: variants, so
        // e.g. "md:flex-row" silently did nothing (same reason the *old* sidebar's
        // "lg:grid-cols-3" never actually put it next to Reservations on desktop
        // either). flex-wrap with a min-width per child reflows naturally with no
        // breakpoint to get wrong.
        <div className="mt-lg flex flex-wrap gap-xl">
          <div className="flex-1 min-w-0" style={{ minWidth: '260px' }}>
            <h4 className="section-title text-sm mb-md">{t('whats_new', "What's New")}</h4>
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

          <div className="flex-1 min-w-0" style={{ minWidth: '260px' }}>
            <h4 className="section-title text-sm mb-md">{t('help_resources', 'Help & Resources')}</h4>
            <div className="flex flex-col gap-md">
              <button type="button" className="btn btn-outline justify-start text-left" onClick={onInstallClick}>
                💻 {t('guide_title', 'Client Installation Guide')}
              </button>
              <button
                type="button"
                className="btn btn-outline justify-start text-left"
                onClick={() => window.dispatchEvent(new CustomEvent('start-onboarding-tour'))}
              >
                🧭 {t('onboarding_guide_title', 'Run Dashboard Onboarding Tour')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
