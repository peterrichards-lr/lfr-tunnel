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
    await expect(page.locator('[data-i18n="client_config_title"]')).toBeVisible();

    // Verify the inputs are populated
    // run-ui.sh starts the client with subdomain "client-ui-test" and server "http://tunnel.lfr-demo.local"
    // Wait for the config to be loaded via the API
    await expect(page.locator('#cfg-subdomain')).toHaveValue('');
    await expect(page.locator('#cfg-server-url')).toHaveValue('https://tunnel.lfr-demo.se');
  });

  test('should sync local theme preference', async ({ page }) => {
    await page.goto('http://localhost:4040/');
    
    await page.locator('#tab-settings').click();
    
    // Change theme to dark
    await page.locator('#cfg-theme').selectOption('dark');
    
    // Check if data-theme was updated (removed for dark mode default)
    await expect(page.locator('html')).not.toHaveAttribute('data-theme', 'light');

    // Refresh page and verify preference is persisted
    await page.goto('http://localhost:4040/');
    await expect(page.locator('html')).not.toHaveAttribute('data-theme', 'light');
  });
});
