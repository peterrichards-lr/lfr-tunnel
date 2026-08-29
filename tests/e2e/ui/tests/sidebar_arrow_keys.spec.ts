import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Arrow-key movement in the sidebar, in both portals (#1562).
 *
 * Deliberately additive: every nav item keeps its place in the tab order. The usual
 * roving-tabindex pattern collapses the menu to one Tab stop, which suits a menubar but takes
 * something away from a plain list of links. So the tests assert both halves -- that arrows move
 * focus, AND that Tab still reaches individual items, because a regression toward roving tabindex
 * would satisfy the first while quietly breaking the second.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

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

const focusedText = (page: any) =>
  page.evaluate(() => (document.activeElement?.textContent || '').trim());

for (const portal of ['V1', 'V2'] as const) {
  test.describe(`${portal} sidebar arrow keys`, () => {
    test.beforeEach(async ({ page }) => {
      if (portal === 'V1') await loginV1(page);
      else await loginV2(page);
    });

    test('ArrowDown and ArrowUp move between nav items', async ({ page }) => {
      const items = page.locator('nav.sidebar .nav-item');
      await items.first().focus();
      const first = await focusedText(page);
      expect(first.length).toBeGreaterThan(0);

      await page.keyboard.press('ArrowDown');
      const second = await focusedText(page);
      expect(second).not.toBe(first);

      await page.keyboard.press('ArrowUp');
      expect(await focusedText(page)).toBe(first);
    });

    test('Home and End jump to the ends of the menu', async ({ page }) => {
      const items = page.locator('nav.sidebar .nav-item');
      await items.first().focus();
      const first = await focusedText(page);

      await page.keyboard.press('End');
      const last = await focusedText(page);
      expect(last).not.toBe(first);

      await page.keyboard.press('Home');
      expect(await focusedText(page)).toBe(first);
    });

    test('Tab still reaches nav items individually', async ({ page }) => {
      // The regression this guards: switching to roving tabindex would make arrows work while
      // removing every item but one from the tab order, which is a loss for anyone already
      // tabbing the sidebar.
      const items = page.locator('nav.sidebar .nav-item');
      await items.first().focus();
      const first = await focusedText(page);

      await page.keyboard.press('Tab');
      const afterTab = await focusedText(page);
      expect(
        afterTab,
        'Tab from the first nav item should land on another focusable thing, not skip the menu',
      ).not.toBe(first);

      const stillInNav = await page.evaluate(
        () => !!document.activeElement?.closest('nav.sidebar'),
      );
      expect(stillInNav, 'the sidebar should not be a single tab stop').toBe(
        true,
      );
    });

    test('arrow keys outside the sidebar are left alone', async ({ page }) => {
      // Scoping matters: a document-level handler would hijack arrows inside inputs and selects.
      await page.evaluate(() => {
        const i = document.createElement('input');
        i.id = 'arrow-probe';
        i.value = 'abc';
        document.body.appendChild(i);
        i.focus();
        i.setSelectionRange(3, 3);
      });

      await page.keyboard.press('ArrowUp');

      const stillFocused = await page.evaluate(
        () => document.activeElement?.id === 'arrow-probe',
      );
      expect(
        stillFocused,
        'pressing an arrow in a text field must not move focus into the sidebar',
      ).toBe(true);
    });
  });
}
