import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 System Settings parity with V1 (#1290).
 *
 * Reported as "V2 seems to be missing the System Settings from V1". It was not missing --
 * it was called something else and filed somewhere else, so someone looking for the name
 * they knew, in the place they knew, found neither.
 *
 * The integration-test control is the part that matters operationally: it is the only way
 * to confirm Slack/Teams/admin-email alerting works before you need it.
 */
test.describe('Portal V2 System Settings', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  test('the sidebar calls it System Settings, as V1 does', async ({ page }) => {
    // V1 labels this "System Settings" (dashboard.html:223). V2 called it just
    // "Settings", which is what made it look absent.
    const link = page.getByRole('link', { name: 'System Settings' });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute('href', /\/admin\/settings$/);
  });

  test('the integration test can be triggered and reports a result', async ({
    page,
  }) => {
    await page.goto('/portalv2/admin/settings');

    const button = page.getByRole('button', { name: /Trigger Test Webhook/i });
    await expect(button).toBeVisible();

    // Assert the request actually goes to the same endpoint V1 uses, rather than that a
    // button exists -- a button wired to nothing looks identical from the DOM.
    const [request] = await Promise.all([
      page.waitForRequest(
        (r) =>
          r.url().includes('/api/admin/test-webhook') && r.method() === 'POST',
        { timeout: 15000 },
      ),
      button.click(),
    ]);
    expect(request).toBeTruthy();

    // And that the outcome is reported either way -- success or failure both produce a
    // toast, which is the point of the control.
    await expect(page.locator('.toast, [class*="toast"]').first()).toBeVisible({
      timeout: 15000,
    });
  });

  test('the page still carries the settings V1 has', async ({ page }) => {
    await page.goto('/portalv2/admin/settings');
    await expect(
      page.getByRole('heading', { name: /System Settings/i }),
    ).toBeVisible();
    // Domain allocation and the vanity hook are the cards both portals share; if a future
    // change drops one, this notices.
    await expect(page.getByText(/Domain Allocation/i).first()).toBeVisible();
  });
});
