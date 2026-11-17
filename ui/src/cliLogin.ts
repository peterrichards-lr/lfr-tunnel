// `lfr-tunnel login` opens the portal with ?cli_login=1 (#2459). The CLI only receives a token when
// the user creates one here, and nothing used to say so: a signed-in user saw the dashboard and the
// terminal waited forever.
//
// Read once, when the app loads, because the router rewrites the address before the dashboard
// mounts -- a signed-out visitor is sent to /login, dropping the query. The marker is then carried
// through sign-in (the SSO link and the magic-link request) so a signed-out start ends at the
// prompt too. Same rule as Portal V1's cliLoginPending.
let pending =
  new URLSearchParams(window.location.search).get('cli_login') === '1';

/** The query suffix that carries the marker into a sign-in request, or "". */
export function cliLoginQuery(): string {
  return pending ? '&cli_login=1' : '';
}

/** True once, if the visit came from `lfr-tunnel login`; clears the marker from the address. */
export function consumeCLILogin(): boolean {
  if (!pending) return false;
  pending = false;
  const params = new URLSearchParams(window.location.search);
  if (params.has('cli_login')) {
    params.delete('cli_login');
    const qs = params.toString();
    window.history.replaceState(
      window.history.state,
      '',
      window.location.pathname + (qs ? '?' + qs : '') + window.location.hash,
    );
  }
  return true;
}
