import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';
import { V2 } from './utils/portal';

/**
 * A session that signed in through an identity provider shows the first and last name read-only,
 * because the provider refreshes them at every sign-in; Preferred Name stays editable (#2515).
 * Both portals, per the A/B rule.
 *
 * The server decides (names_from_provider on /api/me, and PUT /api/me refusing a change -- both
 * covered in Go by sso_names_test.go). This stack has no SSO provider, so /api/me is rewritten
 * to carry the flag. Each portal is checked with and without it, so "editable" cannot pass on
 * a form that never rendered.
 */
const userEmail = `nf${Date.now().toString().slice(-6)}@lfr-demo.local`;
const PROVIDER = 'Liferay SSO';

async function stubNames(page: any, fromProvider: boolean) {
  await page.route('**/api/me', async (route: any) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const res = await route.fetch();
    const body = await res.json().catch(() => ({}));
    body.names_from_provider = fromProvider;
    body.names_provider_name = fromProvider ? PROVIDER : '';
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

test.describe('Names from the identity provider', () => {
  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(userEmail, 'Nora', 'Field');
  });

  test.afterAll(async () => {
    await deleteUser(userEmail);
  });

  test('V2: first and last name read-only with a note; preferred name editable', async ({
    page,
  }) => {
    await loginV2(page);

    await stubNames(page, true);
    await page.goto(`${V2}/account`);
    const note = page.getByTestId('names-from-provider-note');
    await expect(note).toBeVisible();
    await expect(note).toContainText(PROVIDER);
    await expect(page.locator('#first-name')).toHaveValue('Nora');
    await expect(page.locator('#first-name')).not.toBeEditable();
    await expect(page.locator('#last-name')).not.toBeEditable();
    await expect(page.locator('#preferred-name')).toBeEditable();

    // CONTROL: without the flag the names are editable and there is no note.
    await page.unroute('**/api/me');
    await stubNames(page, false);
    await page.goto(`${V2}/dashboard`);
    await page.goto(`${V2}/account`);
    await expect(page.locator('#first-name')).toHaveValue('Nora');
    await expect(page.locator('#first-name')).toBeEditable();
    await expect(page.locator('#last-name')).toBeEditable();
    await expect(page.getByTestId('names-from-provider-note')).toHaveCount(0);
  });

  // No V1 test since #2528: V1 serves admins only, and this needs a non-admin account.
});
