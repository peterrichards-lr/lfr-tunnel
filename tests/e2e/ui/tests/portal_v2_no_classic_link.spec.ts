import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V1, V2 } from './utils/portal';

/**
 * The V2 sidebar footer no longer carries a "Use Classic Dashboard" link (#1622). Since #2528 the
 * only way into V1 is the Admin Zone's "Classic portal (V1)" link, shown to admins alone.
 *
 * Every assertion here is an absence, which passes perfectly well on a page that failed to
 * render, so each one is anchored on sidebar content that must be present.
 */
const adminEmail = 'admin@lfr-demo.local';

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto(`${V2}/`);
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard`);
}

test.describe('V2 sidebar has no Classic Dashboard link', () => {
  test('the footer keeps its other links but not this one', async ({
    page,
  }) => {
    await loginV2(page);

    // Anchors: the footer rendered, and the links that stay are still there.
    await expect(
      page.getByRole('link', { name: /Privacy Policy/i }),
    ).toBeVisible();
    await expect(page.getByRole('link', { name: /Cookies/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /Sign Out/i })).toBeVisible();

    await expect(
      page.getByRole('link', { name: /Classic Dashboard/i }),
    ).toHaveCount(0);
    // Belt and braces: no footer anchor points at V1 by href either.
    const v1Hrefs = await page.$$eval(
      '.sidebar-footer a',
      (els, v1) =>
        els
          .map((e) => e.getAttribute('href') || '')
          .filter((h) => h === v1 || h.startsWith(v1 + '/')),
      V1,
    );
    expect(v1Hrefs).toEqual([]);
  });

  test('the Admin Zone is the one way back to V1, for an admin', async ({
    page,
  }) => {
    await loginV2(page);
    const links = page.locator(`.sidebar a[href="${V1}"]`);
    await expect(links).toHaveCount(1);
    await expect(page.getByTestId('classic-portal-link')).toHaveText(
      'Classic portal (V1)',
    );
  });
});
