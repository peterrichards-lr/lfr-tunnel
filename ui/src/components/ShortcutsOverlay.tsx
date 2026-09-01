import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';
import ModalShell from './ModalShell';

/**
 * Keyboard shortcuts, and the overlay that documents them (#1611).
 *
 * #1562 added arrow, Home and End navigation to the sidebar and nothing said so, which is how the
 * repo owner came to find it by accident. A shortcut nobody can discover is close to no shortcut.
 *
 * ## Why `g` chords rather than Ctrl+Shift+letter
 *
 * Ctrl+Shift+<letter> is the obvious shape and collides with the browser -- differently per
 * browser and platform. D is "bookmark all tabs" in Chrome and responsive design mode in Firefox;
 * T reopens a closed tab; I, J, C and K are developer tools; N is a new incognito window. Whatever
 * letters we picked, some users would get two actions at once and the winner would depend on
 * their browser.
 *
 * `g` followed by a letter is what GitHub, Gmail, Jira and Slack use, so it is already familiar,
 * and it takes nothing from the browser.
 */
type Shortcut = {
  keys: string;
  label: string;
  path?: string;
};

const GO_TO: Shortcut[] = [
  { keys: 'g d', label: 'Dashboard', path: '/dashboard' },
  { keys: 'g a', label: 'Analytics', path: '/analytics' },
  { keys: 'g o', label: 'Account Settings', path: '/account' },
  { keys: 'g w', label: 'Registered Subdomains', path: '/admin/subdomains' },
  { keys: 'g c', label: 'Custom Domains', path: '/admin/vanity-domain-status' },
  { keys: 'g e', label: 'Extension Requests', path: '/admin/extensions' },
  { keys: 'g u', label: 'Users', path: '/admin/users' },
  { keys: 'g t', label: 'API Tokens', path: '/admin/tokens' },
  { keys: 'g y', label: 'Telemetry', path: '/admin/telemetry' },
  { keys: 'g n', label: 'Network Health', path: '/admin/edge-health' },
  { keys: 'g l', label: 'Audit Logs', path: '/admin/audit' },
  { keys: 'g i', label: 'IP Blacklist', path: '/admin/blacklist' },
  { keys: 'g k', label: 'Magic Links', path: '/admin/magic-links' },
  { keys: 'g b', label: 'Database Backups', path: '/admin/backups' },
  { keys: 'g m', label: 'Gateway Maintenance', path: '/admin/maintenance' },
  { keys: 'g s', label: 'System Settings', path: '/admin/settings' },
];

// Every destination the sidebar can show. Admin ones are simply absent from the DOM for a
// non-admin, which is what availableDestinations() reads.
//
// Availability is NOT decided by a role flag here any more (#1640). It used to be, and the list
// drifted from the sidebar: `g a` and `g t` pointed into the Admin Zone with no adminOnly flag,
// so a non-admin was shown both in this overlay and navigated somewhere they could not use --
// while their own Analytics and Account had no shortcut at all. Same shape as #1512.
//
// V1 never had that bug because it asks the nav element whether it is visible instead of
// re-stating the role rules. This does the same: one source of truth, and a sidebar link added
// or gated differently cannot leave a stale entry behind here.
const PORTAL_BASENAME = '/portalv2';

function isReachable(path: string): boolean {
  const link = document.querySelector<HTMLElement>(
    `.sidebar a[href="${PORTAL_BASENAME}${path}"]`,
  );
  // offsetParent is null for a link inside a collapsed or hidden section, so this covers "the
  // sidebar chose not to show it" as well as "it is not rendered at all".
  return !!link && link.offsetParent !== null;
}

// Documented but not bound here -- the sidebar owns these (#1562). Listed because the overlay is
// meant to answer "what can I do with the keyboard", not "what did this component register".
const SIDEBAR_KEYS: Shortcut[] = [
  { keys: '↑ ↓', label: 'Move between sidebar links' },
  { keys: 'Home / End', label: 'First or last sidebar link' },
  { keys: 'Tab', label: 'Move through the page as usual' },
];

/** True when the user is typing, and a plain key must be left alone. */
function isTyping(el: Element | null): boolean {
  if (!el) return false;
  const tag = el.tagName.toLowerCase();
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
  return (el as HTMLElement).isContentEditable;
}

