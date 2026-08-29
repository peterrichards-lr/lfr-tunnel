import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * V1 personal analytics for non-admins (#1561).
 *
 * V1's Analytics link lived inside `admin-sidebar-group`, hidden wholesale from non-admins, and
 * 'analytics' was additionally listed in ADMIN_ONLY_TABS -- so a non-admin could neither see the
 * link nor route to the tab. Their own bandwidth and tunnel charts are rendered on that tab, so
 * they were unreachable, while V2 has shown them since #1512.
 *
 * The page needed no gating of its own: the admin half is keyed off `data.global`, which
 * /api/analytics returns only to an admin. So the fix is reachability, and these tests check
 * both that a non-admin gets in and that getting in does not hand them the admin view.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV1(page: any, email: string) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('Portal V1 personal analytics', () => {
  // One shared non-admin, created once and removed afterwards. Leaving fixture users behind
  // widens the Admin Users email column for portal_v2_table_scroll, which runs later against
  // this same database and only fails in CI, where the Linux font stack is wider than macOS's
  // (#1525). A short local part is not sufficient on its own -- the row has to go.
  const nonAdminEmail = `pa${Date.now().toString().slice(-6)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await createApprovedUser(nonAdminEmail);
  });

  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('a non-admin can reach Analytics and sees their own usage', async ({
    page,
  }) => {
    await loginV1(page, nonAdminEmail);

    const link = page.locator('#nav-analytics-personal');
    await expect(link).toBeVisible();
    await link.click();

    await expect(page.locator('#tab-analytics')).toBeVisible();

    // Positive anchor: the personal chart canvas must actually be present. Asserting only that
    // the admin section is hidden would pass on a tab that rendered nothing at all.
    await expect(page.locator('#myBandwidthChart')).toBeAttached();

    // ...and only then the absence: the global/admin block stays hidden, because the API does
    // not return `data.global` to a non-admin.
    await expect(page.locator('#admin-analytics-section')).toBeHidden();
  });

  test('an admin keeps one Analytics entry, under Reporting', async ({
    page,
  }) => {
    await loginV1(page, adminEmail);

    // Two links pointing at one tab would be the obvious way to get this wrong.
    await expect(page.locator('#nav-analytics-personal')).toBeHidden();
    await expect(page.locator('#nav-analytics')).toBeVisible();

    await page.click('#nav-analytics');
    await expect(page.locator('#tab-analytics')).toBeVisible();
    await expect(page.locator('#admin-analytics-section')).toBeVisible();
  });

  test('the admin-only tabs are still refused to a non-admin', async ({
    page,
  }) => {
    await loginV1(page, nonAdminEmail);

    // Removing 'analytics' from ADMIN_ONLY_TABS must not have opened the rest of that list.
    await page.goto('/portal/users');
    await expect(page.locator('#tab-users')).toBeHidden();

    await page.goto('/portal/audit');
    await expect(page.locator('#tab-audit')).toBeHidden();
  });
});
