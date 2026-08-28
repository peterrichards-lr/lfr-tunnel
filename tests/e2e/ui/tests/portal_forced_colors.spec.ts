import { test, expect } from '@playwright/test';

/**
 * Windows High Contrast Mode (#1521).
 *
 * forced-colors is not "a high contrast theme": the browser replaces the author's palette with
 * the user's system colours, and while doing so drops background-image and box-shadow. Both
 * portals use gradients for their primary and danger buttons and shadows for several edges, so
 * without a media block those controls become labels on the system canvas with no boundary --
 * present, focusable, and invisible.
 *
 * Two things this spec learned the hard way, both worth keeping:
 *
 * `test.use({ forcedColors: 'active' })` silently does NOT apply here -- the project spreads
 * devices['Desktop Chrome'] -- and the result was matchMedia returning false while every test
 * passed, reading ordinary styles. Hence page.emulateMedia() and an explicit guard.
 *
 * And the elements have to be ones the rules actually change. Chromium's UA stylesheet already
 * gives a real <button> a forced-colors border, so asserting on the sign-in button passed with
 * the CSS deleted. Measured against the stylesheet, .btn-primary and .modal-content go from
 * `0px none` to `1px solid` -- those are the rules doing work, so those are what is asserted.
 */
test.describe('High Contrast Mode', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ forcedColors: 'active' });
  });

  async function assertEmulated(page: any) {
    const active = await page.evaluate(
      () => matchMedia('(forced-colors: active)').matches,
    );
    expect(
      active,
      'forced-colors is not being emulated, so this test would pass against ordinary styles',
    ).toBeTruthy();
  }

  // Injected rather than hunted for on a page: the RULE is under test, the real stylesheet in
  // real forced-colors mode answers for it, and nothing depends on which controls a given
  // screen happens to render.
  async function boundaryOf(page: any, className: string, tag = 'div') {
    return page.evaluate(
      ([cls, t]: [string, string]) => {
        const el = document.createElement(t);
        el.className = cls;
        document.body.appendChild(el);
        const cs = getComputedStyle(el);
        const out = {
          width: cs.borderTopWidth,
          style: cs.borderTopStyle,
          shadow: cs.boxShadow,
        };
        el.remove();
        return out;
      },
      [className, tag],
    );
  }

  async function expectBoundary(
    page: any,
    cls: string,
    tag: string,
    what: string,
  ) {
    const b = await boundaryOf(page, cls, tag);
    expect(
      parseFloat(b.width) > 0 && b.style !== 'none',
      `${what} has no border under forced-colors, so it has no visible edge: ${JSON.stringify(b)}`,
    ).toBeTruthy();
  }

  test('Portal V2: the primary button and modal keep a visible edge', async ({
    page,
  }) => {
    await page.goto('/portalv2/');
    await assertEmulated(page);
    // Its fill is a gradient, which the browser drops, and the rule sets `border: none`.
    await expectBoundary(page, 'btn btn-primary', 'button', 'V2 .btn-primary');
    // Its only edge is a box-shadow, which the browser also drops.
    await expectBoundary(page, 'modal-content', 'div', 'V2 .modal-content');
  });

  test('Portal V1: the primary button and modal keep a visible edge', async ({
    page,
  }) => {
    await page.goto('/admin');
    await assertEmulated(page);
    await expectBoundary(page, 'btn btn-primary', 'button', 'V1 .btn-primary');
    await expectBoundary(page, 'modal-content', 'div', 'V1 .modal-content');
  });

  test('Portal V2: the QR frame opts out, so the code still scans', async ({
    page,
  }) => {
    await page.goto('/portalv2/');
    await assertEmulated(page);

    // The frame only appears mid-MFA-setup, which needs a session and several steps, so the
    // rule is exercised directly.
    const computed = await page.evaluate(() => {
      const el = document.createElement('div');
      el.className = 'qr-frame';
      document.body.appendChild(el);
      const cs = getComputedStyle(el);
      const out = {
        adjust: cs.forcedColorAdjust,
        background: cs.backgroundColor,
      };
      el.remove();
      return out;
    });

    expect(
      computed.adjust,
      'the QR frame must opt out of forced colours, or the code can invert and stop scanning',
    ).toBe('none');
    // Opting out only helps if it stays light: a QR needs a light quiet zone to be read.
    expect(computed.background).toBe('rgb(255, 255, 255)');
  });
});
