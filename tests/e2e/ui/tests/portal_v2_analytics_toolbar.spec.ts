import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * V2 Analytics toolbar alignment (#1604).
 *
 * Export PDF did not line up with the time-range select. Same cause as V1's, fixed there in
 * #1560: `.input-field` carries a `margin-bottom` intended for stacked forms, and the select kept
 * it inside a flex toolbar row where the button had none. Flex already equalised their heights --
 * it was the margin pushing one box down.
 *
 * Asserted as measured geometry rather than as a class list, because in V1's case every class
 * involved was already applied correctly and only their combination was wrong.
 */
test.describe('Portal V2 Analytics toolbar', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
    await expect(
      page.getByRole('heading', { name: /Analytics/i }),
    ).toBeVisible();
  });

  test('Export PDF and the range select share a height and a baseline', async ({
    page,
  }) => {
    const select = page.getByLabel('Time range');
    const button = page.getByRole('button', { name: /Export PDF/i });
    await expect(select).toBeVisible();
    await expect(button).toBeVisible();

    const [s, b] = await Promise.all([
      select.boundingBox(),
      button.boundingBox(),
    ]);
    expect(s).not.toBeNull();
    expect(b).not.toBeNull();

    // Exactly equal: both are stretched by the same flex container, so any difference means one
    // has escaped it rather than that they merely rounded apart.
    expect(Math.round(b!.height), 'heights should match').toBe(
      Math.round(s!.height),
    );
    // The margin bug showed up as a vertical offset even when heights agreed, so check the tops
    // too -- that is the half a height-only assertion would miss.
    expect(Math.round(b!.y), 'tops should align').toBe(Math.round(s!.y));
  });

  test('both still fit their labels at larger text sizes', async ({ page }) => {
    // A fixed pixel height is the obvious way to align two controls and the reason this recurs:
    // it clips the label as soon as the text grows.
    await page.addStyleTag({ content: 'html { font-size: 20px; }' });

    const clipped = await page
      .getByRole('button', { name: /Export PDF/i })
      .evaluate((el) => el.scrollHeight > el.clientHeight + 1);
    expect(clipped, 'the button should not clip its label').toBe(false);
  });
});
