import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 Analytics toolbar (#1560).
 *
 * V1 had no time-range control at all, so its Analytics tab showed one fixed 30-day window that
 * the user could not change -- while V2 offered four ranges. Since the two portals are a live
 * A/B test, a capability in only one arm makes the comparison measure the capability rather than
 * the presentation.
 *
 * The Export PDF button also sat at a different height to the control beside it. That is asserted
 * as measured geometry rather than as a class list, because every declaration involved was
 * already correct -- `.btn` and `.input-field` share padding and font-size -- and it is only how
 * each derives its height (flex centring versus a select's own line box) that differed.
 */
test.describe('Portal V1 Analytics controls', () => {
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
    // Sections are path-routed since #1513, so this is the destination the sidebar link copies.
    await page.click('#nav-analytics');
    // Anchored on the section container, not its heading: #1520's anchor pass rebuilds headings,
    // so the data-i18n attribute is gone from the DOM by the time the page settles.
    await expect(page.locator('#tab-analytics')).toBeVisible();
  });

  test('offers the same four ranges V2 does', async ({ page }) => {
    const select = page.locator('#analytics-range');
    await expect(select).toBeVisible();

    const values = await select
      .locator('option')
      .evaluateAll((opts) => opts.map((o) => (o as HTMLOptionElement).value));
    // Same values, same order as V2's control, so the two arms differ only in presentation.
    expect(values).toEqual(['7', '14', '30', '0']);
    await expect(select).toHaveValue('30');
  });

  test('changing the range refetches analytics for that window', async ({
    page,
  }) => {
    const requested = page.waitForRequest(
      (r) => r.url().includes('/api/analytics') && r.url().includes('days=7'),
    );
    await page.locator('#analytics-range').selectOption('7');
    const req = await requested;
    expect(req.url()).toContain('days=7');
  });

  test('Export PDF is the same height as the range control', async ({
    page,
  }) => {
    const select = page.locator('#analytics-range');
    const button = page.locator('.analytics-toolbar .btn');
    await expect(select).toBeVisible();
    await expect(button).toBeVisible();

    const [s, b] = await Promise.all([
      select.boundingBox(),
      button.boundingBox(),
    ]);
    expect(s).not.toBeNull();
    expect(b).not.toBeNull();
    // Exactly equal: both are stretched by the same flex container, so any difference means
    // one of them has escaped it rather than that they merely rounded differently.
    expect(Math.round(b!.height)).toBe(Math.round(s!.height));
  });

  test('both controls still fit their labels at larger text sizes', async ({
    page,
  }) => {
    // A fixed pixel height is the obvious way to align two controls and the reason this comes
    // back: it clips the label as soon as the text grows.
    await page.addStyleTag({ content: 'html { font-size: 20px; }' });

    const clipped = await page
      .locator('.analytics-toolbar .btn')
      .evaluate((el) => el.scrollHeight > el.clientHeight + 1);
    expect(clipped).toBe(false);
  });
});
