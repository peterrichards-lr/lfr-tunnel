import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Diagnostic log sharing consent, in both portals (#1696).
 *
 * The two things worth asserting in a browser, because neither is provable from a Go test:
 *
 *  1. **It starts off.** A freshly registered account has never granted this, and the control
 *     has to show that rather than defaulting to on. A pre-ticked box is not consent, and the
 *     failure mode here is silent -- a toggle rendered "on" against a NULL column looks correct
 *     until somebody's logs are collected.
 *  2. **It applies immediately, and it survives a navigation.** Unlike the other Account
 *     Settings toggles there is no Save step, because withdrawing consent should not require
 *     finding a button afterwards. So the assertion is not "the switch moved" -- a switch moves
 *     whether or not anything was written -- but "the switch is still on after leaving the page
 *     and coming back", which can only be true if the server stored it.
 *
 * Run as a NON-admin. This is an ordinary user's setting and the admin account is not
 * representative of the people it is for.
 *
 * The email is deliberately short and removed in afterAll: the database is shared across the
 * whole suite in file order, and portal_v2_table_scroll measures the Admin Users email column.
 */
const userEmail = `dc${Date.now().toString().slice(-6)}@lfr-demo.local`;

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', userEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(userEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', userEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(userEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

// The native checkbox in a .toggle-switch is opacity:0 / width:0 / height:0 -- visually replaced
// by .toggle-slider -- so it is never "visible" to Playwright and .check() cannot click it. Click
// the slider; read checked-ness from the input.
async function flipToggle(page: any, inputSelector: string) {
  await page
    .locator(inputSelector)
    .locator('xpath=following-sibling::span[contains(@class,"toggle-slider")]')
    .click();
}

test.describe('Diagnostic log sharing consent', () => {
  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(userEmail, 'Dee', 'Cee');
  });

  test.afterAll(async () => {
    await deleteUser(userEmail);
  });

  test('V2: starts off, and turning it on persists across a navigation', async ({
    page,
  }) => {
    await loginV2(page);
    await page.goto('/portalv2/account');

    const toggle = page.locator('#diagnostics-consent');
    // Anchor on something that must be present before asserting the absence of a tick --
    // "not checked" is satisfied just as happily by a page that never rendered.
    await expect(toggle).toHaveCount(1);
    await expect(toggle).not.toBeChecked();

    await flipToggle(page, '#diagnostics-consent');
    await expect(toggle).toBeChecked();

    // Leave and come back. The switch can only still be on if the POST was accepted and the
    // gateway stored it -- this is the assertion that the immediate-apply path actually works.
    await page.goto('/portalv2/dashboard');
    await page.goto('/portalv2/account');
    await expect(page.locator('#diagnostics-consent')).toBeChecked();

    // And withdrawing has to work the same way round, which is the direction that matters.
    await flipToggle(page, '#diagnostics-consent');
    await expect(page.locator('#diagnostics-consent')).not.toBeChecked();
    await page.goto('/portalv2/dashboard');
    await page.goto('/portalv2/account');
    await expect(page.locator('#diagnostics-consent')).not.toBeChecked();
  });

  test('V1: the same control exists and behaves the same way', async ({
    page,
  }) => {
    await loginV1(page);
    await page.locator('#nav-account').click();

    const toggle = page.locator('#acc-diagnostics-consent');
    await expect(toggle).toHaveCount(1);
    await expect(toggle).not.toBeChecked();

    await flipToggle(page, '#acc-diagnostics-consent');
    await expect(toggle).toBeChecked();

    // Navigate rather than reload: the login URL carries a one-time token, and reloading it
    // re-runs the magic-link exchange instead of restoring the session.
    await page.goto('/admin');
    await page.locator('#nav-account').click();
    await expect(page.locator('#acc-diagnostics-consent')).toBeChecked();

    await flipToggle(page, '#acc-diagnostics-consent');
    await expect(page.locator('#acc-diagnostics-consent')).not.toBeChecked();
    await page.goto('/admin');
    await page.locator('#nav-account').click();
    await expect(page.locator('#acc-diagnostics-consent')).not.toBeChecked();
  });
});
