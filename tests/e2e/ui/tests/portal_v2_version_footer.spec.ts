import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Gateway and client versions in the V2 sidebar footer (#1647), matching V1.
 *
 * This is a deployment check as much as parity: #1632 shipped a twelve-day-old portal behind a
 * correct version string, and it took a user noticing a missing column to find it. A version in
 * the footer would have shown it at a glance.
 *
 * Uptime is deliberately NOT here -- the header already carries it beside the status indicator,
 * and that duplication is what #1603 removed for the status link. Asserted, so it does not creep
 * back in.
 */
const adminEmail = 'admin@lfr-demo.local';

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

test.describe('V2 sidebar version footer', () => {
  test('it shows the gateway and client versions', async ({ page }) => {
    await loginV2(page);

    const versions = page.locator('.sidebar-footer-versions');
    await expect(versions).toBeVisible();

    // Matched against what /api/version actually reports, not a hardcoded string, so the test
    // does not need editing at every release -- and so a stale or wrong value fails.
    // page.request rather than an in-page fetch: the latter hung to the 30s test timeout here,
    // and it shares the page's cookies anyway, so there is nothing gained by running it inside
    // the document.
    const api = await page.request
      .get('/api/version')
      .then((r: any) => r.json());
    const expected = api.server_version || api.latest_version;
    expect(expected).toBeTruthy();

    await expect(versions).toContainText('Gateway');
    await expect(versions).toContainText(expected);
    await expect(versions).toContainText('Client');
  });

  test('it does not repeat the uptime already shown in the header', async ({
    page,
  }) => {
    await loginV2(page);

    const versions = page.locator('.sidebar-footer-versions');
    await expect(versions).toBeVisible();

    // Anchored on the versions being present first, so this cannot pass on an empty footer.
    await expect(versions).toContainText('Gateway');
    await expect(versions).not.toContainText(/uptime/i);
    // V1's format is "v1.2.3 (Uptime: 10m)" -- the parenthesised suffix is the specific shape
    // that must not appear.
    await expect(versions).not.toContainText(/\(.*\d+[dhm].*\)/);
  });
});
