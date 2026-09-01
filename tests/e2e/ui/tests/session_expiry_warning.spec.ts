import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The portal warns before the session ends, in both arms (#1656).
 *
 * This could not have been built honestly before #1655: the server slid the session's expiry but
 * never re-issued the cookie, so the browser's copy died 24h after login whatever the user did.
 * A countdown against the server's expiry would have shown time that did not exist.
 *
 * `session_expires_at` is stubbed rather than waited for -- a real 24h session cannot be tested
 * by waiting, and the behaviour under test is what the UI does with the value, not how the
 * server computes it. The server side is covered by a Go test.
 */
const adminEmail = 'admin@lfr-demo.local';

/** Rewrites /api/me so the session appears to end in `minutes`. */
async function stubExpiry(page: any, minutes: number | null) {
  await page.route('**/api/me', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json().catch(() => ({}));
    if (minutes === null) {
      delete body.session_expires_at;
    } else {
      body.session_expires_at = new Date(
        Date.now() + minutes * 60000,
      ).toISOString();
    }
    await route.fulfill({
      response: res,
      body: JSON.stringify(body),
      headers: { ...res.headers(), 'content-type': 'application/json' },
    });
  });
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

test.describe('Session expiry warning', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('V2 warns when the session is nearly over', async ({ page }) => {
    await stubExpiry(page, 5);
    await loginV2(page);

    const banner = page.locator('.session-expiry-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText(/minute/i);
    await expect(
      banner.getByRole('button', { name: /Stay signed in/i }),
    ).toBeVisible();
  });

  test('V2 stays quiet on a fresh session', async ({ page }) => {
    await stubExpiry(page, 600);
    await loginV2(page);

    // Anchored on the portal having rendered, so "no banner" cannot pass on a blank page.
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.locator('.session-expiry-banner')).toHaveCount(0);
  });

  test('V1 warns when the session is nearly over', async ({ page }) => {
    await stubExpiry(page, 5);
    await loginV1(page);

    const banner = page.locator('#session-expiry-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText(/minute/i);
  });

  test('V1 stays quiet on a fresh session', async ({ page }) => {
    await stubExpiry(page, 600);
    await loginV1(page);

    await expect(page.locator('#nav-overview')).toBeVisible();
    await expect(page.locator('#session-expiry-banner')).toBeHidden();
  });

  test('Stay signed in makes a real request, not just a dismissal', async ({
    page,
  }) => {
    await stubExpiry(page, 5);
    await loginV2(page);

    const banner = page.locator('.session-expiry-banner');
    await expect(banner).toBeVisible();

    // The point of the button: every request slides the session server-side (#1655), so it has
    // to actually call the server. A button that only hid the banner would leave the session
    // expiring on its original schedule while telling the user it would not.
    let called = false;
    page.on('request', (req: any) => {
      if (req.url().includes('/api/me') && req.method() === 'GET')
        called = true;
    });

    await banner.getByRole('button', { name: /Stay signed in/i }).click();
    await expect.poll(() => called).toBe(true);
  });
});
