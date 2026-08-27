import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 section URLs (#1215).
 *
 * Filed as "sidebar sections have no bookmarkable URLs". They do -- V1 routes on the
 * fragment, so /portal#users has always worked. The report tested *path* segments,
 * /portal/users, which were never routes.
 *
 * But two real defects sat underneath that false headline, and both are covered here:
 *
 *   - /portal/users matched no control plane handler and fell through to the data-plane
 *     ProxyHandler, which has no lease for a control-domain host. It answered with the
 *     "Developer Environment Offline" page -- telling an admin their environment was down
 *     because they typed the URL shape Portal V2 taught them. That error is why the
 *     original reporter concluded no URLs existed at all, and why this issue was then
 *     investigated from scratch twice.
 *
 *   - An unknown fragment rendered nothing. Confirmed against a running build before any
 *     fix: '#nonsense' gave visible-tabs=[] nav-active=[]. Not merely an empty content
 *     area -- no sidebar item was highlighted either, so there was no cue about where you
 *     were or that anything had gone wrong. A stale bookmark to a renamed section landed
 *     you in a portal that simply looked broken.
 *
 * The first group below is the part the issue claimed was missing, asserted so it cannot
 * quietly stop being true -- which is how the Inspector lost its log viewer in #783.
 */
test.describe('Portal V1 section URLs', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  async function signIn(page: import('@playwright/test').Page) {
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
  }

  // Which section is on screen, read the same way showTab decides what to hide, so the
  // test cannot pass by looking at an element showTab does not manage.
  async function visibleSection(page: import('@playwright/test').Page) {
    return page
      .locator('.main-content > div[id^="tab-"]:not(.hidden)')
      .evaluateAll((els) => els.map((e) => e.id.slice(4)));
  }

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test('a fragment URL deep-links to its section and survives a reload', async ({
    page,
  }) => {
    await page.goto('/admin#users');
    await page.reload();
    await expect.poll(() => visibleSection(page)).toEqual(['users']);

    // The reload is the assertion that matters: it is what "bookmarkable" means, and a
    // same-document hash change would prove nothing.
    await page.goto('/admin#blacklist');
    await page.reload();
    await expect.poll(() => visibleSection(page)).toEqual(['blacklist']);
  });

  test('back and forward move between sections, not just the URL', async ({
    page,
  }) => {
    await page.goto('/admin#users');
    await expect.poll(() => visibleSection(page)).toEqual(['users']);

    await page.click('#nav-blacklist');
    await expect.poll(() => visibleSection(page)).toEqual(['blacklist']);

    await page.goBack();
    await expect(page).toHaveURL(/#users$/);
    await expect.poll(() => visibleSection(page)).toEqual(['users']);

    await page.goForward();
    await expect(page).toHaveURL(/#blacklist$/);
    await expect.poll(() => visibleSection(page)).toEqual(['blacklist']);
  });

  test('an unknown fragment falls back to the overview instead of rendering nothing', async ({
    page,
  }) => {
    for (const hash of ['#nonsense', '#tokens-renamed']) {
      await page.goto(`/admin${hash}`);
      await page.reload();

      await expect
        .poll(() => visibleSection(page), {
          message: `${hash} should fall back to the overview, not render an empty portal`,
        })
        .toEqual(['overview']);

      // The sidebar has to agree, or the page renders content with nothing highlighted
      // and still looks broken.
      await expect(page.locator('#nav-overview')).toHaveClass(/active/);

      // And the address bar is corrected, so a reload lands where the URL says.
      await expect(page).toHaveURL(/#overview$/);
    }
  });

  test('an unknown fragment adds no history entry to go back to', async ({
    page,
  }) => {
    // replaceState, not pushState -- otherwise Back from a corrected URL returns to the
    // broken one and the user bounces.
    await page.goto('/admin#users');
    await expect.poll(() => visibleSection(page)).toEqual(['users']);

    await page.evaluate(() => {
      window.location.hash = '#nonsense';
    });
    await expect.poll(() => visibleSection(page)).toEqual(['overview']);

    await page.goBack();
    await expect.poll(() => visibleSection(page)).toEqual(['users']);
  });

  test('a path URL redirects to its section rather than the offline page', async ({
    page,
  }) => {
    // The shape Portal V2 teaches. Before this it answered 404 carrying offline.html.
    const res = await page.goto('/admin/users');
    expect(res?.ok()).toBeTruthy();

    await expect(page).toHaveURL(/#users$/);
    await expect.poll(() => visibleSection(page)).toEqual(['users']);
    await expect(page.locator('body')).not.toContainText('Environment Offline');
  });

  test('a path URL for a section that does not exist still lands somewhere real', async ({
    page,
  }) => {
    const res = await page.goto('/admin/not-a-section');
    expect(res?.ok()).toBeTruthy();
    await expect.poll(() => visibleSection(page)).toEqual(['overview']);
    await expect(page.locator('body')).not.toContainText('Environment Offline');
  });
});
