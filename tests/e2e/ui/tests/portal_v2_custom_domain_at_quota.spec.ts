import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * The custom-domain register control must stay VISIBLE at the quota (#2241).
 *
 * V2 rendered it under `{!isAtCustomDomainLimit && (...)}`, mirroring the subdomain form. That is
 * right for subdomains, where the quota is several. The custom-domain quota defaults to ONE, so
 * the same pattern hid the control from everyone who had ever used the feature -- and an absent
 * control is indistinguishable from one that was never built. Reported from production on the day
 * v1.49.0 shipped as "I can see it on V1 but not V2".
 *
 * V1 disables the inputs and shows a warning. This asserts V2 now does the same.
 *
 * Why an e2e spec rather than a source check: `check-portal-parity` asserts a capability is
 * present in the SOURCE of both arms, which it was. It cannot see that one arm renders it
 * conditionally on something the other does not, which is precisely this defect. The only level
 * that distinguishes "present" from "reachable" is the rendered page.
 */
const nonAdminEmail = `cq${Date.now().toString().slice(-6)}@lfr-demo.local`;

async function loginV2(page: any, email: string) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

test.describe('V2 custom-domain control at the quota', () => {
  // Removed afterwards: a fixture left behind widens the Admin Users email column for
  // portal_v2_table_scroll, which runs later against this same database (#1833, §4).
  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('the register control is present and usable below the quota, and present but disabled at it', async ({
    page,
  }) => {
    await createApprovedUser(nonAdminEmail);
    await loginV2(page, nonAdminEmail);

    // POSITIVE ANCHOR FIRST (§3): an absence check alone passes on a page that rendered nothing,
    // and "the control is missing" is exactly what this spec exists to tell apart from "the page
    // is blank". Everything below is asserted only once the panel itself is on screen.
    const quota = page.locator('text=Custom Domain Quota');
    await expect(quota).toBeVisible();

    const input = page.getByLabel('Custom domain to register');
    const submit = page.getByRole('button', { name: 'Register Domain' });

    // Below the quota: present AND usable. "Present" alone would pass on the disabled state too,
    // so the enabled-ness is asserted in both directions rather than only at the limit.
    await expect(input).toBeVisible();
    await expect(input).toBeEnabled();
    await expect(submit).toBeEnabled();

    // Consume the quota through the real endpoint, the way a user would.
    const domain = `e2e-${Date.now().toString().slice(-6)}.customer.invalid`;
    await input.fill(domain);
    await submit.click();

    // The row appears, which is what proves the registration landed rather than merely that a
    // request was sent.
    await expect(page.locator(`text=${domain}`).first()).toBeVisible({
      timeout: 15000,
    });

    // AT the quota: still on screen, no longer usable, and saying why. This is the assertion the
    // defect would fail -- with the old gate the locators resolve to nothing at all.
    await expect(input).toBeVisible();
    await expect(input).toBeDisabled();
    await expect(submit).toBeDisabled();
    await expect(
      page.locator('text=You have reached your custom domain limit'),
    ).toBeVisible();
  });
});
