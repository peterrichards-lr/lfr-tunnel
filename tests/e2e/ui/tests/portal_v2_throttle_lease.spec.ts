import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The Throttle action on Registered Subdomains (#1629).
 *
 * It posted to /api/admin/leases/throttle, which has never had a handler -- there is no
 * reference to "throttle" in any non-test Go file. The real endpoint is
 * /api/admin/leases/rate-limit, and it reads `host`, not `full_host`, so fixing only the path
 * would still have failed with "Host is required".
 *
 * The request itself is what these assert. A test that only checked for a success toast would
 * pass against a handler that silently ignored the body.
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

// A reservation with a matching live lease, so the Throttle button is enabled.
async function stubWithLiveLease(page: any) {
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
          expires_at: '2026-12-25T09:30:00Z',
          created_at: '2026-08-11T15:39:37Z',
        },
      ]),
    });
  });
  await page.route('**/api/admin/leases', async (route: any) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          subdomain_prefix: 'demo',
          full_host: 'demo.lfr-demo.local',
          client_ip: '10.1.2.3',
          bytes_in: 1,
          bytes_out: 1,
          rate_limit: 0,
          node_id: 'edge-us',
        },
      ]),
    });
  });
}

test.describe('V2 Throttle posts a request the server can actually serve', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('it uses the rate-limit endpoint and the host field', async ({
    page,
  }) => {
    await stubWithLiveLease(page);

    let seen: { url: string; body: any } | null = null;
    // Deliberately matched on a wildcard under /api/admin/leases so a request to the OLD path
    // is captured too -- matching only the correct path would let the bug through as "no
    // request seen" rather than failing on the wrong one.
    await page.route('**/api/admin/leases/**', async (route: any) => {
      const req = route.request();
      if (req.method() === 'POST') {
        seen = { url: req.url(), body: req.postDataJSON() };
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ status: 'success' }),
        });
        return;
      }
      await route.continue();
    });

    await loginV2(page);
    await page.goto('/portalv2/admin/subdomains');
    await expect(page.getByText('demo.lfr-demo.local')).toBeVisible();

    await page
      .getByRole('button', { name: /^Throttle$/ })
      .first()
      .click();

    // showPrompt renders a React dialog, not window.prompt -- a page.on('dialog') handler never
    // fires for it and the click appears to do nothing at all.
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.locator('input[type="text"]').fill('25');
    await dialog.getByRole('button', { name: /Confirm/i }).click();

    await expect.poll(() => seen).not.toBeNull();
    const sent = seen!;
    expect(sent.url).toContain('/api/admin/leases/rate-limit');
    expect(sent.url).not.toContain('/throttle');
    // The handler reads `host`; `full_host` is ignored and yields "Host is required".
    expect(sent.body).toMatchObject({
      host: 'demo.lfr-demo.local',
      rate_limit: 25,
    });
  });

  test('a non-numeric rate is rejected rather than removing the limit', async ({
    page,
  }) => {
    await stubWithLiveLease(page);

    let posted = false;
    await page.route('**/api/admin/leases/**', async (route: any) => {
      if (route.request().method() === 'POST') {
        posted = true;
        await route.fulfill({ status: 200, body: '{}' });
        return;
      }
      await route.continue();
    });

    await loginV2(page);
    await page.goto('/portalv2/admin/subdomains');
    await expect(page.getByText('demo.lfr-demo.local')).toBeVisible();

    await page
      .getByRole('button', { name: /^Throttle$/ })
      .first()
      .click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.locator('input[type="text"]').fill('abc');
    await dialog.getByRole('button', { name: /Confirm/i }).click();

    // parseInt('abc') is NaN -> JSON null -> the handler reads 0 -> the limit is REMOVED. So
    // "nothing was sent" is the requirement, not merely "no crash".
    // Scoped to the visible toast: the same text is also mirrored into the sr-only live region,
    // so an unscoped getByText matches two elements and trips strict mode.
    await expect(
      page
        .locator('.toast-card')
        .getByText(/whole number of requests per second/i),
    ).toBeVisible();
    expect(posted).toBe(false);
  });
});
