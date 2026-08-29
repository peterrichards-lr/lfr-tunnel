import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * V1 skip navigation and landmarks (#1562).
 *
 * V2 has had a skip link since #1219 and a spec pinning it (portal_v2_landmarks). V1 had
 * neither: no skip link, and its main region was a bare <div class="main-content"> with no id,
 * no role and nothing focusable to jump to.
 *
 * So a V1 keyboard user tabbed through every sidebar link before reaching the content, on every
 * navigation — while a V2 user pressed Tab once. Under a live A/B test that is a difference in
 * how much work the portal costs to use, not a cosmetic one.
 *
 * Mirrors portal_v2_landmarks deliberately, so the two arms are held to one standard.
 */
test.describe('Portal V1 landmarks and skip navigation', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test('the page exposes navigation and main landmarks', async ({ page }) => {
    await expect(
      page.getByRole('navigation', { name: 'Primary' }),
    ).toBeVisible();
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('a skip link is the first thing a keyboard user reaches', async ({
    page,
  }) => {
    await page.keyboard.press('Tab');
    const skip = page.getByRole('link', { name: /skip to content/i });
    await expect(skip).toBeFocused();
  });

  test('the skip link is focusable, not merely present', async ({ page }) => {
    // display:none and visibility:hidden both remove an element from the tab order. A skip link
    // styled either way looks correct in the markup and is unreachable by the only people it
    // exists for, so this asserts it can actually take focus.
    const skip = page.getByRole('link', { name: /skip to content/i });
    await skip.focus();
    await expect(skip).toBeFocused();

    // And that focusing it reveals it, rather than leaving it off-screen where a sighted
    // keyboard user cannot tell what they have landed on.
    const box = await skip.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
  });

  test('following the skip link actually skips the sidebar', async ({
    page,
  }) => {
    // Asserted on where the NEXT Tab lands, because that is the user-facing property: after
    // skipping, tabbing must continue past the sidebar rather than re-enter it.
    //
    // Worth knowing what this does NOT prove. Removing tabindex="-1" from the target leaves
    // every test here passing -- Chromium sets the sequential focus navigation starting point
    // from the fragment link on its own. So this guards the skip link's effect, not the
    // attribute; an assertion on document.activeElement would have looked stricter and proved
    // no more.
    const skip = page.getByRole('link', { name: /skip to content/i });
    await skip.focus();
    await page.keyboard.press('Enter');
    await page.keyboard.press('Tab');

    const landedInSidebar = await page.evaluate(
      () => !!document.activeElement?.closest('nav.sidebar'),
    );
    expect(
      landedInSidebar,
      'after skipping, the next Tab should continue past the sidebar, not re-enter it',
    ).toBe(false);
  });
});
