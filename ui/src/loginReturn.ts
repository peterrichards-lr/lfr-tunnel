import { PORTAL_BASE } from './portalBase';

/**
 * Where to go after signing in (#2528).
 *
 * A deep link -- an email's /portal/admin/users, a redirected V1 section link -- reaches a
 * signed-out visitor as the sign-in page, and sign-in always lands on the dashboard. The page
 * they asked for is remembered here when the app sends them to sign in, and taken back once a
 * session exists.
 *
 * localStorage rather than the URL, because the ways in do not share one: a magic link opens in
 * whatever tab the mail client picks, and SSO returns through the identity provider to a fixed
 * path. Kept briefly, so an abandoned sign-in does not redirect one much later.
 */
const KEY = 'lfr_login_return';
const MAX_AGE_MS = 15 * 60 * 1000;

// A router path inside V2: one leading slash (never `//host`, which the browser reads as another
// origin), and not the sign-in page itself.
function isReturnable(path: string): boolean {
  return (
    path.startsWith('/') &&
    !path.startsWith('//') &&
    !path.includes('\\') &&
    path !== '/login' &&
    !path.startsWith('/login?') &&
    !path.startsWith('/login/')
  );
}

/** Remembers the page the browser is on, as a router path, before it is sent to sign in. */
export function rememberReturnPath(): void {
  const { pathname, search } = window.location;
  if (pathname !== PORTAL_BASE && !pathname.startsWith(PORTAL_BASE + '/')) {
    return;
  }
  const path = (pathname.slice(PORTAL_BASE.length) || '/') + search;
  if (!isReturnable(path) || path === '/' || path === '/dashboard') return;
  try {
    localStorage.setItem(KEY, JSON.stringify({ path, at: Date.now() }));
  } catch {
    // Storage unavailable: the visitor lands on the dashboard, as before.
  }
}

/** The remembered page, once: it is forgotten as it is read. Null when there is none. */
export function takeReturnPath(): string | null {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
    localStorage.removeItem(KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  try {
    const { path, at } = JSON.parse(raw);
    if (typeof path !== 'string' || typeof at !== 'number') return null;
    if (Date.now() - at > MAX_AGE_MS || !isReturnable(path)) return null;
    return path;
  } catch {
    return null;
  }
}
