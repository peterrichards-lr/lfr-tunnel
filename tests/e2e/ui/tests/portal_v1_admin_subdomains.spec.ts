import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * V1 gained an admin-wide Registered Subdomains screen (#1619).
 *
 * It never had one: /api/portal/reservations is scoped to the caller by design and Active
 * Tunnels reads currentUser.tunnels, so an admin could see their own reservations and nobody
 * else's while V2 had had an admin-wide list all along.
 */
const adminEmail = 'admin@lfr-demo.local';

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  // The email field is hidden behind this button; filling it directly just times out.
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

// The reservation and its live lease come from two different endpoints and V1 has to merge
// them -- reading only /api/admin/subdomains yields a table where every row looks offline.
// Stubbed because the test stack holds no reservations at all, so a real fetch renders an
// empty table and every assertion below would pass vacuously.
async function stubAdminData(page: any, opts: { withLease: boolean }) {
  await page.route('**/api/admin/subdomains', async (route: any) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 1,
          user_id: 'u1',
          user_email: 'someone-else@lfr-demo.local',
          subdomain: 'demo',
          domain: 'lfr-demo.local',
          expires_at: '2026-12-25T09:30:00Z',
          created_at: '2026-08-11T15:39:37Z',
        },
      ]),
    });
  });
  await page.route('**/api/admin/leases', async (route: any) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(
        opts.withLease
          ? [
              {
                subdomain_prefix: 'demo',
                full_host: 'demo.lfr-demo.local',
                client_ip: '10.1.2.3',
                bytes_in: 2048,
                bytes_out: 4096,
                rate_limit: 0,
                node_id: 'edge-us',
              },
            ]
          : [],
      ),
    });
  });
}

test.describe('V1 admin-wide Registered Subdomains', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('the sidebar offers the section and it lists other users reservations', async ({
    page,
  }) => {
    await stubAdminData(page, { withLease: false });
    await loginV1(page);

    const navLink = page.locator('#nav-admin-subdomains');
    await expect(navLink).toBeVisible();
    await navLink.click();

    // Arriving at a URL is not arriving at a page, so assert the section is on screen.
    await expect(page.locator('#tab-admin-subdomains')).toBeVisible();
    // Either base is served (server.go routes both /portal/ and /admin/), and which one you
    // get depends on where you signed in, so both are accepted here.
    await expect(page).toHaveURL(/\/(portal|admin)\/admin-subdomains$/);

    // The whole point: a reservation belonging to somebody else.
    const row = page.locator('#admin-subdomains-table-body tr').first();
    await expect(row).toContainText('someone-else@lfr-demo.local');
    await expect(row).toContainText('demo.lfr-demo.local');
    // Expires is present here as it is in V2 (#1617).
    await expect(row).toContainText('2026-12-25');
  });

  test('live lease data is merged in, not left blank', async ({ page }) => {
    await stubAdminData(page, { withLease: true });
    await loginV1(page);
    await page.goto('/admin/admin-subdomains');
    await expect(page.locator('#tab-admin-subdomains')).toBeVisible();

    const row = page.locator('#admin-subdomains-table-body tr').first();
    // All of this comes from /api/admin/leases. Reading only /api/admin/subdomains would leave
    // the row permanently Offline with no IP, no node and zeroed counters.
    await expect(row).toContainText('Online');
    await expect(row).toContainText('10.1.2.3');
    await expect(row).toContainText('edge-us');
  });

  test('a reservation with no lease reads as offline', async ({ page }) => {
    await stubAdminData(page, { withLease: false });
    await loginV1(page);
    await page.goto('/admin/admin-subdomains');
    await expect(page.locator('#tab-admin-subdomains')).toBeVisible();

    const row = page.locator('#admin-subdomains-table-body tr').first();
    await expect(row).toContainText('Offline');
    // Anchored on the row having rendered at all, so "no Online text" cannot pass on a blank
    // table.
    await expect(row).toContainText('someone-else@lfr-demo.local');
  });
});
