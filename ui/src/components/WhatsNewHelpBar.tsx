import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface ReleaseNote {
  version: string;
  release_date?: string;
  features: string[];
}

// Tracks which release the user has already expanded this bar for, so the "New" badge
// clears once they've actually looked. The bar itself never opens on its own.
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
// The gateway's /api/version fields this bar reads (#2526). Each is optional: a deployment that
// configures none of them shows none of these links, as V1 does.
interface HelpLinks {
  documentation_url?: string;
  secure_token_guide_url?: string;
  docker_image?: string;
  docker_hub_url?: string;
  docker_bypass_url?: string;
}

export default function WhatsNewHelpBar({
  onInstallClick,
  serverConfig,
}: {
  onInstallClick: () => void;
  serverConfig?: HelpLinks | null;
}) {
  const { t } = useI18n();
  const { showToast } = useUI();
  const docker = serverConfig?.docker_image;
  // V1's fallback when the gateway names an image but no Docker Hub page.
  const dockerHub =
    serverConfig?.docker_hub_url ||
    (docker ? `https://hub.docker.com/r/${docker.split(':')[0]}` : '');
  const copyDockerPull = async () => {
    try {
      await navigator.clipboard.writeText(`docker pull ${docker}`);
      showToast(t('copied_to_clipboard', 'Copied to clipboard.'), 'success');
    } catch {
      showToast(t('copy_failed', 'Could not copy to the clipboard.'), 'error');
    }
  };
  const [releases, setReleases] = useState<ReleaseNote[]>([]);
  const [expanded, setExpanded] = useState(false);
  const [hasUnseen, setHasUnseen] = useState(false);

  useEffect(() => {
    axios
      .get('/static/whats-new.json')
      .then((res) => {
        const data: ReleaseNote[] = Array.isArray(res.data)
          ? res.data
          : res.data?.version && Array.isArray(res.data?.features)
            ? [res.data]
            : [];
        setReleases(data);

        // Flag an unseen release with the badge, but leave the bar collapsed. It is
        // low-frequency content that shouldn't take the top of the dashboard uninvited,
        // and auto-expanding meant every user got it reopened after each release --
        // exactly the permanent screen space this bar replaced the old sidebar to avoid.
        const latest = data[0]?.version;
        if (latest && localStorage.getItem(SEEN_VERSION_KEY) !== latest) {
          setHasUnseen(true);
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

  // Not hidden when the release notes are missing or failed to load: the bar also carries the
  // help links and the docker pull box (#2526), and those must not depend on whats-new.json.

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
          {hasUnseen && (
            <span className="badge badge-primary">{t('new', 'New')}</span>
          )}
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
          <div className="flex-1 min-w-0 min-w-lg">
            <h4 className="section-title text-sm mb-md">
              {t('whats_new', "What's New")}
            </h4>
            <div className="max-h-96 overflow-y-auto pr-sm">
              {releases.map((release, i) => (
                <div
                  key={i}
                  className={i === releases.length - 1 ? '' : 'mb-lg'}
                >
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
                      <li>
                        {t('no_changes_documented', 'No changes documented.')}
                      </li>
                    )}
                  </ul>
                </div>
              ))}
            </div>
          </div>

          <div className="flex-1 min-w-0 min-w-lg">
            <h4 className="section-title text-sm mb-md">
              {t('help_resources', 'Help & Resources')}
            </h4>
            <div className="flex flex-col gap-md">
              <button
                type="button"
                className="btn btn-outline justify-start text-left"
                onClick={onInstallClick}
              >
                💻 {t('guide_title', 'Client Installation Guide')}
              </button>
              <button
                type="button"
                className="btn btn-outline justify-start text-left"
                onClick={() =>
                  window.dispatchEvent(new CustomEvent('start-onboarding-tour'))
                }
              >
                {t('onboarding_guide_title', 'Onboarding Tour')}
              </button>
              {serverConfig?.documentation_url && (
                <a
                  className="btn btn-outline justify-start text-left"
                  href={serverConfig.documentation_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  📖 {t('read_documentation', 'Read the Documentation')}
                </a>
              )}
              {serverConfig?.secure_token_guide_url && (
                <a
                  className="btn btn-outline justify-start text-left"
                  href={serverConfig.secure_token_guide_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  🔐{' '}
                  {t(
                    'secure_token_storage',
                    'Secure Token Storage (LDM-Style)',
                  )}
                </a>
              )}
            </div>
            {docker && (
              <div className="mt-lg" data-testid="docker-pull-box">
                <h4 className="section-title text-sm mb-xs">
                  {t('docker_image_title', 'Docker Hub Image')}
                </h4>
                <p className="text-xs text-muted mb-sm">
                  {t(
                    'docker_pull_desc',
                    'You can pull the official Docker client image directly:',
                  )}
                </p>
                <div className="flex items-center surface-subtle rounded border px-md py-xs gap-sm mb-sm">
                  <code className="text-xs font-mono text-main flex-1">
                    docker pull {docker}
                  </code>
                  <button
                    type="button"
                    onClick={copyDockerPull}
                    className="btn-bare p-xs text-xs rounded text-muted"
                    aria-label={t(
                      'copy_docker_pull',
                      'Copy the docker pull command',
                    )}
                  >
                    📋
                  </button>
                </div>
                <div className="flex flex-col gap-xs text-sm">
                  <a href={dockerHub} target="_blank" rel="noreferrer">
                    → {t('docker_view_hub', 'View on Docker Hub')}
                  </a>
                  {serverConfig?.docker_bypass_url && (
                    <a
                      href={serverConfig.docker_bypass_url}
                      target="_blank"
                      rel="noreferrer"
                    >
                      → {t('docker_edr_guide', 'EDR Bypass Guide')}
                    </a>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
