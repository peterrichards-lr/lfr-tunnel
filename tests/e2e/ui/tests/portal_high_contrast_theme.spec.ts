import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The high-contrast theme, in both portals (#1538).
 *
 * It exists for readers who want maximum contrast by choice rather than because their operating
 * system says so -- `prefers-contrast` (#1534) already covers the latter. It is held to AAA, 7:1,
 * by scripts/check-theme-contrast.cjs, which is where the numbers are proven; this spec proves
 * the thing is actually reachable and renders, which no amount of measuring a CSS file can show.
 *
 * Both portals, because they share one set of theme files (#1522) and an accessibility feature in
 * one arm of an A/B test is a difference between the arms.
 */
test.describe('High contrast theme', () => {
  const adminEmail = 'admin@lfr-demo.local';

  async function signInV1(page: any) {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  }

  test('Portal V1 offers it, applies it, and its tokens resolve', async ({
    page,
  }) => {
    await signInV1(page);
    await page.click('#nav-account');

    await expect(
      page.locator('#acc-theme option[value="high-contrast"]'),
    ).toHaveCount(1);

    // V1 applies a theme on SAVE rather than on change -- unlike language (#1541), it is still a
    // form field there. Driving it as a person would means clicking Save.
    await page.selectOption('#acc-theme', 'high-contrast');
    await page.click('#btn-save-account');

    await expect(page.locator('html')).toHaveAttribute(
      'data-theme',
      'high-contrast',
    );

    // Applied is not the same as styled: the attribute is set either way, and only a served,
    // linked stylesheet makes the tokens resolve.
    const bg = await page.evaluate(() =>
      getComputedStyle(document.documentElement)
        .getPropertyValue('--bg-base')
        .trim(),
    );
    // Matched loosely on purpose: the V2 bundler minifies #000000 to #000, and asserting the
    // spelling rather than the colour would fail for a reason that has nothing to do with the
    // theme reaching the portal.
    expect(
      bg.replace(/\s/g, ''),
      'the high-contrast stylesheet did not reach Portal V1',
    ).toMatch(/^(#000{1,3}|#000000|rgb\(0,0,0\))$/);
  });

  test('Portal V1 keeps it across a reload', async ({ page }) => {
    await signInV1(page);
    await page.click('#nav-account');
    await page.selectOption('#acc-theme', 'high-contrast');
    await page.click('#btn-save-account');
    await expect(page.locator('html')).toHaveAttribute(
      'data-theme',
      'high-contrast',
    );
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute(
      'data-theme',
      'high-contrast',
    );
  });

  test('Portal V2 offers it and its tokens resolve', async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/account');

    const select = page
      .locator('select')
      .filter({ has: page.locator('option[value="high-contrast"]') })
      .first();
    await expect(select, 'Portal V2 does not offer the theme').toHaveCount(1);

    await select.selectOption('high-contrast');
    await expect(page.locator('html')).toHaveAttribute(
      'data-theme',
      'high-contrast',
    );

    const bg = await page.evaluate(() =>
      getComputedStyle(document.documentElement)
        .getPropertyValue('--bg-base')
        .trim(),
    );
    // Matched as a colour, not a spelling: V2's bundler minifies #000000 to #000, and asserting
    // the literal would fail for a reason unrelated to the theme reaching the portal.
    expect(
      bg.replace(/\s/g, ''),
      'the high-contrast stylesheet is not in the V2 bundle',
    ).toMatch(/^(#000|#000000|rgb\(0,0,0\))$/);
  });
});
