import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Backups and Gateway Maintenance belong to their own pages, not to System Settings (#1599).
 *
 * V2's System Settings carried full copies of both, duplicating the dedicated pages added in
 * #1567 and #1568. That happened because the parity audit checked for a backups *route* and a
 * maintenance *route*, found neither, and treated that as proof the capability was missing -- it
 * was there, inside AdminSettings.tsx, which was never opened.
 *
 * V1 settles which copy is right: its System Settings contains neither, and it has both as
 * separate menu entries. So the dedicated pages are the structure both arms should share.
 *
 * Asserted as "exactly one place offers this", because a test that only checked the dedicated
 * pages still work would pass with the duplicate sitting right there.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

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

test.describe('V2 System Settings does not duplicate other pages', () => {
  test('Settings still renders its own sections', async ({ page }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/settings');

    // Positive anchor first: the page must actually be there. Every absence check below would
    // pass on a Settings page that failed to render at all.
    await expect(
      page.getByRole('heading', { name: /System Settings/i }),
    ).toBeVisible();
    await expect(page.getByText(/Domain Allocation/i).first()).toBeVisible();
  });

  test('Settings offers no backups or maintenance controls', async ({
    page,
  }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/settings');
    await expect(
      page.getByRole('heading', { name: /System Settings/i }),
    ).toBeVisible();

    // Scoped to the page content: the sidebar carries a "Database Backups" nav link on every
    // page, so an unscoped search finds it and reports duplication that is not there.
    const content = page.locator('main');

    for (const gone of [
      /Database Backups/i,
      /Trigger Backup/i,
      /Soft Maintenance/i,
      /Iron Curtain/i,
    ]) {
      await expect(
        content.getByText(gone),
        `Settings should no longer offer ${gone}`,
      ).toHaveCount(0);
    }
  });

  test('both capabilities are still reachable, on their own pages', async ({
    page,
  }) => {
    await loginV2(page);

    await page.goto('/portalv2/admin/backups');
    await expect(
      page.getByRole('heading', { name: /Database Backups/i }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: /Create Backup/i }),
    ).toBeVisible();

    await page.goto('/portalv2/admin/maintenance');
    await expect(
      page.getByRole('heading', { name: /Gateway Maintenance/i }),
    ).toBeVisible();
    await expect(page.locator('#btn-toggle-maint')).toBeVisible();
  });

  test('the webhook test target still shows, which shares that endpoint', async ({
    page,
  }) => {
    // Settings still calls /api/admin/maintenance, but only for test_target -- the webhook
    // destination shown beside the Integrations test button (#1290). Removing the maintenance
    // controls must not have taken that with it.
    await loginV2(page);
    await page.goto('/portalv2/admin/settings');
    await expect(
      page.getByRole('heading', { name: /System Settings/i }),
    ).toBeVisible();
    await expect(page.getByText(/Integrations/i).first()).toBeVisible();
  });
});
