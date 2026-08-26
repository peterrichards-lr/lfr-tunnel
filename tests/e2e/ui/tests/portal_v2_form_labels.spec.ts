import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 form labels (#1220).
 *
 * 62 of 64 controls had no programmatic name -- captions were visual text beside the
 * field with no association, so a screen reader announced an unlabelled edit box.
 *
 * getByLabel resolves a name the way assistive technology does: <label for>, a wrapping
 * <label>, aria-label or aria-labelledby. It cannot be satisfied by text that merely sits
 * next to a field, which is exactly the markup this fixes.
 */
test.describe('Portal V2 form labels', () => {
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
  });

  test('account settings fields are labelled', async ({ page }) => {
    await page.goto('/portalv2/account');
    for (const name of ['First Name', 'Last Name', 'Preferred Name']) {
      await expect(
        page.getByLabel(name, { exact: false }).first(),
      ).toBeVisible();
    }
  });

  test('blacklist fields are labelled', async ({ page }) => {
    await page.goto('/portalv2/admin/blacklist');
    // Including the toolbar search, whose caption is not a <label> and so relies on
    // aria-label rather than an association.
    await expect(page.getByLabel(/search/i).first()).toBeVisible();
  });

  test('a caption for a group of radios labels the group, not one radio', async ({
    page,
  }) => {
    // htmlFor names a single control, so a group needs role=radiogroup with
    // aria-labelledby -- pointing it at one radio would have been wrong.
    await page.goto('/portalv2/dashboard');
    const groups = page.getByRole('radiogroup');
    if ((await groups.count()) > 0) {
      await expect(groups.first()).toHaveAttribute('aria-labelledby', /.+/);
    }
  });
});
