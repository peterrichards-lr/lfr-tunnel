import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * #dashboard-shell must close where #1292 meant it to (#1791).
 *
 * `<div id="dashboard-shell">` was opened in #1292 and never closed. The tag labelled
 * `/#dashboard-shell` was consumed by #dashboard-screen, which had itself lost its closing
 * tag in #689's "extra closing div" removal. Browsers recover by auto-closing at `</body>`,
 * so nothing ever looked broken -- but the resulting DOM put every modal, #toast-container
 * and both toast live regions INSIDE a container that carries `display: none` until sign-in.
 *
 * These specs assert the DOM shape and the one user-visible consequence of it, not the
 * markup: dashboard.html is `//go:embed`ed, so only the served page proves anything. A source
 * assertion would have passed against a stale binary.
 */
test.describe('Portal V1 dashboard shell nesting', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test('the toast container is a sibling of the shell, not a child of it', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    // Positive anchors first. Every assertion below is about something NOT being nested a
    // certain way, and an empty page satisfies all of those perfectly.
    await expect(page.locator('#dashboard-shell')).toBeVisible();
    await expect(
      page.locator('#dashboard-shell #dashboard-screen'),
    ).toHaveCount(1);
    await expect(page.locator('#toast-container')).toHaveCount(1);
    await expect(page.locator('#token-modal')).toHaveCount(1);

    // The actual structure. Before the fix all of these were inside the shell.
    await expect(page.locator('#dashboard-shell #toast-container')).toHaveCount(
      0,
    );
    await expect(page.locator('#dashboard-shell #toast-live-a')).toHaveCount(0);

    // Every .modal-overlay declared after </#dashboard-screen> is now outside the shell --
    // but #edge-schedule-modal is NOT one of them. It sits inside #tab-network-health, ahead
    // of the closing tag, and always did. Asserting the exact set rather than "none" keeps
    // that legitimate one from being mistaken for a regression, and keeps an empty result
    // from passing because the selector is wrong.
    const modalsInShell = await page.$$eval(
      '#dashboard-shell .modal-overlay',
      (els) => els.map((el) => el.id).sort(),
    );
    expect(modalsInShell).toEqual(['edge-schedule-modal']);

    // ...and they are body's children, rather than having escaped somewhere else entirely.
    const parents = await page.evaluate(() =>
      ['toast-container', 'toast-live-a', 'token-modal'].map(
        (id) =>
          document.getElementById(id)?.parentElement?.tagName ?? 'MISSING',
      ),
    );
    expect(parents).toEqual(['BODY', 'BODY', 'BODY']);

    // The shell still wraps what it is for: the banners and the sidebar+content row (#1289).
    // Closing it correctly must not empty it out.
    await expect(page.locator('#dashboard-shell #view-as-bar')).toHaveCount(1);
    await expect(page.locator('#dashboard-shell #v2-promo-banner')).toHaveCount(
      1,
    );
    await expect(
      page.locator('#dashboard-shell #session-expiry-banner'),
    ).toHaveCount(1);
  });

  test('the layout is unchanged: the shell still frames the whole viewport', async ({
    page,
  }) => {
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

    // #dashboard-shell is styled (display:flex, flex-direction:column, width:100%,
    // height:100vh), so moving elements out of it is only safe because everything moved is
    // position:fixed. Assert the shell's own box rather than trusting that reasoning.
    const shell = await page.locator('#dashboard-shell').boundingBox();
    const screen = await page.locator('#dashboard-screen').boundingBox();
    expect(shell).not.toBeNull();
    expect(screen).not.toBeNull();

    const viewport = page.viewportSize()!;
    expect(Math.round(shell!.width)).toBe(viewport.width);
    expect(shell!.x).toBe(0);
    expect(shell!.y).toBe(0);

    // The sidebar+content row is still inside it and still full width.
    expect(Math.round(screen!.width)).toBe(Math.round(shell!.width));
    expect(screen!.y).toBeGreaterThanOrEqual(shell!.y);

    // A toast raised while signed in still paints where it always did: bottom-right, fixed,
    // 24px in from both edges. Measured on the CONTAINER, not on the toast -- .toast ships
    // `transform: translateX(120%)` and only animates in when showToast adds .show 10ms
    // later, so a toast measured on arrival is legitimately off-screen and an assertion
    // against its box fails for a reason that has nothing to do with nesting.
    await page.evaluate(() =>
      (window as unknown as { showToast: (m: string) => void }).showToast(
        'shell-nesting-probe',
      ),
    );
    await expect(page.locator('#toast-container .toast')).toBeVisible();

    const anchored = await page.evaluate(() => {
      const el = document.getElementById('toast-container')!;
      const cs = getComputedStyle(el);
      const box = el.getBoundingClientRect();
      return {
        position: cs.position,
        fromRight: Math.round(window.innerWidth - box.right),
        fromBottom: Math.round(window.innerHeight - box.bottom),
      };
    });
    expect(anchored.position).toBe('fixed');
    expect(anchored.fromRight).toBe(24);
    expect(anchored.fromBottom).toBe(24);
  });

  test('a toast raised on the login screen is actually visible', async ({
    page,
  }) => {
    // The user-visible half of the bug, and the assertion the old DOM cannot satisfy: with
    // #toast-container inside #dashboard-shell, the container inherited `display: none` until
    // sign-in, so showToast() on the login screen painted nothing and announced nothing.
    // dashboard.js does exactly that on a bad magic link ("Magic link error: ...").
    await page.goto('/admin');

    // Anchor: we really are on the login screen, with the shell hidden. Without this the
    // toast assertion below could pass on a signed-in page and prove nothing.
    await expect(page.locator('#login-screen')).toBeVisible();
    await expect(page.locator('#dashboard-shell')).toBeHidden();

    await page.evaluate(() =>
      (window as unknown as { showToast: (m: string) => void }).showToast(
        'login-screen-toast-probe',
      ),
    );

    await expect(
      page.locator('#toast-container .toast', {
        hasText: 'login-screen-toast-probe',
      }),
    ).toBeVisible();

    // And the screen-reader announcement, which is the other half of #1520 that the nesting
    // silenced: a display:none live region is not in the accessibility tree at all.
    const announced = await page.evaluate(() =>
      ['toast-live-a', 'toast-live-b']
        .map((id) => document.getElementById(id)?.textContent ?? '')
        .join(''),
    );
    expect(announced).toContain('login-screen-toast-probe');
  });
});
