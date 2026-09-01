import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The audit log must not stop silently (#1618).
 *
 * V1 asked for 100 entries and V2 sent no limit at all, taking the repository's default of 50 --
 * so each showed one page and neither offered any way to reach what lay beyond it. The two arms
 * also disagreed about how much history existed, which is a capability difference rather than a
 * presentation one.
 *
 * Both now page with offset, and say so when they reach their ceiling. Asserted on the requests
 * rather than on row counts: the test stack has far fewer than 50 audit entries, so counting rows
 * would pass whether or not paging happened.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

test.describe('Audit log paging', () => {
  test('V2 asks for pages with an offset, not one capped request', async ({
    page,
  }) => {
    const audits: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/admin/audit') && !r.url().includes('export')) {
        audits.push(r.url());
      }
    });

    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/audit');
    await expect(
      page.getByRole('heading', { name: /Audit Log/i }),
    ).toBeVisible();

    expect(
      audits.length,
      'the audit endpoint should be called',
    ).toBeGreaterThan(0);
    // The bug was a request with no limit falling back to the server's 50.
    expect(audits[0], 'the first page should ask for a limit').toContain(
      'limit=200',
    );
    expect(audits[0], 'and an explicit offset').toContain('offset=0');
  });

  test('V1 asks the same way, so both arms see the same history', async ({
    page,
  }) => {
    const audits: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/admin/audit') && !r.url().includes('export')) {
        audits.push(r.url());
      }
    });

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

    await page.click('#nav-audit');
    await expect(page.locator('#tab-audit')).toBeVisible();
    await page.waitForTimeout(600);

    expect(
      audits.length,
      'the audit endpoint should be called',
    ).toBeGreaterThan(0);
    expect(audits[0]).toContain('limit=200');
    expect(audits[0]).toContain('offset=0');
    // The old value, which is what made V1 and V2 disagree.
    expect(audits[0], 'should no longer ask for 100').not.toContain(
      'limit=100',
    );
  });

  test('V2 keeps paging while a full page comes back', async ({ page }) => {
    // Stubbed: the test stack has too little history to force a second page, so a full page is
    // returned once and then a short one. Without this the loop is never exercised at all.
    const seen: string[] = [];
    await page.route('**/api/admin/audit?*', async (route) => {
      const url = route.request().url();
      seen.push(url);
      const full = url.includes('offset=0');
      const rows = Array.from({ length: full ? 200 : 3 }, (_, i) => ({
        id: `${url}-${i}`,
        actor_id: 'admin@lfr-demo.local',
        action: 'test.event',
        target_type: 'none',
        target_id: '',
        details: '',
        ip_address: '127.0.0.1',
        created_at: '2026-08-31T12:00:00Z',
      }));
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(rows),
      });
    });

    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/audit');
    await expect(
      page.getByRole('heading', { name: /Audit Log/i }),
    ).toBeVisible();
    await page.waitForTimeout(600);

    expect(
      seen.length,
      'a full first page should be followed by a second request',
    ).toBeGreaterThan(1);
    expect(seen[1], 'the second request should advance the offset').toContain(
      'offset=200',
    );
  });
});
