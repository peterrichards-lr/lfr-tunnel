import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Anonymous geographic distribution, both portals (#1152).
 *
 * The E2E stack ships no MaxMind database, and never will -- it is a ~60MB licensed
 * artefact. So what runs here is the `available: false` path, which is not a degraded
 * case to be tolerated but the state most deployments are in permanently, and the one an
 * admin is most likely to see.
 *
 * That makes the assertion worth having: an empty panel must SAY it is switched off
 * rather than looking like a working panel with no users in it. Those two states are
 * indistinguishable in the data and mean completely different things.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

test.describe('Geographic distribution — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
  });

  test('the panel is present and explains that it is switched off', async ({
    page,
  }) => {
    const panel = page
      .locator('.card')
      .filter({ hasText: 'Geographic Distribution' });
    await expect(panel).toHaveCount(1);
    // Names the setting, so an operator who wants it knows what to set. If the field is
    // ever renamed, this fails rather than leaving the UI pointing at a dead setting --
    // which it did: the first version of this panel said `geoip_database_path`, which
    // has never existed.
    await expect(panel).toContainText('geolite2_db_path');
  });

  test('it does not claim there are no users when it simply is not running', async ({
    page,
  }) => {
    const panel = page
      .locator('.card')
      .filter({ hasText: 'Geographic Distribution' });
    // The below-threshold copy is the OTHER empty state and must not be shown here.
    await expect(panel).not.toContainText('enough distinct users');
  });
});

test.describe('Geographic distribution — Portal V1', () => {
  test('the panel is present and explains that it is switched off', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    await page.click('#nav-analytics');
    const headline = page.locator('#geo-distribution-headline');
    await expect(headline).toBeVisible();
    await expect(headline).toContainText('geolite2_db_path');

    // The raw key, which is what t() falls back to when a translation is missing. V1 calls
    // t() with no default here, so an unadded key renders as "geo_unavailable" to the user.
    await expect(headline).not.toContainText('geo_unavailable');
  });
});

test.describe('Geographic distribution — access', () => {
  // Short on purpose, and cleaned up afterwards: the e2e database is shared and specs run in
  // file order, so a long-lived row here widens the Admin Users table for every later spec
  // (the collision documented in portal_v2_nonadmin_analytics).
  const email = `ng-${Date.now().toString(36).slice(-5)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(email);
  });

  test.afterAll(async () => {
    await deleteUser(email);
  });

  test('the endpoint is not open to an unauthenticated caller', async ({
    request,
  }) => {
    const res = await request.get('/api/admin/analytics/locations');
    expect(res.status()).not.toBe(200);
  });

  test('a non-admin sees no geographic panel', async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', email);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(email);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    await page.goto('/portalv2/analytics');
    // Not merely hidden: #1512 was a non-admin reaching admin analytics at all, and this
    // panel reports where a deployment's users are.
    await expect(
      page.locator('.card').filter({ hasText: 'Geographic Distribution' }),
    ).toHaveCount(0);
  });
});
