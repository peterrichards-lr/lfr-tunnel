import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Registered Subdomains must show when a reservation expires (#1617).
 *
 * `SubdomainInfo` has carried `expires_at` all along and `/api/admin/subdomains` returns it -- it
 * simply was not in the columns array, so there was no column and nothing in the
 * column-visibility menu either. V1's equivalent screen has always shown it, so this was also a
 * parity gap.
 *
 * Both date columns are asserted, because Created Date was printing the raw ISO string and
 * adding Expires beside it would have doubled that.
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

// Stubbed: the test stack holds no reservations, so a real fetch renders an empty table and every
// assertion below would pass vacuously.
async function stubSubdomains(page: any) {
  await page.route('**/api/admin/subdomains', async (route: any) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 1,
          user_id: 'u1',
          user_email: 'someone@lfr-demo.local',
          subdomain: 'demo',
          domain: 'lfr-demo.local',
          full_host: 'demo.lfr-demo.local',
          expires_at: '2026-12-25T09:30:00Z',
          extension_requested: false,
          passcode: '',
          whitelist_ips: '',
          access_mode: 'public',
          created_at: '2026-08-11T15:39:37Z',
          updated_at: '2026-08-11T15:39:37Z',
          is_online: false,
          bytes_in: 0,
          bytes_out: 0,
        },
      ]),
    });
  });
}

test.describe('V2 Registered Subdomains expiry', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('an Expires column is shown by default', async ({ page }) => {
    await stubSubdomains(page);
    await loginV2(page);
    await page.goto('/portalv2/admin/subdomains');

    // Positive anchor: the table must have rendered, or the column assertions mean nothing.
    await expect(page.getByText('demo.lfr-demo.local')).toBeVisible();
    await expect(
      page.getByRole('columnheader', { name: /Expires/i }),
    ).toBeVisible();
  });

  test('the dates are readable, not raw ISO strings', async ({ page }) => {
    await stubSubdomains(page);
    await loginV2(page);
    await page.goto('/portalv2/admin/subdomains');
    await expect(page.getByText('demo.lfr-demo.local')).toBeVisible();

    const row = page.locator('tbody tr').first();
    // The exact format follows the user's own date preference, so this asserts the raw form is
    // gone rather than pinning one locale's output.
    await expect(row).not.toContainText('2026-12-25T09:30:00Z');
    await expect(row).not.toContainText('2026-08-11T15:39:37Z');
    // ...while the date itself is still there in some form.
    await expect(row).toContainText('2026');
  });

  test('Expires is offered in the Visible Columns menu and can be toggled off', async ({
    page,
  }) => {
    await stubSubdomains(page);
    await loginV2(page);
    await page.goto('/portalv2/admin/subdomains');
    await expect(page.getByText('demo.lfr-demo.local')).toBeVisible();

    // The request was "the column, or at least the option to add it from the visible columns",
    // so the menu entry matters as much as the column. It is also the only thing the column
    // definition actually controls: isColumnVisible() is a deny-list, so the <th> renders whether
    // or not the column is declared. Asserting the header alone would pass with the definition
    // deleted.
    await page.getByRole('button', { name: /Visible Columns/i }).click();
    const toggle = page
      .locator('label')
      .filter({ hasText: /^Expires$/ })
      .locator('input[type="checkbox"]');
    await expect(toggle).toBeVisible();
    await expect(toggle).toBeChecked();

    await toggle.uncheck();
    await page.keyboard.press('Escape');
    await expect(
      page.getByRole('columnheader', { name: /Expires/i }),
    ).toHaveCount(0);
  });
});
