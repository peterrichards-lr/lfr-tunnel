import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 promo banner must not overlay the header controls (#1200).
 *
 * `#v2-promo-banner` used to be `position: fixed; top: 0; z-index: 9999`, so it took no
 * space in flow and painted over `#sidebar-toggle-btn` and `#header-language-container`,
 * which both sit at `top: 16px` with `z-index: 100`. The toggle was therefore unclickable
 * at every viewport -- and on a phone that toggle is the only route back to the nav, so
 * users were stranded on whichever page they loaded.
 *
 * Crucially these tests do NOT set `v2_promo_dismissed`. Every other Portal V1 spec
 * dismisses the banner in `beforeEach`, which removes the very element that caused the
 * bug -- that is why the suite stayed green while the portal was broken for every
 * first-time user. Keep the banner shown here, or this file stops testing anything.
 */
test.describe('Portal V1 promo banner does not overlay header controls', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();

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

    // The whole point of this file: prove the banner is actually on screen.
    await expect(page.locator('#v2-promo-banner')).toBeVisible();
  });

  test('the banner sits in flow rather than floating over the page', async ({
    page,
  }) => {
    // A fixed banner is what made this overlap possible at all. Asserting the computed
    // position keeps a future "just bump the z-index" fix from silently reintroducing it.
    const position = await page
      .locator('#v2-promo-banner')
      .evaluate((el) => getComputedStyle(el).position);
    expect(position).not.toBe('fixed');

    // In flow means it occupies vertical space, so the controls start below it.
    const banner = await page.locator('#v2-promo-banner').boundingBox();
    const toggle = await page.locator('#sidebar-toggle-btn').boundingBox();
    expect(banner).not.toBeNull();
    expect(toggle).not.toBeNull();
    expect(toggle!.y).toBeGreaterThanOrEqual(banner!.y + banner!.height);
  });

  for (const [label, width, height] of [
    ['mobile', 400, 800],
    ['desktop', 1280, 900],
  ] as const) {
    test(`header controls are hit-testable at ${label} width`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height });

      // These tests sign in as an admin, and an admin with view-as capability gets a
      // 44px #view-as-bar above #dashboard-screen. That bar pushes .main-content down
      // far enough that the toggle lands ~2px clear of the banner, which masked this
      // bug entirely: the suite measured the one account shape that happens not to
      // overlap. Ordinary users have no such bar, .main-content starts at y=0, and the
      // toggle sits squarely behind the banner. Reproduce the ordinary-user geometry.
      await page.evaluate(() => {
        const bar = document.getElementById('view-as-bar');
        if (bar) bar.style.display = 'none';
      });

      // The sidebar slides off-canvas over 0.3s at <=1024px. Until that transition
      // lands it still sits over the toggle, so measuring immediately reports the
      // sidebar as the occluder and the test fails for a reason that is not the bug.
      // The mobile rules end at visibility:hidden, which is what we wait for.
      if (width <= 1024) {
        await expect(page.locator('.sidebar')).toBeHidden();
      }

      // elementFromPoint is the assertion that actually caught this. `toBeVisible()`
      // passed throughout the bug: the button was 32x32, display:flex, opacity:1 -- it
      // was simply painted over, which only hit-testing reveals.
      for (const id of ['sidebar-toggle-btn', 'header-language-container']) {
        const onTop = await page.evaluate((elementId) => {
          const el = document.getElementById(elementId);
          if (!el) return 'missing';
          const b = el.getBoundingClientRect();
          const top = document.elementFromPoint(
            b.x + b.width / 2,
            b.y + b.height / 2,
          );
          if (!top) return 'nothing';
          return el === top || el.contains(top)
            ? 'self'
            : `blocked by ${top.id || top.tagName}`;
        }, id);
        expect(onTop, `${id} at ${label} width`).toBe('self');
      }
    });
  }

  test('the sidebar toggle actually opens the nav on a phone viewport', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 400, height: 800 });

    const sidebar = page.locator('.sidebar');
    await expect(sidebar).not.toHaveClass(/active/);

    // Playwright's actionability check fails a click that another element intercepts,
    // so this alone would have failed before the fix.
    await page.click('#sidebar-toggle-btn');

    await expect(sidebar).toHaveClass(/active/);
    await expect(page.locator('#nav-tokens')).toBeVisible();
  });

  test('dismissing the banner reclaims its space and keeps the toggle usable', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 400, height: 800 });

    const before = await page.locator('#sidebar-toggle-btn').boundingBox();
    await page.click('#v2-promo-banner button');
    await expect(page.locator('#v2-promo-banner')).toBeHidden();

    const after = await page.locator('#sidebar-toggle-btn').boundingBox();
    expect(after!.y).toBeLessThan(before!.y);

    await page.click('#sidebar-toggle-btn');
    await expect(page.locator('.sidebar')).toHaveClass(/active/);
  });
});
