import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Portal V1: a user's OWN custom domain provisioning status (#2233).
 *
 * #2223/#2224 let a non-admin register a custom domain from V1. Nothing then told them whether
 * it provisioned: `GET /api/portal/vanity-domain-status` had exactly one caller in the tree,
 * V2's VanityDomainStatusPanel. V1's Custom Domains tab is NOT that view -- it reads
 * `/api/admin/vanity-domain-status` and is in ADMIN_ONLY_TABS, so a non-admin cannot open it at
 * all. The same person therefore saw their five provisioning stages or saw nothing, decided only
 * by which arm of the A/B test served them.
 *
 * Signed in as a NON-ADMIN throughout, because that is the whole defect. Every spec here used to
 * sign in as admin@lfr-demo.local, which is how #1512 shipped a page that rendered perfectly and
 * was unreachable for the people it was written for.
 *
 * check-portal-parity.cjs carries a capability entry for the same endpoint, and it cannot prove
 * this: it asserts source presence, not render-time reachability. #2241 shipped a control present
 * in both arms' source and unreachable in one with that gate green throughout. This file is the
 * half that watches the screen.
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

test.describe('Portal V1 custom domain status', () => {
  // Removed in afterAll: every spec shares one database and they run in file order, so a row left
  // behind widens the Admin Users email column for portal_v2_table_scroll, which runs later and
  // fails in CI only (#1512, #1833). The local part is kept short for the same reason.
  const nonAdminEmail = `vs${Date.now().toString().slice(-6)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await createApprovedUser(nonAdminEmail);
  });

  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('a non-admin sees the provisioning stages of their own domain', async ({
    page,
  }) => {
    await loginV1(page, nonAdminEmail);

    // Stubbed: provisioning a real custom domain needs DNS and a certificate authority, so no
    // domain in the test stack ever reaches a later stage. The distinction being asserted --
    // failed AT a stage versus simply not reached -- is exactly what a boolean "done?" renderer
    // would collapse, reporting a dead provisioning run as merely slow.
    await page.route('**/api/portal/vanity-domain-status', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            full_host: 'mine.example.com',
            user_id: nonAdminEmail,
            requested_at: '2026-09-24T09:00:00Z',
            nginx_config_at: '2026-09-24T09:01:00Z',
            cert_issued_at: null,
            live_at: null,
            failed_stage: 'cert_issued',
            error_message: 'ACME challenge timed out',
            updated_at: '2026-09-24T09:05:00Z',
          },
        ]),
      });
    });

    // Which endpoint the panel actually asks is part of the defect, not an implementation
    // detail: reading the admin one would 403 for this user and render an empty table that
    // looks like "you have no custom domains".
    const asked: string[] = [];
    page.on('request', (req) => {
      if (req.url().includes('vanity-domain-status')) asked.push(req.url());
    });

    await page.locator('#nav-reservations').click();
    await expect(page.locator('#tab-reservations')).toBeVisible();

    // Positive anchor BEFORE anything else: an absence check is satisfied by a page that
    // rendered nothing at all (e2e-testing SKILL 3), and a V1 container that exists but is empty
    // is the signature of a loader wired to no branch (3b).
    await expect(page.locator('#custom-domain-status-title')).toBeVisible();
    const headers = page.locator('#custom-domain-status-table thead th');
    await expect(headers).toHaveCount(6);
    for (const label of [
      'Domain',
      'Requested',
      'Nginx Config',
      'Cert Issued',
      'Live',
      'Summary',
    ]) {
      await expect(
        page
          .locator('#custom-domain-status-table thead')
          .getByText(label, { exact: true }),
      ).toBeVisible();
    }

    const row = page.locator('#custom-domain-status-body tr').first();
    await expect(row).toContainText('mine.example.com');

    // The summary names the stage and carries the reason, rather than only saying "failed".
    await expect(row).toContainText('cert_issued');
    await expect(row).toContainText('ACME challenge timed out');

    const cells = row.locator('td');
    await expect(cells.nth(1)).toContainText('✓'); // requested: reached
    await expect(cells.nth(2)).toContainText('✓'); // nginx config: reached
    await expect(cells.nth(3)).toContainText('✕'); // cert issued: this is where it failed
    await expect(cells.nth(4)).toContainText('○'); // live: never reached, and NOT a failure

    expect(
      asked.filter((u) => u.includes('/api/portal/vanity-domain-status')),
      'the panel must read the per-user endpoint',
    ).not.toHaveLength(0);
    expect(
      asked.filter((u) => u.includes('/api/admin/vanity-domain-status')),
      'a non-admin cannot read the admin endpoint, so the panel must not ask it',
    ).toHaveLength(0);
  });

  test('with nothing registered the panel says so instead of vanishing', async ({
    page,
  }) => {
    // Not stubbed: this user has no custom domains, so the real endpoint answers [].
    //
    // V2's VanityDomainStatusPanel returns null for this case. V1 deliberately does not copy
    // that: its sections ship hidden and its cards ship display:none, so an empty container is
    // indistinguishable from a loader that never ran -- and registering does not start
    // provisioning (the hook runs when a client connects with -domain), so "no attempts tracked
    // yet" is the answer for everyone who has just registered and is wondering why nothing is
    // happening.
    await loginV1(page, nonAdminEmail);

    await page.locator('#nav-reservations').click();
    await expect(page.locator('#tab-reservations')).toBeVisible();
    await expect(page.locator('#custom-domain-status-title')).toBeVisible();

    await expect(page.locator('#custom-domain-status-body')).toContainText(
      'No custom domain attempts tracked yet.',
    );
  });

  test('a failed load says so rather than reading as "you have none"', async ({
    page,
  }) => {
    await loginV1(page, nonAdminEmail);

    await page.route('**/api/portal/vanity-domain-status', async (route) => {
      if (route.request().method() !== 'GET') return route.fallback();
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: '{"error":"Failed to retrieve vanity domain status"}',
      });
    });

    await page.locator('#nav-reservations').click();
    await expect(page.locator('#custom-domain-status-title')).toBeVisible();

    // The specific message, not merely "the table is empty": an empty table is what the bug
    // being guarded against looks like (#1868).
    await expect(page.locator('#custom-domain-status-body')).toContainText(
      'Could not load your custom domain status',
    );
    await expect(page.locator('#custom-domain-status-body')).not.toContainText(
      'No custom domain attempts tracked yet.',
    );
  });

  test('the admin Custom Domains tab is still admin-only', async ({ page }) => {
    // The other half of the property. The per-user panel is a NEW place, not a relaxation of the
    // admin table -- putting the user's view there would have left the defect in place while
    // looking fixed, and portal_v1_custom_domains.spec.ts asserts the same boundary from the
    // other side.
    await loginV1(page, nonAdminEmail);

    await expect(page.locator('#nav-custom-domains')).toBeHidden();

    await page.goto('/portal/custom-domains');
    // Positive anchor: showTab() normalises an unreachable section to the overview, so this
    // proves the portal rendered and still refused the tab, rather than that nothing loaded.
    await expect(page.locator('#tab-overview')).toBeVisible();
    await expect(page.locator('#tab-custom-domains')).toBeHidden();
  });

  test('an admin still sees the admin table, and their own panel too', async ({
    page,
  }) => {
    // The admin arm must keep working: the per-user panel is on the Reservations section for
    // everyone, and #nav-custom-domains still opens the fleet-wide table for an admin.
    await loginV1(page, adminEmail);

    await page.locator('#nav-reservations').click();
    await expect(page.locator('#custom-domain-status-title')).toBeVisible();

    await page.locator('#nav-custom-domains').click();
    await expect(page.locator('#tab-custom-domains')).toBeVisible();
  });
});
