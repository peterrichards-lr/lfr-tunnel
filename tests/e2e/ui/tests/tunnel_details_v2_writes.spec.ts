import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser } from './utils/nonadmin';

/**
 * V2's tunnel details can change a live tunnel, as V1's always could (#2525): add and remove a
 * custom header, override the rate limit (admin only), copy the URL and refresh.
 *
 * ## What is asserted
 *
 * The REQUESTS. The stack has no live tunnel, so the gateway's answer is stubbed and what is
 * checked is what the panel asked for. That is the part #2530 was about: a header edit must send
 * the change (set one, remove one), never a rebuilt map, because the panel holds header names
 * only and a rebuilt map wipes every value it cannot see.
 *
 * ## The controls
 *
 * - A two-port client: an edit goes to BOTH hosts. A panel that edited members[0] only would
 *   pass every single-port assertion.
 * - A non-admin sees no Override button, and an admin does -- the same panel, both ways.
 * - Bad rate-limit input sends nothing. A panel that posted whatever was typed would pass the
 *   happy path.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

const base = {
  status: 'up',
  node_id: 'edge-us',
  client_ip: '10.0.0.1',
  created_at: '2026-11-19T09:00:00Z',
  rate_limit: 25,
  user_id: 'u-writes',
  bytes_in: 0,
  bytes_out: 0,
  visitor_ips: [],
  passcode: '',
  whitelist_ips: '',
  access_mode: '',
  launch_flags: [],
  launch_overrides: {},
  added_header_names: ['X-Demo-Auth'],
};

// One client, two ports: two leases sharing a prefix and node, as Register produces them.
const TWO_PORT = [
  { ...base, subdomain_prefix: 'writes', full_host: 'writes.lfr-demo.local' },
  {
    ...base,
    subdomain_prefix: 'writes',
    full_host: 'writes-8443.lfr-demo.local',
  },
];

async function stubTunnels(page: any) {
  await page.route('**/api/me', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json();
    body.tunnels = TWO_PORT;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
}

// Records every POST to the two write endpoints and answers success.
async function recordWrites(page: any) {
  const sent: { url: string; body: any }[] = [];
  for (const path of [
    '**/api/portal/reservations/headers',
    '**/api/admin/leases/rate-limit',
  ]) {
    await page.route(path, async (route: any) => {
      sent.push({
        url: route.request().url(),
        body: route.request().postDataJSON(),
      });
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{"status":"success"}',
      });
    });
  }
  return sent;
}

async function loginV2(page: any, email: string) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

async function openDetails(page: any) {
  await stubTunnels(page);
  await page.reload();
  const row = page.locator('tr', { hasText: 'writes' }).first();
  await row.getByRole('button', { name: 'Details' }).click();
  const modal = page.locator('[role="dialog"]', { hasText: 'X-Demo-Auth' });
  await expect(modal).toBeVisible();
  return modal;
}

test.describe('V2 tunnel details can change a live tunnel', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('Add and Remove send only the change, to every port', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    const sent = await recordWrites(page);
    const modal = await openDetails(page);

    await modal.getByTestId('header-name-input').fill('X-New');
    await modal.getByTestId('header-value-input').fill('v2');
    await modal.locator('button:text-is("Add")').click();
    await expect.poll(() => sent.length).toBe(2);
    expect(sent.map((s) => s.body)).toEqual(
      expect.arrayContaining([
        {
          subdomain: 'writes',
          domain: 'lfr-demo.local',
          set: { 'X-New': 'v2' },
        },
        {
          subdomain: 'writes-8443',
          domain: 'lfr-demo.local',
          set: { 'X-New': 'v2' },
        },
      ]),
    );
    // Cleared once the gateway accepted it, so a second Add is not the same header again.
    await expect(modal.getByTestId('header-name-input')).toHaveValue('');

    await modal.locator('button[aria-label="Remove X-Demo-Auth"]').click();
    await expect.poll(() => sent.length).toBe(4);
    expect(sent.slice(2).map((s) => s.body)).toEqual(
      expect.arrayContaining([
        {
          subdomain: 'writes',
          domain: 'lfr-demo.local',
          remove: ['X-Demo-Auth'],
        },
        {
          subdomain: 'writes-8443',
          domain: 'lfr-demo.local',
          remove: ['X-Demo-Auth'],
        },
      ]),
    );
    // Never a whole map: that is the request that wiped every other header (#2530).
    for (const s of sent) expect(s.body).not.toHaveProperty('added_headers');
  });

  test('an admin can override the rate limit; bad input sends nothing', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    const sent = await recordWrites(page);
    const modal = await openDetails(page);
    await expect(modal.getByTestId('tunnel-rate-limit')).toHaveText('25 RPS');

    const prompt = page.locator('[role="dialog"]', {
      hasText: 'Live Rate Limit Override',
    });

    await modal.locator('button:text-is("Override")').click();
    await prompt.locator('input[type="text"]').fill('abc');
    await prompt.getByRole('button', { name: /Confirm/i }).click();
    await expect(
      page.getByText('Enter a whole number of requests per second').first(),
    ).toBeVisible();
    expect(sent).toHaveLength(0);

    await modal.locator('button:text-is("Override")').click();
    await prompt.locator('input[type="text"]').fill('7');
    await prompt.getByRole('button', { name: /Confirm/i }).click();
    await expect.poll(() => sent.length).toBe(2);
    expect(sent.map((s) => s.body)).toEqual(
      expect.arrayContaining([
        { host: 'writes.lfr-demo.local', rate_limit: 7 },
        { host: 'writes-8443.lfr-demo.local', rate_limit: 7 },
      ]),
    );
  });

  test('Copy URL and Refresh are there; Refresh re-reads /api/me', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    const modal = await openDetails(page);
    await expect(modal.locator('button:text-is("Copy URL")')).toBeVisible();

    // Layout polls /api/me every 10 seconds anyway, so "some /api/me happened" proves nothing.
    // The poll marks itself X-Background-Poll (#1676) and a deliberate re-read does not.
    const reread = page.waitForRequest(
      (r: any) =>
        r.url().endsWith('/api/me') &&
        r.method() === 'GET' &&
        !r.headers()['x-background-poll'],
      { timeout: 5000 },
    );
    await modal.locator('button:text-is("Refresh")').click();
    await reread;
  });

  test('a non-admin can edit headers but is not offered the override', async ({
    page,
  }) => {
    const email = `nonadmin-writes-${Date.now()}@lfr-demo.local`;
    await createApprovedUser(email);
    await loginV2(page, email);
    const modal = await openDetails(page);

    await expect(modal.getByTestId('tunnel-rate-limit')).toHaveText('25 RPS');
    await expect(modal.locator('button:text-is("Override")')).toHaveCount(0);
    await expect(modal.locator('button:text-is("Add")')).toBeVisible();
  });
});
