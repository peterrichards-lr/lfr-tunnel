import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser } from './utils/nonadmin';

/**
 * A non-admin can see their own analytics in Portal V2 (#1512).
 *
 * V1's Analytics tab has always had two sections: an admin-gated one, and a "My Usage" one every
 * user sees. V2 mounted the whole page inside AdminRoute, so a non-admin reaching for analytics
 * got nothing at all -- the same person saw their own usage in V1 and a redirect in V2, which is
 * a difference between the arms of a live A/B test rather than a missing nicety.
 *
 * The page always rendered the personal half correctly. It was simply unreachable for the people
 * it was written for, and no test could have caught that because every other spec signs in as
 * admin@lfr-demo.local.
 *
 * Asserted against a real non-admin session rather than by reading the guards, per the issue's
 * acceptance criteria -- the point is what this user can actually reach.
 */
test.describe('Portal V2 analytics for a non-admin', () => {
  // Unique per run so a re-run against a warm database does not collide with its own leftovers,
  // but kept SHORT on purpose.
  //
  // The e2e database is shared by every spec, and portal_v2_table_scroll asserts that the Admin
  // Users table fits a 1280px viewport. A `nonadmin-<13-digit-timestamp>@lfr-demo.local` row is
  // twice the width of `admin@lfr-demo.local` and widens the email column for the spec that runs
  // after it -- which is exactly how this one broke that one in CI while passing locally, where
  // the font stack is narrower. Base36 keeps it comparable to the accounts already there.
  const email = `na-${Date.now().toString(36).slice(-5)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(email);
  });

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', email);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(email);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  test('reaches analytics and sees their own usage', async ({ page }) => {
    await page.goto('/portalv2/analytics');

    // Not bounced by AdminRoute -- the bug was a redirect away from here.
    await expect(page).toHaveURL(/\/portalv2\/analytics$/);

    // The title has to describe what is on screen: "System Analytics" would be naming a page
    // this user is not being shown.
    await expect(
      page.getByRole('heading', { name: /My Usage/i }),
    ).toBeVisible();
  });

  test('sees no admin-only analytics', async ({ page }) => {
    await page.goto('/portalv2/analytics');
    await expect(page).toHaveURL(/\/portalv2\/analytics$/);

    // Anchor on something that MUST be there first. Mutation testing caught this: with the
    // route removed the page renders nothing, and a test that only asserts absence passes
    // just as happily against a blank page as against a correct one.
    await expect(
      page.getByRole('heading', { name: /My Usage/i }),
    ).toBeVisible();

    // The admin half is one block guarded on `global`, which the API omits for a non-admin.
    // Checked by what renders, not by the guard: these are the headings that would appear if
    // the server ever started returning `global` to the wrong person.
    for (const heading of [
      /System Bandwidth/i,
      /Top Users/i,
      /Region Latency/i,
      /Client Versions/i,
    ]) {
      await expect(page.getByRole('heading', { name: heading })).toHaveCount(0);
    }
  });

  test('the sidebar offers analytics', async ({ page }) => {
    // Without this the page is reachable only by typing the URL, which is not "reachable".
    const link = page.getByRole('link', { name: /^Analytics$/i });
    await expect(link).toHaveCount(1);
    await link.click();
    await expect(page).toHaveURL(/\/portalv2\/analytics$/);
    // Arriving at the URL is not the same as arriving at the page: without a route matching
    // it, the address bar changes and nothing renders. Assert the destination exists.
    await expect(
      page.getByRole('heading', { name: /My Usage/i }),
    ).toBeVisible();
  });

  test('the admin route stays closed to them', async ({ page }) => {
    // Opening analytics up must not have opened the admin path with it: /admin/analytics
    // renders the same component, so it is the ROUTE guard that keeps it admin-only.
    await page.goto('/portalv2/admin/analytics');
    await expect(page).not.toHaveURL(/\/portalv2\/admin\/analytics$/);
  });
});
