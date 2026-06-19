import { test, expect } from '@playwright/test';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

test.describe('Dashboard UI Automation', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async () => {
    // Ensure mailpit is clean before each test
    await clearMailpit();
  });

  test('Magic Link Login, Theme Toggle, and Pagination', async ({ page }) => {
    // 1. Trigger Magic Link
    await page.goto('/admin');
    
    // Show email form
    await page.click('#btn-show-email');
    
    // Fill email
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    
    // Check for success message
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    // 2. Get token from Mailpit
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();

    // 3. Login using Magic Link
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load (the body or h2 should be visible)
    await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();

    // 4. Test Theme Toggling via Account Settings
    await page.click('#nav-account');
    await expect(page.locator('h2:has-text("Account Settings")')).toBeVisible();

    const htmlElement = page.locator('html');
    
    // Change theme to dark
    await page.selectOption('#acc-theme', 'dark');
    // Save account settings
    await page.click('button:has-text("Save Changes")');
    
    // Verify success via button text
    await expect(page.locator('button:has-text("Saved!")')).toBeVisible();
    
    // Verify theme changed
    await expect(htmlElement).toHaveAttribute('data-theme', 'dark');

    // Change theme to light
    await page.selectOption('#acc-theme', 'light');
    await page.click('button:has-text("Save Changes")');
    await expect(page.locator('button:has-text("Saved!")')).toBeVisible();
    await expect(htmlElement).toHaveAttribute('data-theme', 'light');

    // 5. Check Table Pagination / Controls render
    await page.click('#nav-tunnels');
    await expect(page.locator('#tunnels-table-body-pagination')).toBeAttached();
    await expect(page.locator('#tunnels-table-body-search')).toBeVisible();
  });

  test('Admin IP Blacklist and Backups Sections', async ({ page }) => {
    // 1. Trigger Magic Link
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    // 2. Get token from Mailpit
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();

    // 3. Login using Magic Link
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load
    await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();

    // 4. Navigate to IP Blacklist
    await page.click('#nav-blacklist');
    await expect(page.locator('h2:has-text("IP Blacklist")')).toBeVisible();
    await expect(page.locator('#blacklist-table-body')).toBeVisible();

    // 5. Navigate to Database Backups
    await page.click('#nav-backups');
    await expect(page.locator('h2:has-text("Database Backups")')).toBeVisible();
    await expect(page.locator('#backups-table-body')).toBeVisible();
    await expect(page.locator('text=Restore via CLI only.')).toBeVisible();
  });
});
