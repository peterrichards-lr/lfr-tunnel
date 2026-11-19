import { test, expect } from './utils/fixtures';
import {
  getMagicLinkPath,
  getMagicLinkToken,
  clearMailpit,
} from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';
import { V1, V2 } from './utils/portal';

/**
 * V2 is the portal, at /portal; V1 is an admin-only fallback (#2528, epic #2524).
 *
 * - Every older entry point -- `/`, `/admin`, `/portalv2/...`, V1's `/portal/<section>` -- lands
 *   on the matching V2 page, keeping its query string.
 * - Signing in, by magic link or SSO, starts and ends in V2, and a deep link survives it.
 * - V2 shows no V1 banner to anyone. The one way into V1 is the Admin Zone's "Classic portal
 *   (V1)" link, shown to admins only; anyone else who opens V1's path is sent to V2.
 *
 * Each absence is anchored on something that must be present (e2e-testing SKILL section 3).
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// The V1 banners V2 used to show, by their English text.
const V1_BANNER_TEXT = [
  /Return to V1/i,
  /Switch back to V1/i,
  /Prefer the classic look/i,
  /legacy interface/i,
];

async function signIn(page: any, email: string) {
  await clearMailpit();
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard**`);
}

async function expectNoV1Banner(page: any) {
  for (const text of V1_BANNER_TEXT) {
    await expect(page.getByText(text)).toHaveCount(0);
  }
  await expect(page.locator('.v1-promo-banner')).toHaveCount(0);
}

test.describe('V2 is the portal at /portal', () => {
  test('older entry points redirect to V2, keeping the query', async ({
    request,
  }) => {
    for (const [from, to] of [
      ['/', `${V2}`],
      ['/?lang=fr', `${V2}?lang=fr`],
      ['/admin?cli_login=1', `${V2}?cli_login=1`],
      ['/admin/users', `${V2}/admin/users`],
      ['/portalv2/dashboard?x=1', `${V2}/dashboard?x=1`],
      ['/portalv2/login?token=abc&lang=en', `${V2}/login?token=abc&lang=en`],
      // A magic link V1 emailed before the move: to V2's sign-in page, token intact.
      ['/portal?token=abc&lang=en', `${V2}/login?token=abc&lang=en`],
      ['/admin?sso_mfa=1', `${V2}/login?sso_mfa=1`],
      // A V1 section link from an old email.
      ['/portal/users', `${V2}/admin/users`],
      ['/portal/tokens', `${V2}/dashboard/tokens`],
    ]) {
      const res = await request.get(from, { maxRedirects: 0 });
      expect(res.status(), `GET ${from}`).toBe(302);
      expect(res.headers()['location'], `GET ${from}`).toBe(to);
    }
  });

  test('an old V1 section link opens the V2 page for an admin', async ({
    page,
  }) => {
    await signIn(page, adminEmail);
    await page.goto('/portal/users');
    await page.waitForURL(`**${V2}/admin/users`);
    await expect(
      page.getByRole('heading', { name: /User Management/i }).first(),
    ).toBeVisible();
  });

  test('a magic-link sign-in starting at /portal ends signed in on V2', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto(V2);
    await page.waitForURL(`**${V2}/login`);
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const link = await getMagicLinkPath(adminEmail);
    expect(link).toMatch(new RegExp(`^${V2}/login\\?token=`));
    await page.goto(link);
    await page.waitForURL(`**${V2}/dashboard`);
    await expect(page.locator('#access-tokens')).toBeVisible();
  });

  // The test stack has no identity provider, so the round trip itself is covered in Go
  // (sso_return_test.go: every sign-in lands on /portal/dashboard). What only a browser shows is
  // that V2's sign-in page offers SSO at all, from /portal.
  test('V2 sign-in offers SSO from /portal', async ({ page }) => {
    await page.goto(V2);
    await page.waitForURL(`**${V2}/login`);
    const sso = page.locator('[data-testid="sso-providers"] a').first();
    await expect(sso).toBeVisible();
    expect(await sso.getAttribute('href')).toMatch(
      /^\/api\/auth\/login\?provider=keycloak\b/,
    );
  });

  test('a deep link survives signing in', async ({ page }) => {
    await clearMailpit();
    await page.goto(`${V2}/admin/users`);
    await page.waitForURL(`**${V2}/login`);
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`${V2}/login?token=${token}`);
    await page.waitForURL(`**${V2}/admin/users`);
    await expect(
      page.getByRole('heading', { name: /User Management/i }).first(),
    ).toBeVisible();
  });
});

test.describe('V2 shows no V1 banner; V1 is for admins only', () => {
  const userEmail = `ep${Date.now().toString().slice(-6)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await createApprovedUser(userEmail);
  });

  test.afterAll(async () => {
    await deleteUser(userEmail);
  });

  test('the sign-in page has no V1 banner', async ({ page }) => {
    await page.goto(`${V2}/login`);
    await expect(page.locator('#email-input')).toBeVisible(); // anchor
    await expectNoV1Banner(page);
  });

  test('an admin sees no V1 banner, and the Admin Zone link opens V1', async ({
    page,
  }) => {
    await signIn(page, adminEmail);
    const link = page.getByTestId('classic-portal-link');
    await expect(link).toBeVisible(); // anchor, and the positive control
    await expect(link).toHaveAttribute('href', V1);
    await expectNoV1Banner(page);
    // The server gives the path to this session: the source of the link above.
    const me = await (await page.request.get('/api/me')).json();
    expect(me.email).toBe(adminEmail);
    expect(me.classic_portal_path).toBe(V1);

    await link.click();
    await page.waitForURL(`**${V1}**`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test('a user sees no V1 banner and no link to V1', async ({ page }) => {
    await signIn(page, userEmail);
    await expect(page.locator('.sidebar')).toBeVisible(); // anchor
    await expect(page.locator('#access-tokens')).toBeVisible();
    await expectNoV1Banner(page);
    await expect(page.getByTestId('classic-portal-link')).toHaveCount(0);
    await expect(page.locator(`a[href="${V1}"]`)).toHaveCount(0);
    // And the server does not hand it over. The sidebar would hide a leaked path anyway (the
    // link sits in the admin-only Admin Zone), so only this catches the server giving it out.
    const me = await (await page.request.get('/api/me')).json();
    expect(me.email).toBe(userEmail);
    expect(me).not.toHaveProperty('classic_portal_path');
  });

  test('a user who opens V1 directly is sent to V2', async ({ page }) => {
    await signIn(page, userEmail);
    await page.goto(V1);
    await page.waitForURL(`**${V2}/dashboard**`);
    await expect(page.locator('#access-tokens')).toBeVisible();
    await expect(page.locator('h2:has-text("Dashboard Overview")')).toHaveCount(
      0,
    );
  });

  test('a signed-out visitor who opens V1 is sent to V2 sign-in', async ({
    page,
  }) => {
    await page.goto(`${V1}/users`);
    await page.waitForURL(`**${V2}/login`);
    await expect(page.locator('#email-input')).toBeVisible();
  });
});