export default function ShortcutsOverlay({ user }: { user: any }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const { t } = useI18n();

  const isAdmin = user?.role === 'admin' || user?.role === 'owner';
  // Memoised, and the chord state lives in a ref. Both matter: without the memo this array is a
  // new value every render, so the effect below tears down and re-registers its listener each
  // time -- and with the pending 'g' held in a local, that re-registration silently forgets it.
  // Typing into any field re-renders, so the chord would break exactly when a key arrived during
  // one. Found by mutation testing: removing the typing guard left the tests passing, because
  // the chord was already failing for this reason instead.
  // Recomputed when the overlay opens and when the role changes, because the sidebar has to be
  // in the DOM for isReachable() to read it.
  const [goTo, setGoTo] = useState<Shortcut[]>(GO_TO);
  useEffect(() => {
    setGoTo(GO_TO.filter((s) => (s.path ? isReachable(s.path) : true)));
  }, [isAdmin, open]);
  const awaitingG = useRef(false);
  const chordTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );

  useEffect(() => {
    const clearChord = () => {
      awaitingG.current = false;
      if (chordTimer.current) clearTimeout(chordTimer.current);
    };

    const onKeyDown = (e: KeyboardEvent) => {
      // Never take a key from a field, and never from the browser: Ctrl+G, Cmd+G and friends
      // stay theirs. This is the guard #1562 called for and is what makes plain keys safe here.
      if (isTyping(document.activeElement)) return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;

      if (e.key === '?') {
        e.preventDefault();
        setOpen(true);
        clearChord();
        return;
      }

      if (awaitingG.current) {
        const match = goTo.find((s) => s.keys.endsWith(e.key.toLowerCase()));
        clearChord();
        if (match?.path) {
          e.preventDefault();
          setOpen(false);
          navigate(match.path);
        }
        return;
      }

      if (e.key.toLowerCase() === 'g') {
        awaitingG.current = true;
        // Times out so a stray g does not lie in wait and swallow the next keystroke.
        chordTimer.current = setTimeout(() => {
          awaitingG.current = false;
        }, 1000);
      }
    };

    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      if (chordTimer.current) clearTimeout(chordTimer.current);
    };
  }, [goTo, navigate]);

  return (
    <>
      {/* A visible way in. An overlay reachable only by the shortcut it documents helps nobody
          who does not already know the shortcut. */}
      <button
        type="button"
        className="shortcuts-trigger"
        onClick={() => setOpen(true)}
        aria-label={t('shortcuts_open', 'Keyboard shortcuts')}
        title={t('shortcuts_open', 'Keyboard shortcuts')}
      >
        ?
      </button>

      <ModalShell
        isOpen={open}
        onClose={() => setOpen(false)}
        labelledBy="shortcuts-title"
        cardClassName="modal-card modal-card--md"
      >
        <div className="modal-header">
          <h3 id="shortcuts-title" className="modal-title">
            {t('shortcuts_title', 'Keyboard shortcuts')}
          </h3>
        </div>

        <p className="text-sm text-muted mb-lg">
          {t(
            'shortcuts_intro',
            'Press g then a letter to jump to a page. Shortcuts are ignored while you are typing.',
          )}
        </p>

        <h4 className="text-md fw-semibold mb-sm">
          {t('shortcuts_go_to', 'Go to')}
        </h4>
        <dl className="shortcuts-list mb-lg">
          {goTo.map((s) => (
            <div className="shortcuts-row" key={s.keys}>
              <dt>
                <kbd>{s.keys}</kbd>
              </dt>
              <dd>{s.label}</dd>
            </div>
          ))}
        </dl>

        <h4 className="text-md fw-semibold mb-sm">
          {t('shortcuts_navigation', 'Navigation')}
        </h4>
        <dl className="shortcuts-list">
          {SIDEBAR_KEYS.map((s) => (
            <div className="shortcuts-row" key={s.keys}>
              <dt>
                <kbd>{s.keys}</kbd>
              </dt>
              <dd>{s.label}</dd>
            </div>
          ))}
          <div className="shortcuts-row">
            <dt>
              <kbd>?</kbd>
            </dt>
            <dd>{t('shortcuts_this', 'Show this list')}</dd>
          </div>
          <div className="shortcuts-row">
            <dt>
              <kbd>Esc</kbd>
            </dt>
            <dd>{t('shortcuts_close', 'Close a dialog')}</dd>
          </div>
        </dl>
      </ModalShell>
    </>
  );
}
