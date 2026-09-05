import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 must hand a freshly minted token to a waiting `lfr-tunnel login` (#1741).
 *
 * V1 has always POSTed the raw token to the CLI's loopback listener
 * (`pkg/server/static/dashboard.js`, `generateToken`) and reported the outcome in
 * `#handoff-alert`. V2 created the token and stopped, so the same `lfr-tunnel login`
 * auto-configured or silently demanded a copy-paste depending purely on which arm of the
 * A/B test the gateway served. Nothing told the user which had happened.
 *
 * Three things make this worth asserting in a browser rather than a unit test:
 *
 *  - `mode: 'no-cors'` is a browser concept. The POST is opaque and unreadable by design, so
 *    the only honest signal is "the fetch did not reject", and only a real network stack
 *    produces the reject.
 *  - The port is fixed at 4444 and is half of a contract with already-deployed gateways
 *    (`handoffPort` in `pkg/client/login.go`). Asserting the literal URL is asserting the
 *    contract; a test that accepted any port would pass against a client nothing can reach.
 *  - The failure path is the one that matters most. A handoff that fails must still leave
 *    the token on screen with an explanation, which is a rendering claim.
 *
 * Every run routes 4444 explicitly rather than letting the request hit the host. 4444 is a
 * popular port -- Selenium Grid's default, plus assorted debuggers -- so an unrouted test
 * would pass or fail on whatever happens to be listening on the machine running it.
 */

const HANDOFF_URL = 'http://127.0.0.1:4444/handoff';
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// Short, and revoked afterwards. The database is shared across the whole suite in file
// order, so a fixture that accumulates rows is a neighbour's layout bug (see the
// e2e-testing skill, "the database is shared").
const tokenName = 'ho-test';

test.describe('Portal V2 CLI magic handoff', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  test.afterEach(async ({ page }) => {
    // Revoke through the API with the session this test already holds. `page.request`
    // shares the browser context's cookies, so no second login is needed.
    const res = await page.request.get('/api/tokens');
    if (!res.ok()) return;
    for (const t of ((await res.json()) as any[]) || []) {
      if (t.name === tokenName) {
        await page.request.delete(`/api/tokens/${t.id}`);
      }
    }
  });

  async function openGenerateTokenModal(page: any) {
    await page
      .getByRole('button', { name: /Generate (New )?Token/i })
      .first()
      .click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel(/Token Name/i).fill(tokenName);
    return dialog;
  }

  test('POSTs the raw token to the CLI listener and reports delivery', async ({
    page,
  }) => {
    // The route is held open until `release` resolves, so the in-flight state is observable
    // deterministically instead of being a race against a loopback round trip.
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });

    const seen: { method: string; body: string | null }[] = [];
    await page.route(HANDOFF_URL, async (route) => {
      seen.push({
        method: route.request().method(),
        body: route.request().postData(),
      });
      await held;
      await route.fulfill({ status: 200, body: '' });
    });

    const dialog = await openGenerateTokenModal(page);
    await dialog.getByRole('button', { name: /^Generate$/i }).click();

    const banner = page.getByTestId('cli-handoff-status');
    const tokenField = dialog.locator('input[readonly]');

    // While the POST is in flight the user is told so -- and, more importantly, the token is
    // already on screen. V1 renders the result step before firing the handoff for exactly
    // this reason: a hung CLI must not hide the thing the user came for.
    await expect(banner).toHaveText(/Attempting to send token to CLI/i);
    await expect(tokenField).toBeVisible();
    const shownToken = await tokenField.inputValue();
    expect(shownToken.length).toBeGreaterThan(0);

    release();

    await expect(banner).toHaveText(/successfully delivered to your CLI/i);
    // Still copyable after a successful handoff: "delivered" is inferred from an opaque
    // response, so it can be wrong, and the manual route must survive being told otherwise.
    await expect(tokenField).toHaveValue(shownToken);

    // The contract with the CLI, asserted rather than assumed: one POST, to that exact
    // address, carrying the raw token as the body. `pkg/client/login.go` reads the body and
    // trims it, so anything else -- a JSON wrapper, the token id, an empty body -- is a
    // handoff the CLI answers with 400.
    expect(seen).toHaveLength(1);
    expect(seen[0].method).toBe('POST');
    expect(seen[0].body).toBe(shownToken);
  });

  test('a refused connection still shows the token, with the manual-copy fallback', async ({
    page,
  }) => {
    let attempted = 0;
    await page.route(HANDOFF_URL, async (route) => {
      attempted += 1;
      // What a machine with no `lfr-tunnel login` waiting actually does.
      await route.abort('connectionrefused');
    });

    const dialog = await openGenerateTokenModal(page);
    await dialog.getByRole('button', { name: /^Generate$/i }).click();

    const banner = page.getByTestId('cli-handoff-status');
    const tokenField = dialog.locator('input[readonly]');

    // Positive anchor before the fallback claim: an empty modal would satisfy "the token was
    // not lost" perfectly well by rendering nothing at all.
    await expect(tokenField).toBeVisible();
    await expect(tokenField).not.toHaveValue('');

    await expect(banner).toHaveText(/lfr-tunnel login/);
    await expect(banner).toHaveText(/manually copy your token below/i);
    await expect(banner).toHaveClass(/alert-banner--warning/);

    // The banner tells the user to copy the token, so the means of doing so has to be
    // present alongside it -- "offers nothing" is the state this whole path exists to avoid.
    await expect(dialog.getByRole('button', { name: /^Copy$/i })).toBeEnabled();

    expect(attempted).toBe(1);
  });
});
