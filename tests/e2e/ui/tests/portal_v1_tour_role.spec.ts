import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The V1 onboarding tour must not walk a non-admin through admin-only navigation (#1291).
 *
 * The step list was a single fixed array. One step highlights #nav-analytics, which lives
 * inside #admin-sidebar-group and is hidden for anyone who is not admin or owner -- so a
 * plain user was walked to an element that is not on screen, and the accompanying
 * showTab('analytics') is bounced back to overview by the role guard from #1289.
 *
 * Every other V1 spec signs in as the owner, so nothing exercised the non-admin tour at
 * all. This uses View As to get a genuine non-admin session without inventing a fixture
 * user -- setViewAs reloads, so currentUser.role really is 'user' afterwards.
 */
test.describe('Portal V1 onboarding tour respects role', () => {
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
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  // Reads the step total out of driver.js's own progress text ("1 of 8"), which is the
  // only externally visible signal of how many steps the tour was built with.
  async function tourStepTotal(page: import('@playwright/test').Page) {
    await page.evaluate(() => (window as any).startTutorial(true));
    const progress = page.locator('.driver-popover-progress-text');
    await expect(progress).toBeVisible({ timeout: 15000 });
    const text = (await progress.textContent()) || '';
    const m = text.match(/of\s+(\d+)/i);
    expect(m, `could not parse a step total from "${text}"`).not.toBeNull();
    return Number(m![1]);
  }

  test('an owner gets the admin steps, a plain user does not', async ({
    page,
  }) => {
    const asOwner = await tourStepTotal(page);
    await page.keyboard.press('Escape');

    // View As reloads, so the tour is rebuilt against the previewed role.
    await page.selectOption('#view-as-select', 'user');
    await expect(page.locator('#view-as-bar')).toContainText(
      'Previewing as user',
    );
    await expect(page.locator('#admin-sidebar-group')).toBeHidden();

    const asUser = await tourStepTotal(page);

    // Exactly one step is admin-only today (Analytics). The assertion is the difference,
    // not the absolute totals, so adding an ordinary step to the tour does not break this.
    expect(asUser).toBe(asOwner - 1);
  });

  // Deliberately no "the tour never highlights a hidden element" test. I wrote one and it
  // passed against the unfixed build, because driver.js does not mark a hidden target as
  // .driver-active-element -- so it asserted nothing. One test that demonstrably fails
  // without the fix is worth more than two where one is decorative.
});
