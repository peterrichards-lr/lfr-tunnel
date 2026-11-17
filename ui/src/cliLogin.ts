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

// Set once the marker has opened the dialog on this page load. A forced first MFA enrolment
// (force_mfa) gates every request, so no token can have been created yet when the gate finishes;
// its reload must bring the marker back or the terminal waits forever. V1 never consumes the
// marker before the MFA intercept, so it needs no equivalent.
let consumed = false;

/** True if this page load came from `lfr-tunnel login`, before or after the dialog opened. */
export function arrivedFromCLILogin(): boolean {
  return pending || consumed;
}

/** The query suffix that carries the marker into a sign-in request, or "". */
export function cliLoginQuery(): string {
  return pending ? '&cli_login=1' : '';
}

/** True once, if the visit came from `lfr-tunnel login`; clears the marker from the address. */
export function consumeCLILogin(): boolean {
  if (!pending) return false;
  pending = false;
  consumed = true;
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

/** Reloads the page, restoring the marker if it was consumed on this load (see `consumed`). */
export function reloadKeepingCLILogin(): void {
  if (!consumed) {
    window.location.reload();
    return;
  }
  const params = new URLSearchParams(window.location.search);
  params.set('cli_login', '1');
  window.location.assign(
    window.location.pathname + '?' + params.toString() + window.location.hash,
  );
}
