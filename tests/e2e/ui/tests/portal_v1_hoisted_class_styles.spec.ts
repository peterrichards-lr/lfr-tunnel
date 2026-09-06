import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1's hoisted class rules (#1752).
 *
 * Fifteen classes named a shape and styled nothing, because their appearance sat in an inline
 * `style` on every element that used them. #1752 moved those declarations into the rules and
 * deleted them from the markup -- roughly sixty elements, so the risk is not that a class stays
 * inert, it is that one of them now renders differently.
 *
 * `make check-css` cannot see any of that: it asks whether a rule EXISTS, and an empty rule
 * would satisfy it. So these assert the COMPUTED style of a real element in a real browser --
 * the value the reader actually gets -- and each one fails if its rule is deleted.
 *
 * Note dashboard.css is //go:embed-ed, so nothing here is meaningful until the image is rebuilt
 * (`docker compose up -d --build`). A run against a stale container measures the old stylesheet
 * and reports it as a pass.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

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

const styleOf = (locator: any, prop: string) =>
  locator.evaluate(
    (el: Element, p: string) => getComputedStyle(el).getPropertyValue(p),
    prop,
  );

test.describe('Portal V1 hoisted class rules', () => {
  /**
   * The regression the buttons hoist could most easily have caused, and the reason
   * `.login-card .btn` exists. .btn is the login card's own button -- full width, stacked --
   * and `.btn-secondary, .btn-outline { width: auto }` would otherwise shrink the one variant
   * button still living there. Asserted against its plain-.btn sibling rather than a pixel
   * count, so it holds at any viewport.
   */
  test('a variant button on the login card is still full width', async ({
    page,
  }) => {
    await page.goto('/admin');

    const plain = page.locator('#btn-show-email'); // .btn
    const variant = page.locator('#btn-show-register'); // .btn.btn-outline
    await expect(plain).toBeVisible();
    await expect(variant).toBeVisible();

    const plainBox = await plain.boundingBox();
    const variantBox = await variant.boundingBox();
    expect(plainBox).not.toBeNull();
    expect(variantBox).not.toBeNull();
    expect(variantBox!.width).toBeCloseTo(plainBox!.width, 0);

    // And it keeps the stacked rhythm: .btn's 12px, not .btn-secondary/.btn-outline's 0.
    expect(await styleOf(variant, 'margin-bottom')).toBe('12px');
  });

  /**
   * The other half of the same rule: away from the login card a variant button is inline-sized,
   * which used to be `style="width: auto; margin: 0"` repeated thirty-odd times.
   */
  test('a variant button on the dashboard is inline-sized', async ({
    page,
  }) => {
    await loginV1(page);
    await page.click('#nav-reservations');

    const btn = page.locator('#btn-generate-subdomain'); // .btn.btn-secondary
    await expect(btn).toBeVisible();
    expect(await styleOf(btn, 'margin-bottom')).toBe('0px');

    // width: auto, so it is as wide as its label. Without the rule .btn's `width: 100%`
    // applies and the flex row hands it most of the space it shares with two siblings.
    const btnBox = await btn.boundingBox();
    const rowBox = await btn.locator('xpath=..').boundingBox();
    expect(btnBox).not.toBeNull();
    expect(rowBox).not.toBeNull();
    expect(btnBox!.width).toBeLessThan(rowBox!.width / 2);
  });

  /** .form-group carried its own margin-bottom on all four uses; 20px is now the rule. */
  test('a form group has the stacked-field margin', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-system');
    const group = page.locator('#tab-system .form-group').first();
    await expect(group).toBeVisible();
    expect(await styleOf(group, 'margin-bottom')).toBe('20px');
  });

  /**
   * switchInstallerTab() used to set four inline properties per click. It now toggles one class
   * per element, so this asserts the CSS actually moves the underline and swaps the panel --
   * the thing the inline writes were doing by hand.
   */
  test('the installer tabs switch by class, not by inline style', async ({
    page,
  }) => {
    await loginV1(page);
    // The guide is only reachable from the CLI banner on Overview; dashboard.spec.ts covers
    // that route, this covers what the tabs do once it is open.
    await page.click('#nav-overview');
    await expect(page.locator('#cli-client-banner-container')).toBeVisible();
    await page.click('button:has-text("Other Operating Systems")');
    await expect(page.locator('#installer-guide-modal')).toBeVisible();

    const macTab = page.locator('#tab-btn-macos');
    const winTab = page.locator('#tab-btn-windows');
    const macPanel = page.locator('#tab-content-macos');
    const winPanel = page.locator('#tab-content-windows');

    // Which tab opens is decided by the user agent, and playwright.config.ts pins that to
    // Windows -- so start from an explicit click rather than from whichever tab the runner's
    // platform happens to select.
    await macTab.click();

    // Anchored on presence first: an absence-only check passes on a blank modal.
    await expect(macPanel).toBeVisible();
    await expect(winPanel).toBeHidden();
    const activeBorder = await styleOf(macTab, 'border-bottom-color');
    expect(activeBorder).not.toBe('rgba(0, 0, 0, 0)');
    expect(await styleOf(winTab, 'border-bottom-color')).toBe(
      'rgba(0, 0, 0, 0)',
    );

    await winTab.click();

    await expect(winPanel).toBeVisible();
    await expect(macPanel).toBeHidden();
    expect(await styleOf(winTab, 'border-bottom-color')).toBe(activeBorder);
    expect(await styleOf(macTab, 'border-bottom-color')).toBe(
      'rgba(0, 0, 0, 0)',
    );
  });

  /**
   * modal-header / modal-title / modal-close were the three the issue flagged as needing a
   * design decision. The markup was V2's; only the rules were missing, so the close button had
   * been rendering as a bare user-agent <button> stacked under the heading.
   */
  test('the keyboard-shortcuts overlay has a real header row', async ({
    page,
  }) => {
    await loginV1(page);
    await page.locator('#shortcuts-trigger').click();

    const overlay = page.locator('#shortcuts-overlay');
    await expect(overlay).toBeVisible();
    await expect(overlay).toContainText(/Keyboard shortcuts/i);

    const header = overlay.locator('.modal-header');
    expect(await styleOf(header, 'display')).toBe('flex');
    expect(await styleOf(header, 'justify-content')).toBe('space-between');

    // Title and close sit on ONE row: the defect was them stacking.
    const title = overlay.locator('.modal-title');
    const close = overlay.locator('.modal-close');
    const titleBox = await title.boundingBox();
    const closeBox = await close.boundingBox();
    expect(titleBox).not.toBeNull();
    expect(closeBox).not.toBeNull();
    expect(closeBox!.x).toBeGreaterThan(titleBox!.x);
    expect(Math.abs(closeBox!.y - titleBox!.y)).toBeLessThan(
      titleBox!.height + closeBox!.height,
    );

    // `.modal h3` would otherwise keep its 16px and lift the title out of the centred row.
    expect(await styleOf(title, 'margin-bottom')).toBe('0px');

    // Chromeless, not a user-agent button.
    expect(await styleOf(close, 'background-color')).toBe('rgba(0, 0, 0, 0)');
    expect(await styleOf(close, 'border-top-width')).toBe('0px');
  });

  /** The dashed underline is the only thing telling a reader the timestamp has a tooltip. */
  test('a timestamp still signals that it is hoverable', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-users');
    await page.click(`a:has-text("${adminEmail}")`);
    await expect(page.locator('#user-details-modal')).toBeVisible();

    const stamp = page
      .locator('#detail-user-joined .timestamp-tooltip')
      .first();
    await expect(stamp).toBeVisible();
    expect(await styleOf(stamp, 'cursor')).toBe('help');
    expect(await styleOf(stamp, 'border-bottom-style')).toBe('dashed');
  });

  /** Both dropdown triggers inlined the same flex layout; the class carries it now. */
  test('the language dropdown trigger lays out as a row', async ({ page }) => {
    await loginV1(page);

    const trigger = page.locator('#portal-custom-dropdown-trigger');
    await expect(trigger).toBeVisible();
    expect(await styleOf(trigger, 'display')).toBe('flex');
    expect(await styleOf(trigger, 'align-items')).toBe('center');
    expect(await styleOf(trigger, 'border-radius')).toBe('6px');
  });
});
