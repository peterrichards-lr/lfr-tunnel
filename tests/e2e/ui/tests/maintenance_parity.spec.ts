import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser } from './utils/nonadmin';

/**
 * Gateway Maintenance in V2 (#1568), the counterpart of V1's screen.
 *
 * IMPORTANT: nothing here ever confirms the dialog. Enabling maintenance blocks standard logins
 * and kicks active tunnels on the one gateway the whole suite shares, so a spec that actually
 * turned it on would fail every spec that runs after it. Each test opens the confirmation to
 * prove it is required and describes the consequence, then cancels.
 */
const adminEmail = 'admin@lfr-demo.local'; // Also the owner, per tests/e2e/server-config.yaml

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

test.describe('Gateway Maintenance in V2', () => {
  test('shows the current state, and the tri-state status is not a boolean', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    await page.goto('/portalv2/admin/maintenance');

    await expect(
      page.getByRole('heading', { name: /Gateway Maintenance/i }),
    ).toBeVisible();

    // Inactive is the state the shared stack should be in; the point of asserting it is that
    // the badge renders a real value rather than being absent.
    await expect(page.getByTestId('soft-status')).toHaveText(/Inactive/i);

    await expect(page.locator('#btn-toggle-maint')).toBeVisible();
    await expect(page.locator('#maint-countdown-select')).toBeVisible();
  });

  test('enabling requires a confirmation that names the consequence', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    await page.goto('/portalv2/admin/maintenance');

    await page.locator('#maint-countdown-select').selectOption('0');
    await page.locator('#btn-toggle-maint').click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    // Immediate activation must say what it does, not just ask "are you sure?".
    await expect(dialog).toContainText(/closes all standard tunnels/i);

    // Cancel — see the note at the top of this file.
    await dialog.getByRole('button', { name: /Cancel/i }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByTestId('soft-status')).toHaveText(/Inactive/i);
  });

  test('scheduling warns about the countdown rather than immediate cutoff', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    await page.goto('/portalv2/admin/maintenance');

    await page.locator('#maint-countdown-select').selectOption('5');
    await page.locator('#btn-toggle-maint').click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(/countdown reaches zero/i);

    await dialog.getByRole('button', { name: /Cancel/i }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  // The scheduled state cannot be produced for real without enabling maintenance on the shared
  // gateway, so the response is stubbed. That is the whole point of the case: `status` is
  // "true" | "false" | "pending", and code that treats it as a boolean renders a scheduled
  // window as off -- the state an operator most needs to see.
  test('a scheduled window renders as Scheduled, not as off', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);

    await page.route('**/api/admin/maintenance', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'pending',
          maintenance_mode: 'pending',
          iron_curtain: false,
          action: 'Server Upgrade',
          reason: 'System upgrade and maintenance',
          duration: 30,
          start_time: '2026-08-29T12:00:00Z',
        }),
      });
    });

    await page.goto('/portalv2/admin/maintenance');

    await expect(page.getByTestId('soft-status')).toHaveText(/Scheduled/i);
    await expect(page.getByTestId('soft-status')).not.toHaveText(/Inactive/i);
    // Already on, so the button must offer the way out rather than another enable.
    await expect(page.locator('#btn-toggle-maint')).toHaveText(/Disable/i);
  });

  test('the iron curtain is owner-only and warns it cannot be undone from here', async ({
    page,
  }) => {
    await loginV2(page, adminEmail); // owner
    await page.goto('/portalv2/admin/maintenance');

    const curtain = page.getByTestId('iron-curtain');
    await expect(curtain).toBeVisible();
    await expect(curtain).toContainText(/disabled via SSH/i);
    await expect(page.locator('#btn-toggle-hard-maint')).toBeVisible();
  });

  test('a non-admin cannot reach the route', async ({ page }) => {
    // Short local part: a long address widens the Admin Users table and breaks
    // portal_v2_table_scroll, which runs later against this same database, and only in CI.
    const email = `nm${Date.now().toString().slice(-6)}@lfr-demo.local`;
    await createApprovedUser(email);
    await loginV2(page, email);

    await page.goto('/portalv2/admin/maintenance');

    // Positive anchor first: a blank page would satisfy the absence check on its own.
    await expect(page.locator('#root')).not.toBeEmpty();
    await expect(page.locator('#btn-toggle-maint')).toHaveCount(0);
  });
});
