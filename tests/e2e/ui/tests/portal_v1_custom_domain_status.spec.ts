import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V2, openV1, V1 } from './utils/portal';

/**
 * Portal V1: a user's OWN custom domain provisioning status (#2233).
 *
 * #2223/#2224 let a non-admin register a custom domain from V1. Nothing then told them whether
 * it provisioned: `GET /api/portal/vanity-domain-status` had exactly one caller in the tree,
 * V2's VanityDomainStatusPanel. V1's Custom Domains tab is NOT that view -- it reads
 * `/api/admin/vanity-domain-status` and is in ADMIN_ONLY_TABS, so a non-admin cannot open it at
 * all. The same person therefore saw their five provisioning stages or saw nothing, decided only
 * by which arm of the A/B test served them.
 *
 * Signed in as a NON-ADMIN throughout, because that is the whole defect. Every spec here used to
 * sign in as admin@lfr-demo.local, which is how #1512 shipped a page that rendered perfectly and
 * was unreachable for the people it was written for.
 *
 * check-portal-parity.cjs carried a capability entry for the same endpoint, and could not prove
 * this: it asserted source presence, not render-time reachability. #2241 shipped a control present
 * in both arms' source and unreachable in one with that gate green throughout. #2529 replaced it
 * with check-v2-capabilities.cjs, which does not read V1 at all. This file is the
 * half that watches the screen.
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

test.describe('Portal V1 custom domain status', () => {
  // The non-admin tests went with #2528: V1 serves admins only, and anyone else who opens it is
  // redirected to V2 (portal_v2_entry_point.spec.ts). The admin arm is what is left to watch.
  test('an admin still sees the admin table, and their own panel too', async ({
    page,
  }) => {
    // The admin arm must keep working: the per-user panel is on the Reservations section for
    // everyone, and #nav-custom-domains still opens the fleet-wide table for an admin.
    await loginV1(page, adminEmail);

    await page.locator('#nav-reservations').click();
    await expect(page.locator('#custom-domain-status-title')).toBeVisible();

    await page.locator('#nav-custom-domains').click();
    await expect(page.locator('#tab-custom-domains')).toBeVisible();
  });
});
