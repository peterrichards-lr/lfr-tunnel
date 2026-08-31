import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The sidebar must stay on screen, and every admin entry must be reachable (#1598).
 *
 * Reported as "System Settings, Database Backups and Gateway Maintenance are missing, and the
 * sidebar does not scroll -- the wheel scrolls the page instead".
 *
 * Measured cause: the row holding the sidebar and the main content grows to fit the content --
 * 1806px on a dashboard in a 600px window -- so the document scrolls. The sidebar was an ordinary
 * block in that row, so it scrolled away with everything else and its lower entries sat below the
 * fold. Pointing at it and using the wheel scrolled the document because that is what was
 * genuinely under the pointer.
 *
 * Worth recording what this is NOT: an earlier attempt blamed a missing `min-height: 0` on
 * `.sidebar-menu`. Removing that again changed nothing -- the menu could already scroll
 * (scrollHeight 666 > clientHeight 276). Mutation testing caught the wrong diagnosis before it
 * shipped, which is why these assertions are about position rather than overflow.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml
const SHORT = { width: 1280, height: 600 };

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
  // Wait for the dashboard to actually render. waitForURL only proves navigation started, and
  // measuring before the content exists reports a page that does not overflow -- which made the
  // precondition below fail on a correctly-behaving page.
  await expect(
    page.getByRole('heading', { name: /Dashboard Overview/i }),
  ).toBeVisible();
}

test.describe('V2 sidebar stays on screen', () => {
  test('the sidebar does not scroll away with the page', async ({ page }) => {
    await page.setViewportSize(SHORT);
    await loginV2(page);

    // Precondition: the page really is taller than the window here. Without this the test would
    // pass on a short dashboard where nothing could scroll away in the first place.
    // documentElement, not body: body is a flex container here and reports the viewport height,
    // which would make this precondition quietly measure nothing.
    const pageHeight = await page.evaluate(() =>
      Math.max(
        document.documentElement.scrollHeight,
        document.querySelector('.main-content')?.scrollHeight ?? 0,
      ),
    );
    expect(
      pageHeight,
      'the dashboard should overflow the window at this height',
    ).toBeGreaterThan(SHORT.height);

    await page.evaluate(() => window.scrollTo(0, 400));
    await page.waitForTimeout(200);

    const after = await page.evaluate(() => {
      const sb = document.querySelector('.sidebar')!.getBoundingClientRect();
      const foot = document
        .querySelector('.sidebar-footer')!
        .getBoundingClientRect();
      return {
        top: Math.round(sb.top),
        bottom: Math.round(sb.bottom),
        footerBottom: Math.round(foot.bottom),
        viewport: window.innerHeight,
      };
    });

    // Pinned to the top of the window rather than carried up with the content.
    expect(after.top, 'the sidebar should stick to the top').toBe(0);
    expect(
      after.bottom,
      'the whole sidebar should fit the window once scrolled',
    ).toBeLessThanOrEqual(after.viewport + 1);
    expect(
      after.footerBottom,
      'the footer should be on screen, not below the fold',
    ).toBeLessThanOrEqual(after.viewport + 1);
  });

  test('every admin entry can be brought into view', async ({ page }) => {
    await page.setViewportSize(SHORT);
    await loginV2(page);

    // The three that were reported missing. System Settings is last in the admin block, so it
    // was the first to be lost.
    for (const href of [
      '/admin/backups',
      '/admin/maintenance',
      '/admin/settings',
    ]) {
      const link = page.locator(`.sidebar-menu a[href$="${href}"]`);
      await expect(link, `${href} should exist in the menu`).toBeAttached();

      // Scroll the MENU, never the page, and reset the page scroll first. Playwright's
      // scrollIntoViewIfNeeded will happily scroll the document to satisfy itself, which passes
      // on the broken build -- confirmed by mutation testing, where this test kept passing while
      // the sidebar scrolled off the top of the window.
      const onScreen = await link.evaluate((el) => {
        window.scrollTo(0, 0);
        const menu = el.closest('.sidebar-menu') as HTMLElement;
        menu.scrollTop = el.offsetTop - menu.offsetTop;
        const r = el.getBoundingClientRect();
        return r.top >= 0 && r.bottom <= window.innerHeight;
      });
      expect(
        onScreen,
        `${href} should be reachable by scrolling the sidebar alone`,
      ).toBe(true);
    }
  });

  test('the footer puts status on its own row, policies in two columns', async ({
    page,
  }) => {
    await loginV2(page);

    const policies = page.locator('.sidebar-footer-policies');
    await expect(policies).toBeAttached();

    const columns = await policies.evaluate(
      (el) => getComputedStyle(el).gridTemplateColumns.split(' ').length,
    );
    expect(columns, 'the policy links should sit in two columns').toBe(2);

    await expect(policies.locator('a')).toHaveCount(2);
    // Status belongs to its own row above, not to the policy pair.
    await expect(policies.locator('a[href*="status"]')).toHaveCount(0);
  });
});
