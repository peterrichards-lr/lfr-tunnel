import { test, expect } from './utils/fixtures';
import { V2 } from './utils/portal';

/**
 * An SSO sign-in through a provider that does not perform MFA itself comes back to V2's sign-in page
 * with ?sso_mfa=1, to enter the account's TOTP code (#2517). The server holds the
 * pending sign-in in an HttpOnly cookie, so the portal submits the code with an EMPTY temp_token.
 *
 * This stack has no SSO provider, so the redirect is visited directly and /api/auth/mfa-verify is
 * intercepted: what is under test is that the portal shows its code prompt for the flag and
 * submits the code without a token of its own. The server side -- the challenge, the cookie, the
 * session it issues -- is covered in Go by sso_totp_test.go. The page is also loaded WITHOUT
 * the flag, which must show the ordinary sign-in rather than the prompt.
 */

async function captureVerify(page: any): Promise<{ body: any }> {
  const seen: { body: any } = { body: null };
  await page.route('**/api/auth/mfa-verify', async (route: any) => {
    seen.body = route.request().postDataJSON();
    await route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Invalid verification code or session' }),
    });
  });
  return seen;
}

test.describe('SSO sign-in asks for the TOTP code', () => {
  test('V2: ?sso_mfa=1 shows the code prompt and submits without a token', async ({
    page,
  }) => {
    // CONTROL first: the ordinary sign-in page has no code prompt.
    await page.goto(`${V2}/login`);
    await expect(page.locator('#email-input')).toBeVisible();
    await expect(page.locator('#mfa-form')).toHaveCount(0);

    const seen = await captureVerify(page);
    await page.goto(`${V2}/login?sso_mfa=1`);
    const code = page.getByLabel('Authentication code');
    await expect(code).toBeVisible();
    await code.fill('123456');
    await page.locator('#mfa-form').getByRole('button').click();
    await expect(
      page.getByText('Invalid verification code or session'),
    ).toBeVisible();
    expect(seen.body).toEqual({ temp_token: '', code: '123456' });
    await expect(page).not.toHaveURL(/sso_mfa=1/);
  });

  // No V1 test since #2528: every SSO sign-in returns to V2, and the server's challenge path is
  // V2's /portal/login?sso_mfa=1. The Go tests in sso_totp_test.go hold the path.
});
