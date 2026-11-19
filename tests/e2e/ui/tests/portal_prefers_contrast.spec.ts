import { test, expect } from '@playwright/test';
import { V2, signInToV1 } from './utils/portal';

/**
 * prefers-contrast: more (#1521), part 2.
 *
 * Honours the reader's operating-system setting with no action from them. It raises contrast
 * WITHIN whichever theme is active rather than replacing it, so a deliberate theme choice is
 * still honoured -- it just stops being subtle.
 *
 * The overrides live in the shared theme files, so both portals get them from one place. That is
 * only possible because #1522 merged the two copies, and it is why this spec checks both: if the
 * shared files ever stop reaching one portal, this fails rather than the difference going
 * unnoticed.
 *
 * Emulated with page.emulateMedia() and guarded, for the reason recorded in the forced-colors
 * spec: `test.use()` silently does not apply in this project, and the tests then pass against
 * ordinary styles while proving nothing.
 */
test.describe('prefers-contrast: more', () => {
  async function readMuted(page: any) {
    return page.evaluate(() =>
      getComputedStyle(document.documentElement)
        .getPropertyValue('--text-muted')
        .trim(),
    );
  }

  async function assertEmulated(page: any) {
    const active = await page.evaluate(
      () => matchMedia('(prefers-contrast: more)').matches,
    );
    expect(
      active,
      'prefers-contrast is not being emulated, so this test would pass against ordinary styles',
    ).toBeTruthy();
  }

  for (const [portal, url] of [
    ['Portal V2', `${V2}/`],
    ['Portal V1', ''],
  ] as const) {
    test(`${portal}: muted text is strengthened`, async ({ page }) => {
      if (url) {
        await page.goto(url);
      } else {
        // V1 is served to an admin session only (#2528), so this is the admin's portal and
        // carries their saved theme -- high-contrast, if an earlier spec left it there, where
        // this override is already in force. Pinned to the default the signed-out page had.
        await signInToV1(page);
        await page.evaluate(() =>
          document.documentElement.setAttribute('data-theme', 'dark'),
        );
      }
      const before = await readMuted(page);
      expect(before, 'the theme tokens did not load at all').toBeTruthy();

      await page.emulateMedia({ contrast: 'more' });
      await assertEmulated(page);
      const after = await readMuted(page);

      expect(
        after,
        `${portal} does not change --text-muted under prefers-contrast; ` +
          `the shared theme files are not reaching this portal`,
      ).not.toBe(before);
    });
  }
});
