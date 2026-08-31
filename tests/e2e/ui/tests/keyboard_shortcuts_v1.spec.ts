import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * V1 keyboard shortcuts (#1611), matching the set V2 gained in #1613.
 *
 * The portals are an A/B test, so a shortcut that works in one arm and not the other is a
 * capability difference rather than a presentation one.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

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

test.describe('V1 keyboard shortcuts', () => {
  test('? opens the overlay and Escape closes it', async ({ page }) => {
    await loginV1(page);

    await page.keyboard.press('?');
    const overlay = page.locator('#shortcuts-overlay');
    await expect(overlay).toBeVisible();
    await expect(overlay).toContainText(/Keyboard shortcuts/i);

    await page.keyboard.press('Escape');
    await expect(overlay).toBeHidden();
  });

  test('the overlay is reachable without knowing a shortcut', async ({
    page,
  }) => {
    await loginV1(page);
    await page.locator('#shortcuts-trigger').click();
    await expect(page.locator('#shortcuts-overlay')).toBeVisible();
  });

  test('g then a letter navigates', async ({ page }) => {
    await loginV1(page);

    await page.keyboard.press('g');
    await page.keyboard.press('u');
    await expect(page.locator('#tab-users')).toBeVisible();
    await expect(page).toHaveURL(/\/admin\/users$/);
  });

  test('shortcuts are inert while typing', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-users');
    await expect(page.locator('#tab-users')).toBeVisible();

    // 'g d' deliberately, not 'g u': we are already on users, so an unguarded 'g u' would
    // navigate to where we already are and prove nothing. Same trap as the V2 spec.
    const search = page.locator('#users-table-body-search');
    await expect(search).toBeVisible();
    await search.click();
    await search.pressSequentially('gd');

    await expect(page.locator('#tab-users')).toBeVisible();
    await expect(search).toHaveValue('gd');
  });

  test('modifier combinations are left to the browser', async ({ page }) => {
    await loginV1(page);
    await page.keyboard.press('Control+g');
    await page.keyboard.press('u');
    // Ctrl+G must not have started a chord, so we should still be on the overview.
    await expect(page.locator('#tab-overview')).toBeVisible();
  });

  test('the overlay lists only destinations this role can reach', async ({
    page,
  }) => {
    // The nav item is hidden for roles that may not go there, so availability is read from the
    // sidebar rather than restating the role rules in two places.
    await loginV1(page);
    await page.keyboard.press('?');

    const overlay = page.locator('#shortcuts-overlay');
    await expect(overlay).toContainText('System Settings');
    await expect(overlay).toContainText('Database Backups');
    // And the sidebar keys from #1562 are documented here too.
    await expect(overlay).toContainText(/sidebar/i);
  });
});
