import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * All Time actually means all time, in both portals (#1565).
 *
 * The option was broken in two directions at once. Both portals omitted `days` for All Time, so
 * the server fell back to its 30-day default and the option silently returned the same data as
 * Last 30 Days. Sending days=0 would not have helped either: the floor was computed as
 * "today minus days", so zero meant today, showing a single day.
 *
 * Asserted on the request rather than the rendered numbers, because the test stack has little
 * enough history that 30 days and all time can legitimately contain the same rows -- which is
 * exactly how a fix here would look correct while doing nothing.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

test.describe('All Time analytics range', () => {
  test('V2 asks for days=0, not for nothing', async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
    await expect(
      page.getByRole('heading', { name: /Analytics/i }),
    ).toBeVisible();

    const analytics = page.waitForRequest(
      (r) => r.url().includes('/api/analytics') && r.url().includes('days=0'),
    );
    await page.getByLabel('Time range').selectOption('0');
    expect((await analytics).url()).toContain('days=0');
  });

  test('V1 asks for days=0 too, and for the same latency window', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
    await page.click('#nav-analytics');
    await expect(page.locator('#tab-analytics')).toBeVisible();

    const analytics = page.waitForRequest(
      (r) => r.url().includes('/api/analytics') && r.url().includes('days=0'),
    );
    // Region latency used to be given 365 when the range was All Time, so the screen showed a
    // year of latency beside all-time bandwidth. It must follow the same window now.
    //
    // Matched on days=0 rather than on the path alone. The path fires twice -- once on initial
    // load at days=30, then again when the range changes -- and an unfiltered waiter races them:
    // it caught the second locally, where the first had already settled, and the first in CI,
    // where it had not. That is a flaky assertion, not a flaky page.
    const latency = page.waitForRequest(
      (r) =>
        r.url().includes('/api/admin/analytics/region-latency') &&
        r.url().includes('days=0'),
    );

    await page.locator('#analytics-range').selectOption('0');

    expect((await analytics).url()).toContain('days=0');
    const latencyUrl = (await latency).url();
    expect(latencyUrl).toContain('days=0');
    expect(latencyUrl).not.toContain('days=365');
    // The waiter above already requires days=0, so this line alone would be circular. It is the
    // 365 check that carries the weight: it is the substitution this fix removed.
    expect(latencyUrl).not.toContain('days=30');
  });
});
