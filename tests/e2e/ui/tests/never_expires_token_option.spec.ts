import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The never-expiring token option comes from the operator's policy, in BOTH arms (#2266).
 *
 * Why this cannot be a source gate. check-portal-parity reads source text, so it can say the
 * policy is referenced and cannot say the reference is reached. Both halves were demonstrated
 * while reviewing #2266: commenting out the single call that applies the policy in V1 left the
 * gate green, and V1 turned out never to load the policy at all on the MFA sign-in path while
 * the marker for it sat in the file. Only a rendered option list can tell.
 *
 * Why an ADMIN, and why this discriminates without touching tests/e2e/server-config.yaml.
 * That config sets no never_expires block, so every policy is `disabled` and the option must be
 * absent. Before #2266 both arms decided from the ROLE and SHOWED the option to an admin --
 * so an admin seeing no "Never" is exactly the old behaviour minus the new one. A non-admin
 * would have been refused by the old role gate too and would prove nothing (github-workflow
 * SKILL 5c: an assertion the pre-fix code also satisfies).
 *
 * The fail-closed direction is the one that matters. Offering an option the server answers with
 * 403 is the defect #2259 was; a missing option is a bug report.
 */
test.describe('the never-expiring token option follows the operator policy', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async () => {
    await clearMailpit();
  });

  async function signInV2(page: import('@playwright/test').Page) {
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  }

  async function signInV1(page: import('@playwright/test').Page) {
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  }

  test('V1 does not offer it on a gateway whose policy is disabled', async ({
    page,
  }) => {
    await signInV1(page);

    await page.goto('/admin#tokens');
    await page.reload();

    // The modal, not the static markup: V1 keeps the <option> in the DOM and decides its
    // visibility when the modal opens, so reading the select before that reads nothing.
    await page.click('button[onclick="openTokenModal()"]');
    const select = page.locator('#token-expiry');
    await expect(select).toBeVisible();

    // Every option the user can actually pick, by rendered text and value.
    const offered = await select.evaluate((el: HTMLSelectElement) =>
      Array.from(el.options)
        .filter((o) => getComputedStyle(o).display !== 'none')
        .map((o) => ({ value: o.value, text: (o.textContent ?? '').trim() })),
    );

    expect(
      offered.map((o) => o.value),
      'a gateway with never_expires.tokens disabled offered a non-expiring token to an admin',
    ).not.toContain('0');
    // And the control is still usable -- "no Never" must not mean "no options".
    expect(offered.length).toBeGreaterThan(0);
    expect(
      await select.evaluate((el: HTMLSelectElement) => el.selectedIndex),
      'the expiry select renders blank',
    ).toBeGreaterThanOrEqual(0);
  });

  test('V2 does not offer it on a gateway whose policy is disabled', async ({
    page,
  }) => {
    await signInV2(page);

    await page.getByRole('button', { name: /generate token/i }).click();
    const select = page.locator('#expiration');
    await expect(select).toBeVisible();

    const offered = await select.evaluate((el: HTMLSelectElement) =>
      Array.from(el.options).map((o) => ({
        value: o.value,
        text: (o.textContent ?? '').trim(),
      })),
    );

    expect(
      offered.map((o) => o.value),
      'a gateway with never_expires.tokens disabled offered a non-expiring token to an admin',
    ).not.toContain('0');
    expect(offered.length).toBeGreaterThan(0);
  });

  // CONTROL, and the half that makes the two assertions above mean something: the arms must
  // offer the SAME options, not merely both omit one. Two arms independently wrong in the same
  // direction is what #2259 was, and "neither shows Never" would be satisfied by an arm whose
  // select is empty.
  test('both arms offer the same expiry options', async ({ page }) => {
    await signInV1(page);

    await page.goto('/admin#tokens');
    await page.reload();
    await page.click('button[onclick="openTokenModal()"]');
    const v1 = await page
      .locator('#token-expiry')
      .evaluate((el: HTMLSelectElement) =>
        Array.from(el.options)
          .filter((o) => getComputedStyle(o).display !== 'none')
          .map((o) => o.value),
      );

    // Straight to V2, NOT a second sign-in. Both arms share the lfr_session cookie, so the
    // context is already authenticated and /portalv2/ redirects past the login form -- there
    // is no #email-input to fill, which is how the first version of this test timed out.
    // Comparing the same session across the two arms is also closer to what a user does.
    await page.goto('/portalv2/dashboard');
    await page.waitForURL('**/portalv2/dashboard');
    const generate = page.getByRole('button', { name: /generate token/i });
    await expect(generate).toBeVisible({ timeout: 20000 });
    await generate.click();
    const v2 = await page
      .locator('#expiration')
      .evaluate((el: HTMLSelectElement) =>
        Array.from(el.options).map((o) => o.value),
      );

    expect(
      v2,
      'the two arms offer different token lifetimes, which is #2259',
    ).toEqual(v1);
  });
});
