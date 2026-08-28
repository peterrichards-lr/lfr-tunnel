import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Heading anchor links, both portals (#1520).
 *
 * The controls are deliberately invisible at rest (opacity: 0) and revealed on hover or
 * focus. That makes them the easy thing to break without noticing: a later change to
 * `display: none` would still LOOK identical in every screenshot while silently removing
 * the button from the tab order, which is the exact failure the design was chosen to
 * avoid.
 *
 * So this file drives the controls BY KEYBOARD -- tab to them, activate with Enter or
 * Space -- rather than with `.click()`, which reaches an element whether or not a
 * keyboard user ever could. A `.click()` suite would pass on the broken implementation.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

declare function changePortalLanguage(lang: string): Promise<void>;

test.describe('Portal V2 heading anchors', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  test('the heading text links to its own section', async ({ page }) => {
    const link = page.locator('a.heading-anchor-link[href="#active-tunnels"]');
    await expect(link).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/#active-tunnels$/);
  });

  test('the copy button has a real accessible name, not the glyph', async ({
    page,
  }) => {
    // getByRole matches on the accessible name, so this passing at all proves the
    // aria-label wins over the emoji -- a bare link glyph announces as "link emoji".
    const button = page.getByRole('button', {
      name: 'Copy link to Active Tunnels',
    });
    await expect(button).toHaveCount(1);
    await expect(button.locator('span')).toHaveAttribute('aria-hidden', 'true');
  });

  test('the controls are reachable and operable by keyboard alone', async ({
    page,
  }) => {
    const button = page.getByRole('button', {
      name: 'Copy link to Active Tunnels',
    });

    // Focus is what reveals it; if a future change swaps opacity for display:none this
    // focus() cannot land and the assertion below fails.
    await button.focus();
    await expect(button).toBeFocused();
    await expect(button).toHaveCSS('opacity', '1');

    // Enter, not click. Clipboard reads need permission; the toast is the observable
    // signal and is what a screen reader is given too.
    await page.keyboard.press('Enter');
    await expect(page.locator('.toast-card')).toContainText('Link copied');
  });

  test('the copy result is announced, not just painted', async ({ page }) => {
    await page
      .getByRole('button', { name: 'Copy link to Active Tunnels' })
      .focus();
    await page.keyboard.press('Enter');

    // role="status" is the live region. Painting the toast without this is the state
    // both portals shipped in before #1520: every "Copied" was silent.
    await expect(
      page.locator('[role="status"]').filter({ hasText: 'Link copied' }),
    ).toHaveCount(1);
  });

  test('copying the same link twice announces twice', async ({ page }) => {
    const button = page.getByRole('button', {
      name: 'Copy link to Active Tunnels',
    });
    await button.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#toast-live-a')).toHaveText('Link copied');
    await expect(page.locator('#toast-live-b')).toHaveText('');

    await page.keyboard.press('Enter');
    // A live region only speaks when its text CHANGES, so the second copy has to land in
    // the OTHER region or a screen reader user hears nothing the second time. Asserting
    // the message MOVED slots is what distinguishes "announced twice" from "the first
    // announcement is still sitting there".
    await expect(page.locator('#toast-live-b')).toHaveText('Link copied');
    await expect(page.locator('#toast-live-a')).toHaveText('');
  });

  test('the target of every heading link exists', async ({ page }) => {
    const hrefs = await page
      .locator('a.heading-anchor-link')
      .evaluateAll((els) =>
        els.map((e) => (e as HTMLAnchorElement).getAttribute('href') || ''),
      );
    expect(hrefs.length).toBeGreaterThan(0);
    for (const href of hrefs) {
      expect(href.startsWith('#')).toBe(true);
      await expect(page.locator(href)).toHaveCount(1);
    }
  });
});

test.describe('Portal V1 heading anchors', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test('every section heading is decorated, and links to its own fragment', async ({
    page,
  }) => {
    // Derived from the DOM rather than a list here, for the same reason showTab derives
    // its valid section set: a section added later is covered without anyone
    // remembering to update this file.
    const pairs = await page.evaluate(() =>
      Array.from(
        document.querySelectorAll('.main-content > div[id^="tab-"]'),
      ).map((section) => ({
        slug: section.id.slice(4),
        href:
          section
            .querySelector('.header h2 a.heading-anchor-link')
            ?.getAttribute('href') || null,
      })),
    );
    expect(pairs.length).toBeGreaterThan(0);
    for (const { slug, href } of pairs) {
      // A path, not a fragment: V1 routes on the path since #1513, and a heading handing
      // out '#users' would reintroduce the very gap that issue closed.
      expect(href, `section ${slug} has no heading link`).toBe(
        `/admin/${slug}`,
      );
    }
  });

  test('the copy button is keyboard-operable and announced', async ({
    page,
  }) => {
    const button = page.getByRole('button', {
      name: 'Copy link to Dashboard Overview',
    });
    await button.focus();
    await expect(button).toBeFocused();
    await expect(button).toHaveCSS('opacity', '1');

    await page.keyboard.press('Enter');
    await expect(
      page.locator('[role="status"]').filter({ hasText: 'Link copied' }),
    ).toHaveCount(1);
  });

  test('the glyph is hidden from assistive tech', async ({ page }) => {
    const button = page.getByRole('button', {
      name: 'Copy link to Dashboard Overview',
    });
    await expect(button.locator('span')).toHaveAttribute('aria-hidden', 'true');
  });

  test('the heading link still translates when the language changes', async ({
    page,
  }) => {
    // applyTranslations() assigns innerText, which would wipe the copy button out if
    // data-i18n had stayed on the <h2>. A translation pass is the only thing that
    // exercises that, and it is the kind of breakage that survives every other test.
    //
    // Called directly rather than through #portal-language-selector, because that select
    // lives inside #login-screen and is hidden the moment you sign in -- V1 offers a
    // signed-in user no way to change language at all. changePortalLanguage is the
    // function that select invokes, so this is the same code path.
    const before = await page
      .locator('#tab-overview .header h2 a.heading-anchor-link')
      .innerText();
    await page.evaluate(() => changePortalLanguage('es'));

    // The controls survived...
    await expect(
      page.locator('#tab-overview .header h2 a.heading-anchor-link'),
    ).toHaveCount(1);
    await expect(
      page.locator('#tab-overview .header h2 button.heading-anchor'),
    ).toHaveCount(1);
    // ...and the pass really ran, so this cannot pass by doing nothing.
    await expect(
      page.locator('#tab-overview .header h2 a.heading-anchor-link'),
    ).not.toHaveText(before);
    // The copy button's name interpolates the heading text, so it has to be rebuilt too
    // or it stays English in an otherwise translated portal (#1216's defect).
    await expect(
      page.locator('#tab-overview .header h2 button.heading-anchor'),
    ).not.toHaveAttribute('aria-label', `Copy link to ${before}`);
  });
});
