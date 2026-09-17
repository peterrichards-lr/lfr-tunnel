import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Every admin nav item must be clickable, not merely present (#1984).
 *
 * Reported from production: the sidebar "never scrolls beyond Magic Links" with a mouse, so
 * Database Backups, Gateway Maintenance and System Settings could only be reached by keyboard.
 *
 * The cause was `.sidebar-section-content { max-height: 500px; overflow: hidden }`. V2 puts all
 * fourteen admin links in ONE section, which is taller than that, so the tail was clipped. V1
 * escapes it by splitting the same links across three sections, each under the limit -- so this
 * is a V2-only defect hiding behind a shared class name.
 *
 * WHY THE OBVIOUS TESTS MISS IT, and why this one hit-tests:
 *
 *  - maintenance_parity.spec.ts and its siblings navigate by URL and assert the SCREEN renders.
 *    That proves the capability exists -- it does -- and says nothing about reaching it.
 *    Parity of capability is not parity of access.
 *  - The first version of THIS spec compared getBoundingClientRect() against the sidebar's box
 *    and passed against the bug. A rect is the element's LAYOUT position; it is unchanged by an
 *    ancestor clipping it, so rect maths can never see `overflow: hidden`.
 *  - Keyboard access is not evidence either: focusing a clipped child scrolls an
 *    `overflow: hidden` box programmatically, which is exactly why this shipped -- it worked
 *    for anyone who tabbed and failed for everyone who scrolled.
 *
 * document.elementFromPoint IS the question a user asks: if I click here, what do I hit?
 */
const adminEmail = 'admin@lfr-demo.local';

const ADMIN_TAIL = [
  { href: '/portalv2/admin/backups', label: 'Database Backups' },
  { href: '/portalv2/admin/maintenance', label: 'Gateway Maintenance' },
  { href: '/portalv2/admin/settings', label: 'System Settings' },
];

async function loginV2(page: any, email: string) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
  // Layout renders a spinner with NO nav while it loads, so evaluating straight after the URL
  // settles measures an empty shell. An earlier run of this spec did exactly that and failed
  // for the wrong reason.
  await page.waitForSelector('.sidebar-menu', { timeout: 20000 });
}

test.describe('Sidebar reachability — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    // Short on purpose: the nav must overflow for this to mean anything, and a tall CI window
    // would hide the defect.
    await page.setViewportSize({ width: 1280, height: 700 });
    await loginV2(page, adminEmail);
  });

  test('no admin section clips its own contents', async ({ page }) => {
    const clipped = await page.evaluate(() => {
      const out: string[] = [];
      document.querySelectorAll('.sidebar-section-content').forEach((el) => {
        const s = el as HTMLElement;
        const style = getComputedStyle(s);
        // A section taller than its own clipping box hides whatever is past the edge, and no
        // amount of scrolling the MENU brings it back -- the clip is inside the section.
        if (
          style.overflow !== 'visible' &&
          s.scrollHeight > s.clientHeight + 1
        ) {
          out.push(
            `${s.className}: content ${s.scrollHeight}px in a ${s.clientHeight}px box ` +
              `(max-height ${style.maxHeight}, overflow ${style.overflow})`,
          );
        }
      });
      return out;
    });

    expect(
      clipped,
      'a sidebar section is clipping its own links, so the tail is unreachable by mouse',
    ).toEqual([]);
  });

  test('the last three admin items are actually clickable', async ({
    page,
  }) => {
    for (const item of ADMIN_TAIL) {
      const link = page.locator(`.sidebar-menu a[href="${item.href}"]`);

      // PREMISE: it is in the DOM at all. Without this the hit-test below could "fail" for a
      // missing link and be mistaken for a clipping bug.
      await expect(
        link,
        `${item.label} is missing from the sidebar`,
      ).toHaveCount(1);

      // Scroll the designated scroller the way a wheel would -- NOT scrollIntoView, which
      // would scroll the clipping ancestor and hide the very defect under test.
      await page.evaluate(() => {
        const menu = document.querySelector('.sidebar-menu') as HTMLElement;
        menu.scrollTop = menu.scrollHeight;
      });

      const hit = await page.evaluate((href: string) => {
        const el = document.querySelector(
          `.sidebar-menu a[href="${href}"]`,
        ) as HTMLElement;
        const r = el.getBoundingClientRect();
        const x = r.left + r.width / 2;
        const y = r.top + r.height / 2;
        const atPoint = document.elementFromPoint(x, y);
        return {
          hitsTheLink: !!atPoint && (atPoint === el || el.contains(atPoint)),
          // offsetParent is null when an ancestor is display:none; a clipped element still has
          // one, so this distinguishes "hidden" from "clipped" in the failure message.
          hasLayout: el.offsetParent !== null,
          rectTop: Math.round(r.top),
          rectBottom: Math.round(r.bottom),
          viewportHeight: window.innerHeight,
        };
      }, item.href);

      expect(
        hit.hitsTheLink,
        `${item.label} is not what a click at its own centre would hit ` +
          `(rect ${hit.rectTop}-${hit.rectBottom} in a ${hit.viewportHeight}px viewport, ` +
          `hasLayout=${hit.hasLayout}) -- it is present but clipped`,
      ).toBe(true);
    }

    // And the user-facing claim, end to end.
    await page
      .locator('.sidebar-menu a[href="/portalv2/admin/settings"]')
      .click();
    await page.waitForURL('**/portalv2/admin/settings');
  });
});
