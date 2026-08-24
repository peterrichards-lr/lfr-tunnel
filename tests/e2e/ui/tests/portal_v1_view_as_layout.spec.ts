import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 View As bar placement (#1289).
 *
 * `body` is a flex ROW, so the bar -- which sat directly inside it -- became a column beside
 * the sidebar rather than a banner above it, its contents vertically centred over the full
 * viewport. That is not cosmetic: the bar exists because a preview session is read-only, and
 * an owner who cannot tell they are in one reports the refusals as bugs (#1225).
 *
 * Asserted geometrically rather than by class name, because the CSS that broke it was
 * perfectly valid -- only the resulting box was wrong.
 */
test.describe('Portal V1 View As bar placement', () => {
  // The owner in tests/e2e/server-config.yaml, and the only role that may preview at all.
  const ownerEmail = 'admin@lfr-demo.local';

  test.beforeEach(async ({ page, context }) => {
    await clearMailpit();
    await context.addInitScript(() => {
      window.localStorage.setItem('v2_promo_dismissed', 'true');
    });

    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', ownerEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    const token = await getMagicLinkToken(ownerEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
  });

  test('the bar spans the layout and sits above the sidebar', async ({ page }) => {
    const bar = page.locator('#view-as-bar');
    await expect(bar).toBeVisible();

    const barBox = await bar.boundingBox();
    const sidebarBox = await page.locator('nav.sidebar').boundingBox();
    expect(barBox).not.toBeNull();
    expect(sidebarBox).not.toBeNull();
    if (!barBox || !sidebarBox) return;

    // Above, not beside: the bar's bottom edge must not reach past the sidebar's top edge.
    // The broken layout had them side by side, so this was the sidebar's full height.
    expect(barBox.y + barBox.height).toBeLessThanOrEqual(sidebarBox.y + 1);

    // And full width. Beside the menu it was as narrow as its own text.
    const viewportWidth = page.viewportSize()?.width ?? 0;
    expect(viewportWidth).toBeGreaterThan(0);
    expect(barBox.width).toBeGreaterThan(viewportWidth * 0.9);

    // A banner that scrolled off with the content, or one squeezed to nothing by the flex
    // container below it, would also miss the point.
    expect(barBox.height).toBeGreaterThan(20);
    expect(barBox.y).toBeLessThan(80);
  });

  test('previewing as a plain user hides the admin-only System Settings', async ({ page }) => {
    // System Settings sits in the sidebar footer rather than in #admin-sidebar-group, so it
    // was never hidden for anybody -- an owner previewing as a user still saw it, and so did
    // every real non-admin (#1289).
    await expect(page.locator('#nav-system')).toBeVisible();

    await page.selectOption('#view-as-select', 'user');

    // setViewAs reloads the page rather than patching the DOM, since every panel is
    // role-dependent.
    await expect(page.locator('#view-as-bar')).toContainText('Previewing as user');
    await expect(page.locator('#nav-system')).toBeHidden();
    await expect(page.locator('#admin-sidebar-group')).toBeHidden();

    // Hiding a link is not access control: the hash is still typeable. The panel must not
    // render either.
    await page.goto('/admin#system');
    await expect(page.locator('#tab-system')).toBeHidden();

    await page.click('#view-as-bar button:has-text("Exit preview")');
    await expect(page.locator('#nav-system')).toBeVisible();
  });

  test('the dashboard still fills the viewport below the bar', async ({ page }) => {
    const viewport = page.viewportSize();
    expect(viewport).not.toBeNull();
    if (!viewport) return;

    // The sidebar used to be a hard 100vh. Under a banner that would hang past the bottom of
    // the page, taking the content column's scroll container with it.
    const sidebarBox = await page.locator('nav.sidebar').boundingBox();
    expect(sidebarBox).not.toBeNull();
    if (!sidebarBox) return;
    expect(sidebarBox.y + sidebarBox.height).toBeLessThanOrEqual(viewport.height + 1);

    // The whole point of the column: no page-level vertical scrollbar appears just because a
    // banner is present -- the content column scrolls internally, as it did before.
    const documentOverflows = await page.evaluate(
      () => document.documentElement.scrollHeight > window.innerHeight + 1,
    );
    expect(documentOverflows).toBe(false);
  });
});
