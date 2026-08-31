import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * V1 Custom Domains (#1559).
 *
 * V2 has had `/admin/vanity-domain-status` since the vanity-domain work; V1 had only the on/off
 * hook toggle in System Settings. So a V1 admin could enable custom domains but could not see
 * whether any given one provisioned, and could not retry a failure without SSHing to the box.
 * Under a live A/B test that is a capability difference between the arms, not a cosmetic one.
 *
 * The three stage columns are the points where provisioning actually fails -- nginx config
 * written, certificate issued, domain live -- so the tests assert the stage semantics rather
 * than merely that a table appeared.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV1(page: any, email: string) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('Portal V1 Custom Domains', () => {
  // Removed afterwards: a fixture left behind widens the Admin Users email column for
  // portal_v2_table_scroll, which runs later against this same database and fails in CI only.
  const nonAdminEmail = `cd${Date.now().toString().slice(-6)}@lfr-demo.local`;

  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('an admin reaches it and sees every provisioning stage', async ({
    page,
  }) => {
    await loginV1(page, adminEmail);
    await page.locator('#nav-custom-domains').click();

    await expect(page.locator('#tab-custom-domains')).toBeVisible();

    // Positive anchor: the stage columns are the point of the screen, so assert they exist
    // before anything else. A table that rendered no headers would satisfy a looser check.
    const headers = page.locator('#tab-custom-domains thead th');
    await expect(headers).toHaveCount(8);
    for (const label of [
      'Domain',
      'Owner',
      'Requested',
      'Nginx Config',
      'Cert Issued',
      'Live',
      'Summary',
      'Actions',
    ]) {
      await expect(
        page
          .locator('#tab-custom-domains thead')
          .getByText(label, { exact: true }),
      ).toBeVisible();
    }
  });

  test('the stage columns distinguish failed from not-yet-reached', async ({
    page,
  }) => {
    await loginV1(page, adminEmail);

    // Stubbed: provisioning a real custom domain needs DNS and a certificate authority, so the
    // failure state cannot be produced against the test stack. The distinction being asserted --
    // a failed stage versus one simply not reached -- is exactly what a boolean "done?" check
    // would collapse, which is the bug worth guarding against.
    await page.route('**/api/admin/vanity-domain-status', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            full_host: 'broken.example.com',
            user_id: 'someone@lfr-demo.local',
            requested_at: '2026-08-29T09:00:00Z',
            nginx_config_at: '2026-08-29T09:01:00Z',
            cert_issued_at: null,
            live_at: null,
            failed_stage: 'cert_issued',
            error_message: 'ACME challenge timed out',
            updated_at: '2026-08-29T09:05:00Z',
          },
        ]),
      });
    });

    await page.locator('#nav-custom-domains').click();
    await expect(page.locator('#tab-custom-domains')).toBeVisible();

    const row = page.locator('#custom-domains-table-body tr').first();
    await expect(row).toContainText('broken.example.com');

    // The summary must name the stage and carry the reason, not just say "failed".
    await expect(row).toContainText('cert_issued');
    await expect(row).toContainText('ACME challenge timed out');

    const cells = row.locator('td');
    await expect(cells.nth(2)).toContainText('✓'); // requested: reached
    await expect(cells.nth(3)).toContainText('✓'); // nginx config: reached
    await expect(cells.nth(4)).toContainText('✕'); // cert issued: this is where it failed
    await expect(cells.nth(5)).toContainText('○'); // live: never reached, and NOT a failure
  });

  test('the summary renders the timestamp, not its markup', async ({
    page,
  }) => {
    // renderTimestamp returns HTML -- a span carrying the local-time tooltip -- so escaping the
    // whole summary displays the markup instead (#1616). Only the "Live since" branch shows it,
    // and no domain in the test stack ever reaches the live stage, so it is stubbed.
    await loginV1(page, adminEmail);

    await page.route('**/api/admin/vanity-domain-status', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            full_host: 'live.example.com',
            user_id: 'someone@lfr-demo.local',
            requested_at: '2026-08-11T15:30:00Z',
            nginx_config_at: '2026-08-11T15:35:00Z',
            cert_issued_at: '2026-08-11T15:38:00Z',
            live_at: '2026-08-11T15:39:37Z',
            failed_stage: '',
            error_message: '',
            updated_at: '2026-08-11T15:39:37Z',
          },
        ]),
      });
    });

    await page.locator('#nav-custom-domains').click();
    await expect(page.locator('#tab-custom-domains')).toBeVisible();

    const row = page.locator('#custom-domains-table-body tr').first();
    await expect(row).toContainText(/Live since/);
    // The tooltip span must be a real element, not text.
    await expect(row.locator('.timestamp-tooltip')).toHaveCount(1);
    // And no raw markup leaked into the visible text.
    await expect(row).not.toContainText('<span');
    await expect(row).not.toContainText('timestamp-tooltip"');
  });

  test('an error message from the pipeline is still escaped', async ({
    page,
  }) => {
    // The summary returns HTML now, so the untrusted parts must be escaped inside it. This is
    // the assertion that keeps that honest.
    await loginV1(page, adminEmail);

    await page.route('**/api/admin/vanity-domain-status', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            full_host: 'evil.example.com',
            user_id: 'someone@lfr-demo.local',
            requested_at: '2026-08-11T15:30:00Z',
            nginx_config_at: null,
            cert_issued_at: null,
            live_at: null,
            failed_stage: 'nginx_config',
            error_message: '<img src=x onerror="window.__pwned=1">boom',
            updated_at: '2026-08-11T15:39:37Z',
          },
        ]),
      });
    });

    await page.locator('#nav-custom-domains').click();
    await expect(page.locator('#tab-custom-domains')).toBeVisible();

    const row = page.locator('#custom-domains-table-body tr').first();
    // Shown as text...
    await expect(row).toContainText('boom');
    // ...and not injected as an element that could run.
    await expect(row.locator('img')).toHaveCount(0);
    const pwned = await page.evaluate(() => (window as any).__pwned);
    expect(pwned, 'the error message must not execute').toBeUndefined();
  });

  test('a non-admin cannot reach it', async ({ page }) => {
    await createApprovedUser(nonAdminEmail);
    await loginV1(page, nonAdminEmail);

    await expect(page.locator('#nav-custom-domains')).toBeHidden();

    await page.goto('/portal/custom-domains');
    await expect(page.locator('#tab-custom-domains')).toBeHidden();
  });
});
