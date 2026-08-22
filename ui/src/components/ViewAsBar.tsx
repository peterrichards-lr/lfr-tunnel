import { useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';

interface ViewAsBarProps {
  // The role currently being previewed, or "" when not previewing.
  viewAs?: string;
  // Whether this account may start a preview at all. Comes from the server's view of the
  // real role, never from the role on screen -- while previewing, the role on screen is
  // deliberately not the owner's.
  canViewAs?: boolean;
}

const PREVIEWABLE_ROLES = ['admin', 'user'];

/**
 * Lets the owner preview the portal as a lower role, and makes it obvious when they are
 * (#1225).
 *
 * The bar is deliberately loud while a preview is active. The session is read-only, so
 * every action will be refused by the server -- an owner who cannot tell they are
 * previewing would reasonably report those refusals as bugs.
 *
 * This component only asks; the server decides. It cannot grant anything, and a reload
 * reflects whatever the server actually recorded.
 */
export default function ViewAsBar({ viewAs, canViewAs }: ViewAsBarProps) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);

  if (!canViewAs && !viewAs) return null;

  const setRole = async (role: string) => {
    setBusy(true);
    try {
      await axios.post('/api/me/view-as', { role });
      // Full reload rather than local state: every page's contents depend on the role, and
      // re-fetching from the server is the only way to be sure the screen matches it.
      window.location.reload();
    } catch {
      setBusy(false);
    }
  };

  if (viewAs) {
    return (
      <div className="view-as-bar" role="status">
        <span className="view-as-bar__label">
          {t('view_as_active', 'Previewing as')} <strong>{viewAs}</strong>
          {' — '}
          {t('view_as_read_only', 'read-only, no changes can be made')}
        </span>
        <button
          type="button"
          className="btn btn-outline py-xs px-md text-xs w-auto m-0"
          onClick={() => setRole('')}
          disabled={busy}
        >
          {t('view_as_exit', 'Exit preview')}
        </button>
      </div>
    );
  }

  return (
    <div className="view-as-switcher">
      <label className="text-xs text-muted" htmlFor="view-as-select">
        {t('view_as_label', 'View as')}
      </label>
      <select
        id="view-as-select"
        className="form-control w-auto text-xs py-xs"
        value=""
        disabled={busy}
        onChange={(e) => e.target.value && setRole(e.target.value)}
      >
        <option value="">{t('view_as_owner', 'Owner (you)')}</option>
        {PREVIEWABLE_ROLES.map((role) => (
          <option key={role} value={role}>
            {role}
          </option>
        ))}
      </select>
    </div>
  );
}
