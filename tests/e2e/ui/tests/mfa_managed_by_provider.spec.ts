import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';
import { V2 } from './utils/portal';

/**
 * When the identity provider handles MFA, Account Settings says so instead of offering the
 * gateway's own TOTP, which nothing would ask for (#2514). Both portals, per the A/B rule.
 *
 * The server decides (mfa_managed_by_provider on /api/me, covered in Go by
 * mfa_managed_test.go). This stack has no SSO provider, so /api/me is rewritten to carry the
 * flag; what is under test is that each portal renders it. Every test asserts something present
 * before anything absent, and each portal is also checked WITHOUT the flag, so "no Enable
 * button" cannot pass on a card that never rendered.
 */
const userEmail = `mm${Date.now().toString().slice(-6)}@lfr-demo.local`;
const PROVIDER = 'Liferay SSO';

async function stubManaged(page: any, managed: boolean) {
  await page.route('**/api/me', async (route: any) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const res = await route.fetch();
    const body = await res.json().catch(() => ({}));
    body.mfa_managed_by_provider = managed;
    body.mfa_provider_name = managed ? PROVIDER : '';
    await route.fulfill({
      response: res,
      body: JSON.stringify(body),
      headers: { ...res.headers(), 'content-type': 'application/json' },
    });
  });
}

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto(`${V2}/`);
  await page.fill('#email-input', userEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(userEmail);
  expect(token).toBeTruthy();
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard`);
}

test.describe('MFA handled by the identity provider', () => {
  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(userEmail, 'Em', 'Em');
  });

  test.afterAll(async () => {
    await deleteUser(userEmail);
  });

  test('V2: the card names the provider and offers no setup; without the flag it does', async ({
    page,
  }) => {
    await loginV2(page);

    await stubManaged(page, true);
    await page.goto(`${V2}/account`);
    const notice = page.getByTestId('mfa-managed-notice');
    await expect(notice).toBeVisible();
    await expect(notice).toContainText(PROVIDER);
    await expect(page.getByRole('button', { name: 'Setup MFA' })).toHaveCount(
      0,
    );

    // CONTROL: the same page without the flag offers setup, as today.
    await page.unroute('**/api/me');
    await stubManaged(page, false);
    await page.goto(`${V2}/dashboard`);
    await page.goto(`${V2}/account`);
    await expect(page.getByRole('button', { name: 'Setup MFA' })).toBeVisible();
    await expect(page.getByTestId('mfa-managed-notice')).toHaveCount(0);
  });

  // No V1 test since #2528: V1 serves admins only, and this needs a non-admin account.
});
