import { test, expect } from './utils/fixtures';

test.describe('Client Inspector UI', () => {
  test('should load without JavaScript errors', async ({ page }) => {
    await page.goto('http://localhost:4040/');

    // Wait for the Inspector title to be visible
    await expect(page.locator('h1')).toContainText('Inspector');
  });

  test('should display client configuration correctly', async ({ page }) => {
    await page.goto('http://localhost:4040/');

    // Click on the Settings tab
    await page.locator('#tab-settings').click();

    // Verify Settings tab is active
    await expect(page.locator('#tab-settings')).toHaveClass(/active/);

    // Verify configuration title is present (using data-i18n attribute)
    await expect(
      page.locator('[data-i18n="client_config_title"]'),
    ).toBeVisible();

    // These fields show what the running client is ACTUALLY using (#1211). run-ui.sh
    // starts it with `-server http://tunnel.lfr-demo.local -subdomain client-ui-test`
    // and the container has no config file, so before the fix both boxes were empty
    // while the client sat connected and serving traffic.
    //
    // The old expectation here was an empty string, with a comment naming the flag values
    // it did not assert -- the test had drifted into documenting the confusion rather than
    // catching it. Pin the resolved values instead.
    // The gateway URL is fixed by run-ui.sh's -server flag, so pin it exactly.
    await expect(page.locator('#cfg-server-url')).toHaveValue(
      'http://tunnel.lfr-demo.local',
    );

    // The subdomain is deliberately NOT pinned. Whichever client process holds port 4040
    // owns this Inspector, and the analytics spec starts its own tunnel with a generated
    // subdomain, so the exact value depends on test ordering. What matters for #1211 is
    // that the field reports the running client's value at all -- it was empty before,
    // because the panel read the saved config and this container has no subdomain in it.
    const subdomain = await page.locator('#cfg-subdomain').inputValue();
    expect(subdomain).not.toBe('');

    // Editing a field writes the saved config, which does not change the process already
    // running, so the panel has to say which value is in force.
    await expect(page.locator('.runtime-override-note').first()).toBeVisible();

    // The build-time default must still not be baked back in (#1188): the value shown is
    // the flag the client was launched with, not a compiled-in hostname.
    await expect(page.locator('#cfg-server-url')).not.toHaveValue(
      /lfr-demo\.se/,
    );
  });

  test('should sync local theme preference', async ({ page }) => {
    await page.goto('http://localhost:4040/');

    await page.locator('#tab-settings').click();

    // Change theme to dark
    await page.locator('#cfg-theme').selectOption('dark');

    // Check if data-theme was updated (removed for dark mode default)
    await expect(page.locator('html')).not.toHaveAttribute(
      'data-theme',
      'light',
    );

    // Refresh page and verify preference is persisted
    await page.goto('http://localhost:4040/');
    await expect(page.locator('html')).not.toHaveAttribute(
      'data-theme',
      'light',
    );
  });

  /**
   * The log viewer regressed out of the Inspector in #783 and nobody noticed for months
   * (#1420). /api/logs kept working and /logs stayed a valid route, so nothing errored --
   * the tab simply stopped being rendered, and no test asserted on it. This is that test.
   */
  test('the Logs tab shows the client log', async ({ page }) => {
    await page.goto('http://localhost:4040/logs');

    const tab = page.locator('#tab-logs');
    await expect(tab).toBeVisible();
    await expect(tab).toHaveClass(/active/);

    // Assert on rendered content, not just the container. An empty pane is a real state
    // here -- /api/logs 404s until the client has a subdomain -- so a test that only
    // checked the tab exists would have passed against a viewer that shows nothing.
    const viewer = page.locator('.log-viewer');
    await expect(viewer).toBeVisible();
    await expect(page.locator('.log-line').first()).toBeVisible({
      timeout: 15000,
    });

    const parsed = await page
      .locator('.log-line')
      .first()
      .evaluate((el) => ({
        hasLevel: !!el.querySelector('.log-level')?.textContent?.trim(),
        hasMessage: !!el.querySelector('.log-message')?.textContent?.trim(),
      }));
    expect(parsed.hasLevel).toBe(true);
    expect(parsed.hasMessage).toBe(true);
  });

  test('the Logs tab is reachable from the Traffic view', async ({ page }) => {
    await page.goto('http://localhost:4040/');
    await page.locator('#tab-logs').click();
    await expect(page.locator('#tab-logs')).toHaveClass(/active/);
    await expect(page.locator('.log-viewer')).toBeVisible();
  });
});
