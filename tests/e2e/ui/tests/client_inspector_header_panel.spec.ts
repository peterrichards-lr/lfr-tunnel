import { test, expect } from './utils/fixtures';

/**
 * The Inspector's status row belongs inside .header-panel (#1791).
 *
 * pkg/client/dashboard.html carried two surplus `</div>`: one directly after the maintenance
 * toggle row and one below the panel. The first closed `.header-panel` early, so the row
 * holding "Listening on localhost:4040" and the four state pills was reparented up to
 * `<sidebar>` -- outside the panel's 20px padding and below its bottom border. The second was
 * simply dropped on the floor by the parser. Neither produced an error, and the file's own
 * indentation said the opposite of what rendered.
 *
 * This is a real layout change, so it is asserted geometrically rather than by class name:
 * `toHaveClass`-style checks were true throughout the bug's entire life.
 */
test.describe('Client Inspector header panel', () => {
  test('the status row sits inside the header panel, within its padding', async ({
    page,
  }) => {
    await page.goto('http://localhost:4040/');

    // Positive anchors. Everything below is a containment assertion, and a blank page
    // satisfies the negative half of those.
    const panel = page.locator('.header-panel');
    await expect(panel).toBeVisible();
    await expect(panel.locator('h1')).toContainText('Inspector');

    const listening = page.locator('[data-i18n="client_listening"]');
    await expect(listening).toBeVisible();

    // The structural claim. Before the fix this count was 0: the row was a sibling of the
    // panel, not a descendant.
    await expect(
      page.locator('.header-panel [data-i18n="client_listening"]'),
    ).toHaveCount(1);

    // The maintenance toggle above it stayed inside all along, so it is the control that
    // tells "the panel moved" apart from "the row moved".
    await expect(page.locator('.header-panel #maint-toggle')).toHaveCount(1);

    // Geometry, because .header-panel { padding: 20px } is the whole visible difference.
    const panelBox = (await panel.boundingBox())!;
    const rowBox = (await page
      .locator('.header-panel [data-i18n="client_listening"]')
      .boundingBox())!;
    expect(panelBox).not.toBeNull();
    expect(rowBox).not.toBeNull();

    // Inset by the panel's padding rather than flush against the sidebar edge (x was 0).
    expect(rowBox.x).toBeGreaterThan(panelBox.x + 10);
    // Above the panel's bottom border rather than below it.
    expect(rowBox.y + rowBox.height).toBeLessThanOrEqual(
      panelBox.y + panelBox.height,
    );
  });

  test('the tab bar is still a sibling of the panel, not swallowed by it', async ({
    page,
  }) => {
    // The counterpart. Removing a surplus closing tag can just as easily pull the NEXT
    // section inside the panel, and the fix would look correct in the spec above either way.
    await page.goto('http://localhost:4040/');

    await expect(page.locator('#tab-traffic')).toBeVisible();
    await expect(page.locator('.header-panel #tab-traffic')).toHaveCount(0);
    await expect(
      page.locator('.header-panel .access-control-panel'),
    ).toHaveCount(0);

    const panelBox = (await page.locator('.header-panel').boundingBox())!;
    const tabBox = (await page.locator('#tab-traffic').boundingBox())!;
    expect(tabBox.y).toBeGreaterThanOrEqual(panelBox.y + panelBox.height - 2);
  });
});
