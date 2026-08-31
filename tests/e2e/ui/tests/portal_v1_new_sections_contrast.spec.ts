import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The sections added this week must respond to the accessibility mechanisms like the rest of the
 * portal (#1608).
 *
 * They were built with their styling inline, matching dashboard.html's own idiom -- 510 inline
 * declarations. That put them outside the mechanisms the earlier work depends on: forced-colors
 * (#1533), prefers-contrast (#1534) and the high-contrast theme (#1548) all act on CSS, and an
 * inline declaration cannot be overridden by them.
 *
 * The clearest case was the status dot, whose colour was the literal #10b981 -- a value
 * forced-colors cannot substitute, so it stayed green against a system palette that had replaced
 * everything around it.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('V1 new sections respond to CSS mechanisms', () => {
  test('the new sections carry no inline styling', async ({ page }) => {
    await loginV1(page);

    // Asserted in the browser rather than by grepping the source, so it also covers anything the
    // renderers inject at runtime.
    const inline = await page.evaluate(() => {
      const out: string[] = [];
      for (const sel of [
        '#tab-telemetry',
        '#tab-custom-domains',
        '.sidebar-footer-links',
      ]) {
        const root = document.querySelector(sel);
        if (!root) {
          out.push(`${sel}: MISSING`);
          continue;
        }
        root.querySelectorAll('[style]').forEach((el) => {
          const s = el.getAttribute('style') || '';
          if (s.trim())
            out.push(`${sel}: <${el.tagName.toLowerCase()}> ${s.slice(0, 60)}`);
        });
      }
      return out;
    });

    expect(inline, `inline styles found:\n${inline.join('\n')}`).toEqual([]);
  });

  test('the status dot takes its colour from a token, not a literal', async ({
    page,
  }) => {
    await loginV1(page);

    const dot = await page.evaluate(() => {
      const el = document.querySelector('.sidebar-footer-links .status-dot');
      if (!el) return null;
      return {
        hasInline: !!(el.getAttribute('style') || '').trim(),
        ariaHidden: el.getAttribute('aria-hidden'),
      };
    });

    expect(dot, 'the status dot should exist').not.toBeNull();
    // A literal colour survives forced-colors; a token does not, which is the point.
    expect(dot!.hasInline, 'the dot should be styled by CSS, not inline').toBe(
      false,
    );
    // Colour must not be the only carrier of meaning -- the link text names it.
    expect(dot!.ariaHidden).toBe('true');
  });

  test('the new sections restyle under forced colors', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-telemetry');
    await expect(page.locator('#tab-telemetry')).toBeVisible();

    const before = await page.evaluate(
      () => getComputedStyle(document.querySelector('.stat-card-value')!).color,
    );

    // page.emulateMedia rather than test.use({ forcedColors }), which silently does not apply
    // when a project spreads devices[...] -- the trap recorded in the e2e skill.
    await page.emulateMedia({ forcedColors: 'active' });

    const after = await page.evaluate(
      () => getComputedStyle(document.querySelector('.stat-card-value')!).color,
    );

    // Guard: if forced-colors did not take effect the comparison below proves nothing.
    const applied = await page.evaluate(
      () => matchMedia('(forced-colors: active)').matches,
    );
    expect(applied, 'forced-colors should be emulated').toBe(true);
    expect(after, 'the stat value should take the system colour').not.toBe(
      before,
    );
  });
});
