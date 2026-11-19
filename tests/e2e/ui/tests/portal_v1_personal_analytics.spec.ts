import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V2, openV1, V1 } from './utils/portal';

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
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await openV1(page, token);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('Portal V1 personal analytics', () => {
  // The non-admin tests went with #2528: V1 serves admins only, and anyone else who opens it is
  // redirected to V2 (portal_v2_entry_point.spec.ts). The admin arm is what is left to watch.
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
});
