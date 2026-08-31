import { useEffect, useMemo, useRef, useState } from 'react';
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
  adminOnly?: boolean;
};

const GO_TO: Shortcut[] = [
  { keys: 'g d', label: 'Dashboard', path: '/dashboard' },
  { keys: 'g a', label: 'Analytics', path: '/admin/analytics' },
  { keys: 'g t', label: 'API Tokens', path: '/admin/tokens' },
  { keys: 'g u', label: 'Users', path: '/admin/users', adminOnly: true },
  {
    keys: 'g s',
    label: 'System Settings',
    path: '/admin/settings',
    adminOnly: true,
  },
  {
    keys: 'g b',
    label: 'Database Backups',
    path: '/admin/backups',
    adminOnly: true,
  },
  {
    keys: 'g m',
    label: 'Gateway Maintenance',
    path: '/admin/maintenance',
    adminOnly: true,
  },
  {
    keys: 'g n',
    label: 'Network Health',
    path: '/admin/edge-health',
    adminOnly: true,
  },
  { keys: 'g l', label: 'Audit Logs', path: '/admin/audit', adminOnly: true },
  {
    keys: 'g c',
    label: 'Custom Domains',
    path: '/admin/vanity-domain-status',
    adminOnly: true,
  },
];

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
  const goTo = useMemo(
    () => GO_TO.filter((s) => !s.adminOnly || isAdmin),
    [isAdmin],
  );
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
