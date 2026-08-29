import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser } from './utils/nonadmin';

/**
 * Database Backups, in both portals (#1567).
 *
 * V2 had no backups screen at all, and V1's exposed only the list — while the API has offered
 * three operations all along: list, `POST /api/admin/backups` to trigger one, and
 * `GET /api/admin/backups/download/{name}` to fetch one.
 *
 * Parity is therefore taken from the API rather than from the lower of the two portals: both
 * arms get all three. The portals are a live A/B test, so a capability in one arm only would
 * make the comparison measure the capability rather than the presentation.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

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

test.describe('Database Backups parity', () => {
  test('V2 offers list, create and download', async ({ page }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/backups');

    // Positive anchor before any absence or capability check: an unrendered page would
    // otherwise satisfy several of the assertions below by rendering nothing.
    await expect(
      page.getByRole('heading', { name: /Database Backups/i }),
    ).toBeVisible();

    await expect(
      page.getByRole('button', { name: /Create Backup/i }),
    ).toBeVisible();

    // The restore procedure is documented nowhere else in the UI.
    await expect(page.getByText(/restore-with-maintenance\.sh/)).toBeVisible();

    await page.getByRole('button', { name: /Create Backup/i }).click();

    // After creating one there is a row, so the download affordance can be asserted for real
    // rather than on an empty table.
    const download = page.getByRole('link', { name: /Download/i }).first();
    await expect(download).toBeVisible();
    await expect(download).toHaveAttribute(
      'href',
      /\/api\/admin\/backups\/download\//,
    );
  });

  test('V1 offers the same three operations', async ({ page }) => {
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

    await page.click('#nav-backups');
    await expect(page.locator('#tab-backups')).toBeVisible();
    await expect(page.locator('#btn-create-backup')).toBeVisible();

    await page.locator('#btn-create-backup').click();

    const row = page.locator('#backups-table-body tr').first();
    await expect(row).toBeVisible();
    await expect(
      row.locator('a[href^="/api/admin/backups/download/"]'),
    ).toBeVisible();
  });

  test('a non-admin cannot reach the V2 backups route', async ({ page }) => {
    // Short local part on purpose: a long address widens the Admin Users table and breaks
    // portal_v2_table_scroll, which runs after this file alphabetically against the same
    // database. That failure only reproduces in CI, where the font stack is wider.
    const email = `nb${Date.now().toString().slice(-6)}@lfr-demo.local`;
    await createApprovedUser(email);

    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', email);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(email);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    await page.goto('/portalv2/admin/backups');

    // Anchored positively: assert we landed somewhere real, then that it is not this screen.
    // Without the first assertion a blank page would satisfy the second perfectly.
    await expect(page.locator('#root')).not.toBeEmpty();
    await expect(
      page.getByRole('button', { name: /Create Backup/i }),
    ).toHaveCount(0);
  });
});
