import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Global Broadcast must not fire without confirmation (#1202).
 *
 * handleAdminBroadcast (server.go:3968) stores the message and calls
 * BroadcastTelemetry(), so it reaches every connected portal immediately and is audited
 * against target "all". The button sat beside routine actions with nothing between the
 * click and the send.
 *
 * These assert what the request layer actually did, not that a dialog appeared. A
 * confirmation that is shown but whose "cancel" still sends is the failure worth
 * catching, and it looks identical from the DOM.
 */
test.describe('Portal V1 global broadcast confirmation', () => {
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
    await page.goto('/admin#users');
    await page.reload();
    await expect(page.locator('#admin-broadcast-input')).toBeVisible();
  });

  // Count POSTs to the broadcast endpoint, which is the only thing that matters.
  async function countBroadcasts(page: import('@playwright/test').Page) {
    const posts: string[] = [];
    await page.route('**/api/admin/broadcast', async (route) => {
      posts.push(route.request().postData() || '');
      await route.fulfill({ status: 200, body: '{"status":"ok"}' });
    });
    return posts;
  }

  test('dismissing the confirmation sends nothing', async ({ page }) => {
    const posts = await countBroadcasts(page);
    page.on('dialog', (d) => d.dismiss());

    await page.fill('#admin-broadcast-input', 'Gateway restart in 5 minutes');
    await page.click('button[data-i18n="btn_broadcast"]');
    await page.waitForTimeout(500);

    expect(posts).toHaveLength(0);
  });

  test('accepting the confirmation sends exactly the typed message', async ({
    page,
  }) => {
    const posts = await countBroadcasts(page);
    let shown = '';
    page.on('dialog', (d) => {
      shown = d.message();
      d.accept();
    });

    await page.fill('#admin-broadcast-input', 'Gateway restart in 5 minutes');
    await page.click('button[data-i18n="btn_broadcast"]');
    await expect.poll(() => posts.length).toBe(1);

    expect(JSON.parse(posts[0]).message).toBe('Gateway restart in 5 minutes');
    // The admin has to be able to see what they are about to send to everyone.
    expect(shown).toContain('Gateway restart in 5 minutes');
  });

  test('an empty box does not silently clear the live broadcast', async ({
    page,
  }) => {
    const posts = await countBroadcasts(page);
    let dialogs = 0;
    page.on('dialog', (d) => {
      dialogs += 1;
      d.accept();
    });

    await page.fill('#admin-broadcast-input', '   ');
    await page.click('button[data-i18n="btn_broadcast"]');
    await page.waitForTimeout(500);

    // Posting "" is exactly what Clear does, so the primary button used to perform a
    // clear whenever the box was empty.
    expect(posts).toHaveLength(0);
    expect(dialogs).toBe(0);
  });

  test('Clear also confirms before removing the banner', async ({ page }) => {
    const posts = await countBroadcasts(page);
    page.on('dialog', (d) => d.dismiss());

    await page.click('button[data-i18n="btn_clear"]');
    await page.waitForTimeout(500);
    expect(posts).toHaveLength(0);
  });
});
