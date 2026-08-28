import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 sidebar accessibility (#1212).
 *
 * Every nav item used to be a <div onclick>, so none of them appeared in the accessibility
 * tree as a control: unreachable by Tab, unannounced by a screen reader, and the whole
 * portal therefore unnavigable without a mouse.
 *
 * These assert the semantics rather than the appearance, because a future refactor could
 * quietly turn a link back into a div and nothing else in the suite would notice -- the
 * existing dashboard spec clicks these by id, which passes either way.
 */
test.describe('Portal V1 sidebar accessibility', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page, context }) => {
    await clearMailpit();
    await context.addInitScript(() => {
      window.localStorage.setItem('v2_promo_dismissed', 'true');
    });

    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test('nav items are links, not generic elements', async ({ page }) => {
    // The exact failure reported: these showed up as `generic "Reservations"` rather than
    // `link`, so assistive technology had nothing to offer the user.
    for (const id of [
      'nav-overview',
      'nav-tokens',
      'nav-reservations',
      'nav-tunnels',
    ]) {
      const item = page.locator(`#${id}`);
      await expect(item).toHaveRole('link');
      // A real, resolvable href -- that is what makes it a link to assistive technology,
      // and what middle-click and "Copy link address" need. The shape changed from '#users'
      // to '/admin/users' in #1513; what #1212 established is that there IS one.
      await expect(item).toHaveAttribute('href', /^\/(admin|portal)\/[a-z-]+$/);
    }

    // Logout acts rather than navigates, so it is deliberately a button.
    await expect(page.locator('.nav-item:has-text("Logout")')).toHaveRole(
      'button',
    );
  });

  test('the sidebar is a navigation landmark', async ({ page }) => {
    await expect(
      page.getByRole('navigation', { name: 'Primary' }),
    ).toBeVisible();
  });

  test('a nav item can be focused and activated by keyboard alone', async ({
    page,
  }) => {
    const tokens = page.locator('#nav-tokens');
    await tokens.focus();
    await expect(tokens).toBeFocused();

    await page.keyboard.press('Enter');
    await expect(page.locator('#tab-tokens')).toBeVisible();
  });

  test('the current section is announced, not only coloured', async ({
    page,
  }) => {
    await page.click('#nav-tunnels');
    await expect(page.locator('#nav-tunnels')).toHaveAttribute(
      'aria-current',
      'page',
    );
    // And it moves, rather than accumulating on every visited item.
    await expect(page.locator('#nav-overview')).not.toHaveAttribute(
      'aria-current',
      'page',
    );
  });

  test('collapsible section headers report their state', async ({ page }) => {
    const header = page.locator('.sidebar-section-header').first();
    await expect(header).toHaveRole('button');
    await expect(header).toHaveAttribute('aria-expanded', 'true');

    await header.click();
    await expect(header).toHaveAttribute('aria-expanded', 'false');

    await header.click();
    await expect(header).toHaveAttribute('aria-expanded', 'true');
  });

  test('account settings fields have programmatic names', async ({ page }) => {
    // 52 of 55 fields had none (#1216): captions were visual text only, so a screen
    // reader announced an unlabelled edit box. getByLabel resolves via <label for>,
    // a wrapping <label>, aria-label or aria-labelledby -- i.e. exactly the association
    // that was missing, rather than the visible text next to it.
    await page.click('#nav-account');
    await expect(page.locator('h2:has-text("Account Settings")')).toBeVisible();

    for (const name of [
      'First Name',
      'Last Name',
      'Preferred Name',
      'Theme Preference',
    ]) {
      await expect(
        page.getByLabel(name, { exact: false }).first(),
      ).toBeVisible();
    }
  });

  test('fields without a visible label still carry an accessible name', async ({
    page,
  }) => {
    // These are named by aria-label rather than <label for>, because their caption is not
    // a <label> element. A placeholder is a hint, not a name, so it does not count.
    await page.click('#nav-tokens');
    await expect(page.locator('#token-name')).toHaveAttribute(
      'aria-label',
      /.+/,
    );
    await expect(page.locator('#token-expiry')).toHaveAttribute(
      'aria-label',
      /.+/,
    );
  });

  test('a hidden sidebar does not keep its links focusable', async ({
    page,
  }) => {
    // The regression this change could have introduced: making 16 items focusable while
    // the sidebar can be visually hidden means a keyboard user tabs into something they
    // cannot see. Hidden here has to mean hidden from the tab order too.
    await page.setViewportSize({ width: 400, height: 800 });
    await expect(page.locator('#nav-overview')).toBeHidden();
  });
});
