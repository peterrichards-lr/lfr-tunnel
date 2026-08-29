import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 modals must cover the viewport and must be dismissable (#1558).
 *
 * `.modal-backdrop` is `position: fixed`, which resolves against the viewport only while no
 * ancestor establishes a containing block. `.animate-fade-in` wraps most pages and runs
 * `fadeInUp ... both`, and `fill-mode: both` retains the final keyframe's `translateY(0)` as
 * `matrix(1, 0, 0, 1, 0, 0)` -- an identity transform with no visual effect that nonetheless
 * makes that div the containing block for every fixed descendant.
 *
 * The dashboard wrapper is ~1623px tall, so a centred dialog landed at y≈801: below the fold on
 * a 720px viewport. The backdrop was visible, the dialog was not, and with no Escape handler the
 * only way out was reloading the page.
 *
 * Asserted as geometry against the viewport rather than as "is the dialog visible", because the
 * dialog *was* rendered, styled and populated the whole time -- it was simply positioned
 * somewhere nobody could see. A visibility assertion passes on the broken build.
 */
test.describe('Portal V2 modal positioning and dismissal', () => {
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
  });

  test('the install dialog is on screen, not below the fold', async ({
    page,
  }) => {
    await page.getByRole('button', { name: /Install Client/i }).click();

    // Positive anchor first: a blank or unrendered dialog must not be able to satisfy the
    // geometry assertions that follow by rendering nothing at all.
    await expect(
      page.getByRole('heading', { name: /Client Installation Guide/i }),
    ).toBeVisible();

    const geometry = await page.evaluate(() => {
      const backdrop = document.querySelector('.modal-backdrop');
      const rect = backdrop!.getBoundingClientRect();
      return {
        backdrop: {
          top: Math.round(rect.top),
          left: Math.round(rect.left),
          width: Math.round(rect.width),
          height: Math.round(rect.height),
        },
        viewport: { width: window.innerWidth, height: window.innerHeight },
        // The portal is what makes the geometry immune to an ancestor's transform, so assert
        // it directly: the geometry could also come right by luck on a short page.
        portaledToBody: backdrop!.parentElement === document.body,
      };
    });

    expect(geometry.portaledToBody).toBe(true);
    expect(geometry.backdrop).toEqual({
      top: 0,
      left: 0,
      width: geometry.viewport.width,
      height: geometry.viewport.height,
    });
    await expect(page.getByRole('dialog')).toBeInViewport();
  });

  test('Escape closes the dialog and returns focus to the button that opened it', async ({
    page,
  }) => {
    const opener = page.getByRole('button', { name: /Install Client/i });
    await opener.click();
    await expect(page.getByRole('dialog')).toBeVisible();

    await page.keyboard.press('Escape');

    await expect(page.getByRole('dialog')).toHaveCount(0);
    // Returning focus to <body> would strand a keyboard user at the top of the document.
    await expect(opener).toBeFocused();
  });

  // The install dialog was the one reported, but the containing block belongs to the page
  // wrapper, so every modal on a wrapped page had the same defect. This checks a second,
  // independently-written dialog so a fix that only special-cased the first would fail here.
  test('a second dialog on the same page is positioned correctly too', async ({
    page,
  }) => {
    await page
      .getByRole('button', { name: /Generate (New )?Token/i })
      .first()
      .click();

    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByRole('dialog')).toBeInViewport();

    const covers = await page.evaluate(() => {
      const r = document
        .querySelector('.modal-backdrop')!
        .getBoundingClientRect();
      return (
        Math.round(r.top) === 0 &&
        Math.round(r.left) === 0 &&
        Math.round(r.width) === window.innerWidth &&
        Math.round(r.height) === window.innerHeight
      );
    });
    expect(covers).toBe(true);
  });
});
