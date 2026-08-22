import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 landmarks and heading structure (#1219).
 *
 * Screen reader users navigate by landmark region and by heading level. Before this,
 * neither strategy worked anywhere in V2: no nav, no main, no skip link, and every page's
 * top-level heading was an h3 with no h1 above it.
 */
test.describe('Portal V2 landmarks and headings', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    // V2's login differs from V1's: the email field is shown directly rather than behind
    // a #btn-show-email toggle, and the token is redeemed at /portalv2/login.
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('the page exposes navigation and main landmarks', async ({ page }) => {
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible();
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('a skip link is the first thing a keyboard user reaches', async ({ page }) => {
    // Hidden off-screen rather than display:none -- a display:none element cannot be
    // focused, so it would never reach the keyboard users it exists for.
    await page.keyboard.press('Tab');
    const skip = page.getByRole('link', { name: /skip to content/i });
    await expect(skip).toBeFocused();
  });

  test('each page has a single top-level h1', async ({ page }) => {
    // Every admin page used to start at h3 with no h1 above it, so heading navigation
    // had nothing to anchor on.
    for (const path of ['/portalv2/admin/users', '/portalv2/admin/blacklist', '/portalv2/admin/tokens']) {
      await page.goto(path);
      await expect(page.locator('h1')).toHaveCount(1);
      await expect(page.locator('h1')).toBeVisible();
    }
  });
});
