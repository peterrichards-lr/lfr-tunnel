import * as fs from 'fs';
import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Five small things V1 offered and V2 did not (#2526, epic #2524):
 *   - an admin sets a user's preferred domain from the user modal;
 *   - the user modal shows the onboarding tour's status and detail;
 *   - a user exports their reservations as CSV;
 *   - the `docker pull` box, with its Docker Hub and EDR-guide links;
 *   - the documentation and secure-token-guide links.
 *
 * Each assertion is against a value the fixture made distinctive, so a component that rendered
 * a constant would fail: the tour is "aborted" at step "tokens" with 2 reruns, the export carries
 * an expired row (Quarantined) beside a live one (Active), and the help links point at URLs that
 * exist only in the stub. The controls show each piece ABSENT when the gateway configures nothing.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

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

// /api/version as the gateway answers it, with `edit` applied.
async function stubVersion(page: any, edit: (body: any) => void) {
  await page.route('**/api/version', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json();
    edit(body);
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
}

async function openHelp(page: any) {
  await page.reload();
  await page.getByRole('button', { name: /What's New & Help/ }).click();
}

test.describe('V2 closes the small gaps with V1', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('the user modal shows the tour and sets the preferred domain', async ({
    page,
  }) => {
    await loginV2(page);
    await page.route('**/api/admin/users', async (route: any) => {
      if (route.request().method() !== 'GET') return route.continue();
      const res = await route.fetch();
      const users = await res.json();
      for (const u of users) {
        if (u.email === adminEmail) {
          // An ID that is not the email, as the owner row's is in production (its ID comes from
          // owner.user_id). In this stack the two are equal, so without this a call addressed by
          // email would pass for one addressed by ID.
          u.id = 'u-admin-2534';
          adminId = u.id;
          u.onboarding_status = 'aborted';
          u.onboarding_last_step = 'tokens';
          u.onboarding_reruns = 2;
        }
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(users),
      });
    });
    let adminId = '';
    let put: { url: string; body: any } | null = null;
    await page.route('**/preferred-domain', async (route: any) => {
      put = {
        url: route.request().url(),
        body: route.request().postDataJSON(),
      };
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{}',
      });
    });

    await page.goto('/portalv2/admin/users');
    const row = page.locator('tr', { hasText: adminEmail }).first();
    await row.locator('button:text-is("Details")').click();
    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible();

    await expect(modal.getByTestId('user-tour-status')).toHaveText('Aborted');
    await expect(modal.getByTestId('user-tour-detail')).toHaveText(
      'Last step: tokens · Reruns: 2',
    );

    await modal
      .locator('#user-preferred-domain')
      .selectOption('lfr-demo.local');
    await expect.poll(() => put).not.toBeNull();
    // By ID, like every other admin call: the server resolves the ID first (#2469), and an email
    // can name a different account among case duplicates.
    expect(adminId).not.toBe('');
    expect(put!.url).toContain(
      `/api/admin/users/${encodeURIComponent(adminId)}/preferred-domain`,
    );
    expect(put!.body).toEqual({ preferred_domain: 'lfr-demo.local' });
    await expect(modal.locator('#user-preferred-domain')).toHaveValue(
      'lfr-demo.local',
    );
  });

  test('reservations export as the same CSV V1 produces', async ({ page }) => {
    await loginV2(page);
    // `late` appears only once the panel has loaded: the export must fetch at the click, as V1
    // does, and not hand over the list the panel read when the page opened.
    let gets = 0;
    let exporting = false;
    await page.route('**/api/portal/reservations', async (route: any) => {
      if (route.request().method() !== 'GET') return route.continue();
      gets++;
      const res = await route.fetch();
      const body = await res.json();
      const late = exporting
        ? [
            {
              id: 'r-late',
              subdomain: 'latesub',
              domain: 'lfr-demo.local',
              status: 'active',
            },
          ]
        : [];
      body.reservations = [
        ...late,
        {
          id: 'r-live',
          subdomain: 'livesub',
          domain: 'lfr-demo.local',
          status: 'active',
          user_id: 'owner-1',
          created_at: '2026-11-01T00:00:00Z',
          expires_at: '2099-01-01T00:00:00Z',
        },
        {
          id: 'r-old',
          subdomain: 'oldsub',
          domain: 'lfr-demo.local',
          status: 'active',
          user_id: 'owner-1',
          created_at: '2026-01-01T00:00:00Z',
          expires_at: '2026-02-01T00:00:00Z',
        },
        {
          // The CSV-injection control: a cell a spreadsheet would run as a formula.
          id: '=HYPERLINK("x")',
          subdomain: 'formula',
          domain: 'lfr-demo.local',
          status: 'active',
        },
      ];
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      });
    });
    await page.reload();

    const section = page.locator('#registered-subdomains');
    await expect(section).toContainText('livesub');
    expect(gets).toBeGreaterThan(0);
    exporting = true;
    const download = page.waitForEvent('download');
    await section.locator('button:text-is("Export CSV")').click();
    const file = await download;
    expect(file.suggestedFilename()).toBe('subdomain_reservations.csv');
    const csv = fs.readFileSync((await file.path())!, 'utf-8');
    const lines = csv.trim().split('\n');

    expect(lines[0]).toBe(
      '"ID","Subdomain","Domain","Owner","Status","ExpiresAt","CreatedAt"',
    );
    expect(lines).toContain(
      '"r-live","livesub","lfr-demo.local","owner-1","Active","2099-01-01T00:00:00Z","2026-11-01T00:00:00Z"',
    );
    expect(lines).toContain(
      '"r-old","oldsub","lfr-demo.local","owner-1","Quarantined","2026-02-01T00:00:00Z","2026-01-01T00:00:00Z"',
    );
    expect(lines.some((l) => l.startsWith('"r-late"'))).toBe(true);
    // Quoted, quotes doubled, and defused with a leading apostrophe.
    expect(lines).toContain(
      `"'=HYPERLINK(""x"")","formula","lfr-demo.local","","Active","Never",""`,
    );
  });

  test('Help & Resources links the docs and the Docker image', async ({
    page,
  }) => {
    await loginV2(page);
    await stubVersion(page, (v) => {
      v.documentation_url = 'https://docs.example.test/tunnel';
      v.secure_token_guide_url = 'https://docs.example.test/secure-token';
      v.docker_image = 'example/tunnel-client:9.9';
      v.docker_hub_url = '';
    });
    await openHelp(page);

    await expect(
      page.locator('a[href="https://docs.example.test/tunnel"]'),
    ).toBeVisible();
    await expect(
      page.locator('a[href="https://docs.example.test/secure-token"]'),
    ).toBeVisible();
    const box = page.getByTestId('docker-pull-box');
    await expect(box).toContainText('docker pull example/tunnel-client:9.9');
    // No hub URL configured: V1's fallback, the image's repository on Docker Hub.
    await expect(
      box.locator('a[href="https://hub.docker.com/r/example/tunnel-client"]'),
    ).toBeVisible();
  });

  test('the help links do not depend on the release notes loading', async ({
    page,
  }) => {
    await loginV2(page);
    await page.route('**/static/whats-new.json', (route: any) =>
      route.fulfill({ status: 404, body: '' }),
    );
    await stubVersion(page, (v) => {
      v.documentation_url = 'https://docs.example.test/tunnel';
    });
    await openHelp(page);
    await expect(
      page.locator('a[href="https://docs.example.test/tunnel"]'),
    ).toBeVisible();
  });

  test('nothing configured, nothing shown', async ({ page }) => {
    await loginV2(page);
    await stubVersion(page, (v) => {
      delete v.documentation_url;
      delete v.secure_token_guide_url;
      delete v.docker_image;
    });
    await openHelp(page);

    // The bar did open -- its own content is there -- so the absences below mean something.
    await expect(page.getByText('Help & Resources')).toBeVisible();
    await expect(page.getByTestId('docker-pull-box')).toHaveCount(0);
    await expect(page.getByText('Read the Documentation')).toHaveCount(0);
  });
});
