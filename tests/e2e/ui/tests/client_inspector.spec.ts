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

  // #1419. /api/logs has been served since #584, but #783 deleted the UI that read it and
  // nothing noticed for eleven weeks -- because nothing asserted it existed. This is that
  // assertion: it fails if the tab, the container, or the endpoint wiring goes away again.
  test('should show the log viewer and read /api/logs', async ({ page }) => {
    await page.goto('http://localhost:4040/');

    const logsRequest = page.waitForRequest(
      (r) => r.url().endsWith('/api/logs'),
      { timeout: 10_000 },
    );

    await page.locator('#tab-logs').click();
    await expect(page.locator('#tab-logs')).toHaveClass(/active/);

    // The endpoint is actually called -- a tab that renders an empty pane without ever
    // fetching would be the same defect wearing a different hat.
    await logsRequest;

    await expect(page.locator('#logs-container')).toBeVisible();

    // The traffic panels are hidden in this view, the same way the Settings tab hides them.
    await expect(page.locator('#req-list')).toBeHidden();

    // Either real log lines or the explicit empty state -- both mean the view rendered.
    // Which one depends on whether this client has written to its log file yet, so asserting
    // one of them specifically would make the test depend on execution order.
    await expect(
      page
        .locator('#logs-container .log-line, #logs-container .logs-empty')
        .first(),
    ).toBeVisible();
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
});
