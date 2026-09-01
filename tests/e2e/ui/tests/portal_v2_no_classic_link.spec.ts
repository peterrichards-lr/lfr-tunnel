import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The V2 sidebar no longer carries a "Use Classic Dashboard" link (#1622) -- the promo banner is
 * the route back to V1 now.
 *
 * Every assertion here is an absence, which passes perfectly well on a page that failed to
 * render, so each one is anchored on sidebar content that must be present.
 */
const adminEmail = 'admin@lfr-demo.local';

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
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
    // Belt and braces: no sidebar anchor points at V1 by href either. Matched exactly rather
    // than by prefix, because every V2 nav link lives under /portalv2/ and would match a prefix.
    const v1Hrefs = await page.$$eval('.sidebar a', (els) =>
      els
        .map((e) => e.getAttribute('href') || '')
        .filter((h) => h === '/portal' || h === '/portal/'),
    );
    expect(v1Hrefs).toEqual([]);
  });

  test('the promo banner still offers the way back to V1', async ({ page }) => {
    // Removing the sidebar link makes the banner the only in-app route to V1, so it has to work.
    await loginV2(page);
    // Note the trailing slash -- the banner links to /portal/, not /portal.
    const back = page.getByRole('link', { name: /Switch back to V1/i });
    await expect(back).toBeVisible();
    await expect(back).toHaveAttribute('href', '/portal/');
  });
});
