import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The promo banner can be turned back on from Account Settings, in both portals (#1626).
 *
 * Dismissing it wrote its localStorage key with no expiry and nothing in the UI could clear it
 * again. That became load-bearing when #1622 removed the Classic Dashboard link from the V2
 * sidebar and left the banner as the only in-app route back to V1.
 *
 * Note the two portals use DIFFERENT keys -- V1's banner promotes V2 (`v2_promo_dismissed`) and
 * V2's promotes V1 (`v1_promo_dismissed`) -- so each arm is asserted against its own.
 */
const adminEmail = 'admin@lfr-demo.local';

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

// The native checkbox in a .toggle-switch is opacity:0 / width:0 / height:0 -- visually replaced
// by .toggle-slider. So it is never "visible" to Playwright and .check() cannot click it: the
// slider is the thing to click, while checked-ness is still read from the input.
async function flipToggle(page: any, inputSelector: string) {
  await page
    .locator(inputSelector)
    .locator('xpath=following-sibling::span[contains(@class,"toggle-slider")]')
    .click();
}

test.describe('Promo banner can be restored from Account Settings', () => {
  test('V2: dismiss, then switch it back on', async ({ page }) => {
    await loginV2(page);

    const banner = page.getByRole('link', { name: /Switch back to V1/i });
    await expect(banner).toBeVisible();

    await page.getByRole('button', { name: /Dismiss promo banner/i }).click();
    await expect(banner).toHaveCount(0);

    await page.goto('/portalv2/account');
    const toggle = page.locator('#portal-banner-toggle');
    // Positive anchor: the control has to exist and reflect the dismissal, or "turning it on"
    // proves nothing. Attached rather than visible -- see flipToggle above.
    await expect(toggle).toBeAttached();
    await expect(toggle).not.toBeChecked();

    await flipToggle(page, '#portal-banner-toggle');
    await expect(toggle).toBeChecked();
    await page.goto('/portalv2/dashboard');
    await expect(banner).toBeVisible();
  });

  test('V2: the preference survives a reload', async ({ page }) => {
    await loginV2(page);
    await page.getByRole('button', { name: /Dismiss promo banner/i }).click();
    await page.reload();
    // Without persistence this passes trivially on first load, so the reload is the point.
    await expect(
      page.getByRole('link', { name: /Switch back to V1/i }),
    ).toHaveCount(0);

    await page.goto('/portalv2/account');
    await flipToggle(page, '#portal-banner-toggle');
    await page.reload();
    await expect(page.locator('#portal-banner-toggle')).toBeChecked();
  });

  test('V1: dismiss, then switch it back on', async ({ page }) => {
    await loginV1(page);

    const banner = page.locator('#v2-promo-banner');
    await expect(banner).toBeVisible();

    await page.getByRole('button', { name: 'Dismiss' }).click();
    await expect(banner).toBeHidden();

    await page.locator('#nav-account').click();
    const toggle = page.locator('#acc-portal-banner');
    await expect(toggle).toBeAttached();
    await expect(toggle).not.toBeChecked();

    await flipToggle(page, '#acc-portal-banner');
    await expect(toggle).toBeChecked();
    // Applies immediately -- it is a per-browser display preference, not part of the account
    // record, so there is no Save step.
    await expect(banner).toBeVisible();
  });

  test('V1: the toggle reflects a dismissal made earlier', async ({ page }) => {
    await loginV1(page);
    await page.getByRole('button', { name: 'Dismiss' }).click();
    // Assert the dismissal took BEFORE navigating: otherwise a click that missed looks
    // identical to a preference that failed to persist.
    await expect(page.locator('#v2-promo-banner')).toBeHidden();

    // Navigate rather than reload -- the login URL carries a one-time token, and reloading it
    // re-runs the magic-link exchange instead of restoring the session.
    await page.goto('/admin');
    await expect(page.locator('#v2-promo-banner')).toBeHidden();

    await page.locator('#nav-account').click();
    // The control must show what is actually in effect rather than defaulting to on.
    await expect(page.locator('#acc-portal-banner')).not.toBeChecked();
  });
});
