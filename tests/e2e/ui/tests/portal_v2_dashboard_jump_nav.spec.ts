import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 Overview jump navigation (#1218).
 *
 * The Overview runs to roughly five screens and ScrollToTopButton only appears once you
 * are already down it, so there was no way to see how much page remained or to reach a
 * section directly.
 *
 * The assertion that earns its place is the second one: every nav entry resolves to an
 * element that exists. A jump nav is exactly the kind of thing that rots silently -- a
 * panel gets renamed, moved into its own page or conditionally rendered, and the link
 * survives as a dead anchor that scrolls nowhere and reports no error. Checking the
 * targets by reading each href, rather than against a list hardcoded here, means a nav
 * entry added later is covered without anyone remembering to update this file.
 */
test.describe('Portal V2 Overview jump navigation', () => {
  const userEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', userEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(userEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  test('the nav is present and labelled for assistive tech', async ({
    page,
  }) => {
    const nav = page.getByRole('navigation', { name: 'Dashboard sections' });
    await expect(nav).toBeVisible();
    // Six sections: Active Tunnels, Reservations Overview, Registered Subdomains,
    // Custom Domains, Custom Domain Status, Personal Access Tokens.
    await expect(nav.locator('a')).toHaveCount(6);
  });

  test('every nav entry points at a section that exists', async ({ page }) => {
    const nav = page.getByRole('navigation', { name: 'Dashboard sections' });

    // evaluateAll resolves once against whatever is in the DOM at that instant -- it does not
    // auto-wait like a web-first assertion does. Without this the read could land before the nav
    // rendered, return [], and fail on the length check below while the nav was perfectly fine
    // (#1564). The test above passes in the same situation because toHaveCount retries.
    await expect(nav.locator('a').first()).toBeAttached();

    const hrefs = await nav
      .locator('a')
      .evaluateAll((links) => links.map((l) => l.getAttribute('href') || ''));

    expect(hrefs.length).toBeGreaterThan(0);

    for (const href of hrefs) {
      expect(href, 'every jump-nav entry should be a fragment link').toMatch(
        /^#.+/,
      );
      // Counted rather than asserted visible: Custom Domain Status renders only when the
      // account has a custom domain, so the target can legitimately be off-screen or
      // collapsed. What must never happen is the element being absent entirely.
      const target = page.locator(href);
      await expect(
        target,
        `jump-nav entry ${href} has no matching element on the page`,
      ).toHaveCount(1);
    }
  });

  test('the nav label matches the heading of the section it points at', async ({
    page,
  }) => {
    // Two names for one panel is what #1209 had to fix, and a nav is where that drift
    // shows up first: the label is written in one file and the heading in another.
    const nav = page.getByRole('navigation', { name: 'Dashboard sections' });

    for (const name of ['Active Tunnels', 'Reservations Overview']) {
      await expect(nav.getByRole('link', { name })).toHaveCount(1);
      await expect(
        page.getByRole('heading', { name }),
        `"${name}" appears in the jump nav but not as a section heading`,
      ).toHaveCount(1);
    }
  });

  test('clicking an entry scrolls the page to that section', async ({
    page,
  }) => {
    const nav = page.getByRole('navigation', { name: 'Dashboard sections' });
    const target = page.locator('#access-tokens');

    // Measured on the target rather than on a scroll container's scrollTop. Which element
    // actually scrolls is not obvious here: .main-content carries overflow-y: auto and
    // index.css says it is the scroll container, but its scrollTop stays 0 while the
    // section plainly moves -- so at this viewport the document is scrolling instead.
    // Asserting on the target works either way, and is what the user actually cares about.
    const before = await target.boundingBox();
    expect(before).not.toBeNull();
    expect(
      before!.y,
      'Personal Access Tokens should start below the fold, or this test proves nothing',
    ).toBeGreaterThan(page.viewportSize()!.height);

    await nav.getByRole('link', { name: 'Personal Access Tokens' }).click();
    await expect(page).toHaveURL(/#access-tokens$/);

    // scroll-behavior: smooth means the position is animated, so poll until it settles
    // rather than sampling once mid-flight.
    await expect
      .poll(async () => Math.round((await target.boundingBox())!.y))
      .toBeLessThan(page.viewportSize()!.height);

    // Not asserting it reaches the top: Personal Access Tokens is the LAST section, so the
    // scroll bottoms out and the section stops partway up. An earlier version of this test
    // asserted y < viewport/2 and failed at y=426 of 720 for exactly that reason -- the
    // browser refusing to scroll past the end, not a broken anchor.
    const after = await target.boundingBox();
    expect(after!.y).toBeLessThan(before!.y);
  });
});
