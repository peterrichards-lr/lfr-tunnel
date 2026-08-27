import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Sessions per gateway, in BOTH portals (#1150).
 *
 * There was no view of which edges were carrying sessions, so nothing surfaced that a
 * region was unused, oversubscribed, or that a user was stranded on the wrong one. A
 * US-based user ran against the control plane rather than the `us` edge for a day because
 * `us` was inside its scheduled power window when they started and the 24h region cache
 * pinned the choice (#1148); it took reading journal logs to find.
 *
 * Two specs, one per portal, deliberately. Portal V1 and V2 are a live A/B test, so a
 * feature present in one and missing from the other is a difference between the arms and
 * not merely a gap -- and it is invisible from the other side. This is exactly how it got
 * here: V2 already rendered a live distribution pie and V1 rendered nothing, and nothing
 * failed.
 *
 * Both assert the API contract too, because a chart can render empty and look fine.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

test.describe('Sessions per gateway', () => {
  test('the API reports sessions per gateway per day to an admin', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    const res = await page.request.get('/api/analytics?days=30');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();

    // node_daily is the history; node_distribution is the live snapshot. Both belong to
    // the admin branch of handleGetAnalytics.
    expect(
      body.global,
      'an admin should receive the global block',
    ).toBeTruthy();
    expect(
      body.global,
      'node_daily is the field both portals chart',
    ).toHaveProperty('node_daily');
    expect(Array.isArray(body.global.node_daily)).toBeTruthy();

    // Shape, not contents: a fresh stack may legitimately have no traffic yet.
    for (const row of body.global.node_daily) {
      expect(row).toHaveProperty('date');
      expect(row).toHaveProperty('node_id');
      expect(row).toHaveProperty('sessions');
      expect(typeof row.sessions).toBe('number');
      // Rows are five-minute samples; sessions must be a count of distinct tunnels, so a
      // fractional or negative value means the aggregation is wrong.
      expect(Number.isInteger(row.sessions)).toBeTruthy();
      expect(row.sessions).toBeGreaterThan(0);
    }
  });

  test('Portal V1 renders the sessions-per-gateway chart', async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    await page.goto('/admin#analytics');
    await page.reload();

    // The admin section is display:none until loadAnalytics confirms the role.
    await expect(page.locator('#admin-analytics-section')).toBeVisible();
    await expect(page.locator('#nodeSessionsChart')).toBeAttached();

    // A canvas exists whether or not Chart.js ever drew into it, so assert the chart was
    // actually constructed rather than that the markup is present.
    //
    // Evaluated as a string, and reading `charts` bare rather than off window: it is a
    // top-level `let` in dashboard.js, which creates a global binding but NOT a property
    // of window, so `window.charts` is undefined and the check would silently never pass.
    await expect
      .poll(
        () =>
          page.evaluate(
            'typeof charts === "object" && charts !== null && !!charts.nodeSessions',
          ),
        { message: 'V1 should construct the nodeSessions Chart.js instance' },
      )
      .toBeTruthy();
  });

  test('Portal V2 renders the sessions-per-gateway chart', async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    await page.goto('/portalv2/admin/analytics');

    // Titled the same as V1's, so the two arms are describing one feature rather than two
    // similarly-shaped ones -- the naming problem #1209 had to fix.
    await expect(
      page.getByRole('heading', { name: 'Sessions per Gateway' }),
    ).toBeVisible();
  });
});
