import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The tunnel details view, in both portal arms (#2150).
 *
 * V1 has carried a Details modal throughout. V2 had a table of eight columns and no per-tunnel
 * view at all, so a V2 user could not see a tunnel's access control, its current visitors,
 * whether anything was being injected into its requests, or how the client was launched. The
 * two portals are an A/B test and the same person may be served either arm, so that is a
 * defect rather than a roadmap item -- and it survived because each arm was only ever compared
 * against itself. This asserts the same four capabilities in both.
 *
 * ## Why the payload is stubbed
 *
 * The test stack has no live tunnels, and every interesting field here (passcode, whitelist,
 * visitor IPs, header names, launch context) would be empty against real data -- so a panel
 * that rendered nothing at all would look identical to one that rendered correctly. The
 * fixture gives each field a distinct value for that reason.
 *
 * ## The control
 *
 * Two tunnels, not one. `locked` is passcode-protected, whitelisted, has a custom header and
 * reports launch context; `open` has none of it. Every positive assertion below is paired with
 * the other tunnel showing the opposite, so a panel that hardcoded "Enabled" or simply printed
 * its fixture would fail. Asserting one tunnel would pass against a component that ignored its
 * props entirely.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// The header VALUE, which must never reach the browser. A custom header's value is chosen by
// the user and is routinely a credential; this payload goes to every admin, not only the
// tunnel's owner. It is here so the test can assert its absence -- the same class of leak
// #2137 removed from this feed.
const SECRET_HEADER_VALUE = 'Bearer must-not-be-rendered';

const LOCKED = {
  subdomain_prefix: 'locked',
  full_host: 'locked.lfr-demo.local',
  status: 'up',
  node_id: 'edge-us',
  client_ip: '10.0.0.1',
  created_at: '2026-09-22T09:00:00Z',
  rate_limit: 25,
  user_id: 'u-locked',
  bytes_in: 4096,
  bytes_out: 2048,
  visitor_ips: ['9.9.9.9'],
  passcode: '********', // The mask the server sends; never the stored hash.
  whitelist_ips: '203.0.113.0/24',
  access_mode: 'and',
  launch_flags: ['-subdomain', '-region'],
  launch_overrides: { region: 'LFR_TUNNEL_REGION' },
  added_header_names: ['X-Demo-Auth'],
};

const OPEN = {
  subdomain_prefix: 'openone',
  full_host: 'openone.lfr-demo.local',
  status: 'up',
  node_id: 'control',
  client_ip: '10.0.0.2',
  created_at: '2026-09-22T09:05:00Z',
  rate_limit: 0,
  user_id: 'u-open',
  bytes_in: 0,
  bytes_out: 0,
  visitor_ips: [],
  passcode: '',
  whitelist_ips: '',
  access_mode: '',
  launch_flags: [],
  launch_overrides: {},
  added_header_names: [],
};

// V1 replaces `currentUser` wholesale on every telemetry frame -- loadTunnels says so in as
// many words: "Already fetched in /api/me, then replaced by every telemetry frame". So a
// fixture served from /api/me survives only until the socket's first push, a second or two
// later, and the row under test disappears mid-test. Held open and never connected to the
// server, so no frame ever arrives.
//
// Only V1 needs this. V2's dashboard reads /api/me and nothing else; its telemetry screen is
// the one with a socket, and that is not what these exercise.
async function silenceTelemetrySocket(page: any) {
  await page.routeWebSocket('**/api/portal/telemetry/ws', () => {});
}

// The real /api/me with the tunnels replaced, rather than a synthetic user. The portal reads
// role, status, policy consent and half a dozen other fields off this response, so a
// hand-written object fails for reasons that have nothing to do with what is under test.
async function stubTunnels(page: any) {
  await page.route('**/api/me', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json();
    body.tunnels = [LOCKED, OPEN];
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
}

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

test.describe('Tunnel details reach parity across both portal arms', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('V2 has a per-tunnel details view at all', async ({ page }) => {
    await loginV2(page);
    await stubTunnels(page);
    await page.reload();

    const row = page.locator('tr', { hasText: 'locked' }).first();
    await row.getByRole('button', { name: 'Details' }).click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible();

    // Access control -- absent from V2 entirely before this.
    await expect(modal).toContainText('Enabled');
    await expect(modal).toContainText('203.0.113.0/24');
    // The combinator only means anything when both rules exist, and here both do.
    await expect(modal).toContainText('AND');

    // Visitor IPs -- also absent from V2 entirely.
    await expect(modal).toContainText('9.9.9.9');

    // Custom headers: the NAME is shown and the VALUE is not.
    await expect(modal).toContainText('X-Demo-Auth');
    await expect(modal).not.toContainText(SECRET_HEADER_VALUE);

    // Launch provenance (#2148) -- the reason this work started.
    await expect(modal).toContainText('-subdomain');
    await expect(modal).toContainText('LFR_TUNNEL_REGION');
  });

  test('V2 shows an unprotected tunnel as unprotected', async ({ page }) => {
    // The control. Every assertion above would also pass against a panel that printed those
    // strings unconditionally; this is the same panel on a tunnel that has none of them.
    await loginV2(page);
    await stubTunnels(page);
    await page.reload();

    const row = page.locator('tr', { hasText: 'openone' }).first();
    await row.getByRole('button', { name: 'Details' }).click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible();

    await expect(modal).toContainText('Disabled (Public)');
    await expect(modal).not.toContainText('203.0.113.0/24');
    await expect(modal).not.toContainText('9.9.9.9');
    await expect(modal).not.toContainText('X-Demo-Auth');
    // A client older than #2148 and a client started with no flags are both "not reported",
    // and neither is an error.
    await expect(modal).toContainText('Not reported by this client.');
  });

  test('V1 shows the launch context and the header name', async ({ page }) => {
    await loginV1(page);
    await silenceTelemetrySocket(page);
    await stubTunnels(page);
    await page.reload();

    // Active Tunnels is its own screen in V1, not part of Dashboard Overview. Without this the
    // row is in the DOM and its menu button is invisible, which reads as a broken locator and
    // is really a missing navigation -- the failure said "element is not visible", not "not
    // found", and that distinction is the whole diagnosis.
    await page.locator('#nav-tunnels').click();
    await expect(page.locator('#tab-tunnels')).toBeVisible();

    const row = page.locator('#tunnels-table-body tr', { hasText: 'locked' });
    await row.locator('.action-menu-btn').click();
    await row.getByRole('button', { name: 'Details' }).click();

    const modal = page.locator('#tunnel-details-modal');
    await expect(modal).toBeVisible();

    // The panel V1 gained here.
    await expect(
      modal.locator('#detail-tunnel-launch-container'),
    ).toContainText('-subdomain');
    await expect(
      modal.locator('#detail-tunnel-launch-container'),
    ).toContainText('LFR_TUNNEL_REGION');

    // The panel V1 already had, which had never had data: it read `added_headers`, a key
    // nothing has ever set on this payload, so it always rendered its empty state -- and an
    // empty state and a broken read are the same pixels.
    const headers = modal.locator('#detail-tunnel-headers-container');
    await expect(headers).toContainText('X-Demo-Auth');
    await expect(headers).not.toContainText(SECRET_HEADER_VALUE);
  });
});
