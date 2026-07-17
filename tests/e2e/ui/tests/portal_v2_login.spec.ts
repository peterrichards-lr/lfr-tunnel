import { test, expect } from '@playwright/test';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

test.describe('Portal v2 Login Flow', () => {
  const adminEmail = 'admin@lfr-demo.local';

  test.beforeEach(async () => {
    await clearMailpit();
  });

  test('React SPA Magic Link Login', async ({ page }) => {
    // 1. Trigger Magic Link from new React SPA
    await page.goto('/portalv2/');
    
    // Check if we are on the login screen
    await expect(page.locator('h1', { hasText: 'Welcome Back' })).toBeVisible();

    // Fill email
    await page.fill('#email-input', adminEmail);
    
    // Click submit
    await page.click('button[type="submit"]');
    
    // Check for success message (case-insensitive or exact text matching what Login.tsx uses)
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    // 2. Get token from Mailpit
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();

    // 3. Login using Magic Link via React UI
    await page.goto(`/portalv2/login?token=${token}`);
    
    // It should verify the token and redirect to /portalv2/dashboard
    await page.waitForURL('**/portalv2/dashboard');
    await expect(page.locator('h2', { hasText: 'Dashboard' })).toBeVisible();
  });
});
