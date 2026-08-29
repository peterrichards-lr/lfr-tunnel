import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The status-page link, in both portals (#1559).
 *
 * V1's footer has always linked out to the status page; V2's had no equivalent. V1 also carried a
 * hardcoded status.lfr-demo.se as its href, so a deployment that configured nothing still
 * advertised someone else's status page.
 *
 * Both now render it only when `status_page_url` is configured. The test stack leaves it empty,
 * which makes the absent case the natural one to assert and the configured case one to stub.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml
const STATUS_URL = 'https://status.example.test';

// Torn down in afterEach: a route handler that outlives its test throws "route.fetch: Test
// ended" when a later navigation triggers it.
async function stubStatusUrl(page: any, url: string) {
  await page.route('**/api/version', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json();
    await route.fulfill({
      response: res,
      body: JSON.stringify({ ...body, status_page_url: url }),
    });
  });
}

test.describe('Status page link parity', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('V2 shows it when configured, and links where told', async ({
    page,
  }) => {
    await stubStatusUrl(page, STATUS_URL);
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    const link = page.getByRole('link', { name: /system status/i });
    await expect(link).toBeAttached();
    await expect(link).toHaveAttribute('href', STATUS_URL);
    // Opening a third-party site from an authenticated portal without noopener leaks a handle
    // back to this window.
    await expect(link).toHaveAttribute('rel', /noopener/);
  });

  test('V2 omits it when the deployment configures none', async ({ page }) => {
    // The test stack leaves status_page_url empty, so this is the unstubbed behaviour.
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    // Positive anchor first: the footer must actually be present, or this passes on a page that
    // rendered nothing.
    await expect(
      page.getByRole('link', { name: /privacy policy/i }),
    ).toBeAttached();
    await expect(
      page.getByRole('link', { name: /system status/i }),
    ).toHaveCount(0);
  });

  test('V1 hides it too when unconfigured, rather than linking somewhere hardcoded', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    // Anchored on a footer sibling that must be present regardless. Asserted as attached rather
    // than visible: V1's footer sits inside a position:fixed sidebar, below the fold, so
    // viewport visibility says nothing useful about whether the markup is right.
    await expect(page.locator('#footer-privacy-link')).toBeAttached();

    const hidden = await page
      .locator('#status-page-link')
      .evaluate((el: HTMLElement) => el.hidden);
    expect(hidden, 'an unconfigured status link should be hidden').toBe(true);
  });

  test('V1 shows it when configured', async ({ page }) => {
    await stubStatusUrl(page, STATUS_URL);
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    const link = page.locator('#status-page-link');
    await expect(link).toBeAttached();
    const hidden = await link.evaluate((el: HTMLElement) => el.hidden);
    expect(hidden, 'a configured status link should not be hidden').toBe(false);
    await expect(link).toHaveAttribute('href', STATUS_URL);
  });
});
