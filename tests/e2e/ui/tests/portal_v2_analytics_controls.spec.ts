import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 Analytics controls (#1293).
 *
 * The time-range select carried .input-field's 12px vertical padding underneath .h-control's
 * fixed 38px height, which left about 4px less room than a 15px line needs -- so its label
 * was clipped top and bottom.
 *
 * Asserted as geometry rather than as a class list, because every class involved was applied
 * correctly; it is only their combination that did not fit.
 */
test.describe('Portal V2 Analytics controls', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
    await expect(page.getByRole('heading', { name: /Analytics/i })).toBeVisible();
  });

  test('the time-range select is tall enough for its own text', async ({ page }) => {
    const select = page.getByLabel('Time range');
    await expect(select).toBeVisible();

    const fits = await select.evaluate((el) => {
      const style = getComputedStyle(el);
      const px = (v: string) => parseFloat(v) || 0;
      // "normal" line-height has no computed pixel value; 1.2x the font size is the usual
      // approximation and is what a select's own text occupies.
      const line =
        style.lineHeight === 'normal' ? px(style.fontSize) * 1.2 : px(style.lineHeight);
      const needed = px(style.paddingTop) + px(style.paddingBottom) + line;
      return {
        needed,
        height: el.getBoundingClientRect().height,
        padding: `${style.paddingTop}/${style.paddingBottom}`,
        fontSize: style.fontSize,
      };
    });

    expect(
      fits.height,
      `select is ${fits.height}px but its ${fits.fontSize} text plus ${fits.padding} padding needs ${fits.needed}px`,
    ).toBeGreaterThanOrEqual(fits.needed - 0.5);
  });

  test('the select sits level with the button beside it', async ({ page }) => {
    const selectBox = await page.getByLabel('Time range').boundingBox();
    const buttonBox = await page.getByRole('button', { name: /Export PDF/i }).boundingBox();
    expect(selectBox).not.toBeNull();
    expect(buttonBox).not.toBeNull();
    if (!selectBox || !buttonBox) return;

    // Top edges, not heights. An earlier version of this test asserted equal heights, which
    // fails for a reason that has nothing to do with the bug: the button contains an emoji,
    // and its line box -- so its height -- depends on which emoji font the machine has. It
    // measures 63px in the test container and something else elsewhere. Tying the select to
    // that number would make this test a font check.
    const detail = `select ${selectBox.height}px @y${selectBox.y}, button ${buttonBox.height}px @y${buttonBox.y}`;
    expect(Math.abs(selectBox.y - buttonBox.y), detail).toBeLessThanOrEqual(2);
  });
});
