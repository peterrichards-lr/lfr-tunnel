import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V2, V1, openV1 } from './utils/portal';

/**
 * V1's promo banner can be turned back on from Account Settings (#1626).
 *
 * Dismissing it wrote its localStorage key with no expiry and nothing in the UI could clear it
 * again. That became load-bearing when #1622 removed the Classic Dashboard link from the V2
 * sidebar and left the banner as the only in-app route back to V1.
 *
 * V1's banner promotes V2 (`v2_promo_dismissed`). V2 no longer has a banner of its own, nor the
 * setting to restore it (#2528): admins reach V1 from the Admin Zone, and nobody else reaches it.
 */
const adminEmail = 'admin@lfr-demo.local';

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await openV1(page, token);
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
    await page.goto(V1);
    await expect(page.locator('#v2-promo-banner')).toBeHidden();

    await page.locator('#nav-account').click();
    // The control must show what is actually in effect rather than defaulting to on.
    await expect(page.locator('#acc-portal-banner')).not.toBeChecked();
  });
});
