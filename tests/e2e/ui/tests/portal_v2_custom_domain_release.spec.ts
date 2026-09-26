import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * The second half of a non-admin's custom-domain journey: releasing one gives the quota back
 * (#2249, proposal item 2).
 *
 * #2241's spec (portal_v2_custom_domain_at_quota.spec.ts) walks a user up to the quota and stops
 * there, because that is where the defect was. Nothing walks back down. The warning the portal
 * shows at the limit is *"You have reached your custom domain limit. Release one to register a
 * new one."* -- an instruction, and the only instruction offered to a user who wants a different
 * domain, since the quota defaults to one. Whether following it works has never been asserted
 * anywhere.
 *
 * That is the gap #2249 is about, stated as a ratio: 11 of 74 specs use a non-admin at all, and
 * they largely assert which nav items are visible. Visible nav is not a journey. This drives one
 * end to end -- register, reach the quota, release, register again -- and asserts what the user
 * GETS at each step rather than what is on the page.
 *
 * Why the full journey rather than just the release: the release is only meaningful against a
 * consumed quota, and the state has to be reached the way a user reaches it. Consuming the quota
 * through the API and asserting only the last screen would test a state the product may never
 * actually produce.
 *
 * Positive anchor before every absence check (e2e-testing SKILL 3): "the warning is gone" and
 * "the table is empty" are both satisfied by a page that rendered nothing at all, which is the
 * failure this file would otherwise report as a pass.
 */

// Short local part and removed in afterAll. Both are required, not either: portal_v2_table_scroll
// sizes the Admin Users email column to its widest cell and runs later in the same database, and
// shortening reduces the width without removing it (e2e-testing SKILL 4).
const nonAdminEmail = `cr${Date.now().toString().slice(-6)}@lfr-demo.local`;

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

test.describe('V2 custom-domain release', () => {
  test.beforeAll(async () => {
    await createApprovedUser(nonAdminEmail);
  });

  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('a non-admin who releases their custom domain can register another one', async ({
    page,
  }) => {
    await loginV2(page, nonAdminEmail);

    // POSITIVE ANCHOR: the panel is on screen before anything is asserted about its contents.
    const quotaLabel = page.getByText('Custom Domain Quota', { exact: true });
    await expect(quotaLabel).toBeVisible();
    const quotaRow = quotaLabel.locator('xpath=..');

    const input = page.getByLabel('Custom domain to register');
    const submit = page.getByRole('button', { name: 'Register Domain' });
    const table = page.locator('#custom-domains');
    const warning = page.locator(
      'text=You have reached your custom domain limit',
    );

    // Read rather than hardcoded: what this spec is about is that the number goes up and comes
    // back down, not what the limit happens to be configured as.
    await expect(quotaRow).toContainText('0 /');
    await expect(table).toContainText('No custom domains registered yet.');
    await expect(input).toBeEnabled();

    // --- register -------------------------------------------------------------------------
    const domain = `e2e-${Date.now().toString().slice(-6)}.customer.invalid`;
    await input.fill(domain);
    await submit.click();

    // The row, not the request: a registration that was sent and refused looks identical from
    // the form's side.
    await expect(table).toContainText(domain, { timeout: 15000 });
    await expect(quotaRow).toContainText('1 /');

    // --- at the quota ---------------------------------------------------------------------
    // Asserted here as the precondition for the release, not as a re-test of #2241: without it
    // a release that changed nothing would still leave every assertion below satisfiable by a
    // portal that had never counted the registration in the first place.
    await expect(warning).toBeVisible();
    await expect(input).toBeDisabled();
    await expect(submit).toBeDisabled();

    // --- release --------------------------------------------------------------------------
    await table.getByRole('button', { name: 'Release' }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    // Asserted by name before it is confirmed, so a stray confirm on some other action cannot
    // stand in for this one. Matched loosely because the copy is wrong on this path and should
    // be free to change: the dialog, its message and the success toast all say "subdomain" when
    // what is being released is a custom domain (#2273).
    await expect(dialog).toContainText(/release/i);
    await dialog.getByRole('button', { name: 'Confirm' }).click();

    // --- the quota is back ------------------------------------------------------------------
    // The outcome the warning told the user to expect. Anchored on the quota panel still being
    // rendered, so "the warning is gone" cannot be satisfied by a page that unmounted.
    await expect(quotaLabel).toBeVisible();
    await expect(quotaRow).toContainText('0 /', { timeout: 15000 });
    await expect(table).toContainText('No custom domains registered yet.');
    await expect(warning).toBeHidden();
    await expect(input).toBeEnabled();
    await expect(submit).toBeEnabled();

    // --- and the capacity is real -----------------------------------------------------------
    // An enabled control is not the same as a usable one. #2241 is the case where the two
    // disagreed in the other direction, and a portal that re-enables the form while the server
    // still holds the reservation would pass everything above and fail the user here.
    const second = `e2e-${Date.now().toString().slice(-6)}b.customer.invalid`;
    await input.fill(second);
    await submit.click();
    await expect(table).toContainText(second, { timeout: 15000 });

    // Left clean for the specs that follow: deleteUser removes the account, and a reservation
    // outliving its owner is not a state worth handing to the next spec.
    await table.getByRole('button', { name: 'Release' }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: 'Confirm' })
      .click();
    await expect(table).toContainText('No custom domains registered yet.', {
      timeout: 15000,
    });
  });
});
