import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The access-control form: mode first, every field visible, only the relevant ones settable
 * (#2155).
 *
 * Two defects sit behind this. V2 offered three modes where the Inspector and V1 offered five,
 * so a reservation in `or` or `and` opened there with no option selected and -- because each
 * field rendered only under its own exact mode -- no passcode or whitelist field either. And
 * every surface let you fill in a field the chosen mode then ignored, which is how `and` came
 * to be saved with an empty IP list (#2156).
 *
 * `check-access-mode-parity.cjs` already asserts, statically and with its own mutation
 * controls, that all three forms and the server agree on the five modes. What it cannot see is
 * the behaviour this file is for: that a field the mode does not use is **disabled and still
 * visible**, rather than hidden. That distinction is the whole point -- the public notice
 * promises the values are "kept for when you switch back", and a hidden field is no evidence of
 * that while a greyed one showing its value is.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// A reservation in `and` mode: the state the owner reported, and the one V2 could not render at
// all. `passcode` is the mask the API hands out, never the stored hash.
const RESERVATIONS = [
  {
    id: 1,
    user_id: 'u1',
    user_email: adminEmail,
    subdomain: 'locked',
    domain: 'lfr-demo.local',
    access_mode: 'and',
    passcode: '********',
    whitelist_ips: '203.0.113.0/24',
    expires_at: '2027-12-25T09:30:00Z',
    created_at: '2026-08-11T15:39:37Z',
  },
];

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

async function stubReservations(page: any) {
  await page.route('**/api/portal/reservations', async (route: any) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      // The endpoint answers with an OBJECT -- the panel reads res.data.reservations and
      // falls back to [] -- so a bare array stubs as "no subdomains reserved yet" with no
      // error anywhere. The limits are sent too, or the panel renders its at-limit warning.
      body: JSON.stringify({
        reservations: RESERVATIONS,
        limit: 5,
        used: RESERVATIONS.length,
        custom_domain_limit: 1,
        custom_domain_used: 0,
      }),
    });
  });
}

async function openAccessControl(page: any) {
  // ReservationsPanel is rendered by the Dashboard, not on a route of its own -- there is no
  // /portalv2/reservations.
  await page.goto('/portalv2/dashboard');
  const row = page.locator('tr', { hasText: 'locked' }).first();
  await row.getByRole('button', { name: /Access Control/ }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
}

test.describe('V2 access control offers every mode and disables what the mode ignores', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('a reservation in AND opens with AND selected and both fields usable', async ({
    page,
  }) => {
    await loginV2(page);
    await stubReservations(page);
    await openAccessControl(page);

    // The capability V2 did not have. Before this, neither radio existed, so nothing was
    // selected and the dialog rendered no fields at all.
    await expect(
      page.getByRole('radio', { name: /Passcode AND Whitelist/ }),
    ).toBeChecked();

    // Both factors apply, so both are settable.
    await expect(page.locator('#passcode')).toBeEnabled();
    await expect(page.locator('#allowed-ips')).toBeEnabled();
    await expect(page.locator('#allowed-ips')).toHaveValue('203.0.113.0/24');
  });

  test('switching to Whitelist leaves the passcode visible but not settable', async ({
    page,
  }) => {
    await loginV2(page);
    await stubReservations(page);
    await openAccessControl(page);

    await page.getByRole('radio', { name: /IP Whitelist/ }).check();

    // Visible, so the user can see what is retained -- and disabled, so it cannot be filled in
    // under a mode that would ignore it. Hidden would satisfy "not settable" and break the
    // promise that the value is kept.
    await expect(page.locator('#passcode')).toBeVisible();
    await expect(page.locator('#passcode')).toBeDisabled();
    await expect(page.locator('#allowed-ips')).toBeEnabled();
  });

  test('Public disables both, and says why', async ({ page }) => {
    await loginV2(page);
    await stubReservations(page);
    await openAccessControl(page);

    await page.getByRole('radio', { name: /Public/ }).check();

    await expect(page.locator('#passcode')).toBeVisible();
    await expect(page.locator('#passcode')).toBeDisabled();
    await expect(page.locator('#allowed-ips')).toBeVisible();
    await expect(page.locator('#allowed-ips')).toBeDisabled();

    // The sentence that explains the greying. V2 had no such notice at all, while both other
    // surfaces have carried one throughout.
    await expect(page.getByRole('dialog')).toContainText(
      'kept for when you switch back',
    );
  });

  test('the OR mode exists too', async ({ page }) => {
    await loginV2(page);
    await stubReservations(page);
    await openAccessControl(page);

    const or = page.getByRole('radio', { name: /Passcode OR Whitelist/ });
    await or.check();
    await expect(or).toBeChecked();
    await expect(page.locator('#passcode')).toBeEnabled();
    await expect(page.locator('#allowed-ips')).toBeEnabled();
  });
});
