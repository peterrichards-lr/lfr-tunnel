import { test, expect } from '@playwright/test';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { exec } from 'child_process';
import { promisify } from 'util';

const execAsync = promisify(exec);

test.describe('Analytics & Tunnel Automation', () => {
  const adminEmail = 'admin@lfr-demo.local'; 

  test.beforeAll(async () => {
    await clearMailpit();
  });

  test('Generate token, connect tunnel, and verify analytics', async ({ page }) => {
    // 1. Trigger Magic Link
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();

    // 2. Get token from Mailpit
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();

    // 3. Login using Magic Link
    await page.goto(`/admin?token=${token}`);
    await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();

    // 4. Navigate to API Tokens and Generate a new token
    await page.click('#nav-tokens');
    await page.click('button:has-text("Generate New Token")');
    await page.fill('#token-name', 'Analytics E2E Test CLI');
    await page.click('#token-modal button:has-text("Generate Token")');

    // 5. Scrape the generated raw token
    await expect(page.locator('#raw-token-display')).toBeVisible();
    const rawToken = await page.locator('#raw-token-display').innerText();
    expect(rawToken.length).toBeGreaterThan(10);
    await page.click('#token-modal button:has-text("Done")');

    // 6. Connect the lfr-tunnel client via Docker execution
    console.log("Connecting tunnel with token: " + rawToken);
    
    // We execute the CLI inside the running e2e-lfr-tunnel-1 container
    // LFT_TARGET_HOST is already set in the container environment
    const cmd = `docker exec e2e-lfr-tunnel-1 ./lfr-tunnel -token ${rawToken} -server http://tunnel.lfr-demo.local -ports 80 -background`;
    const { stdout, stderr } = await execAsync(cmd);
    console.log("Tunnel CLI stdout: ", stdout);
    console.log("Tunnel CLI stderr: ", stderr);

    // Wait for the tunnel to register and appear in the UI
    await expect(async () => {
        await page.reload();
        await page.click('#nav-tunnels');
        // The table shows 'up' for active tunnels
        await expect(page.locator('#tunnels-table-body td:has-text("up")').first()).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 15000 });

    // Scrape the generated subdomain public URL
    const publicUrlEl = await page.locator('#tunnels-table-body td a[target="_blank"]').first();
    const publicUrl = await publicUrlEl.getAttribute('href');
    expect(publicUrl).toBeTruthy();
    console.log("Public Tunnel URL: ", publicUrl);

    // 8. Generate Traffic through the public URL
    console.log("Sending traffic through the tunnel...");
    // We must route traffic through our local E2E Nginx (localhost:8000) but mock the Host header
    // so it doesn't escape to the real public internet VPS.
    const urlObj = new URL(publicUrl as string);
    const targetHost = urlObj.host;

    for (let i = 0; i < 5; i++) {
        const response = await page.request.get('http://localhost:8000', {
            headers: { 'Host': targetHost }
        });
        expect(response.status()).toBe(200);
    }

    // Wait a brief moment for bandwidth stats to flush and aggregate
    await page.waitForTimeout(2000);

    // 9. Navigate to Analytics Tab and verify data
    await page.click('#nav-analytics');
    
    // Check if the Chart.js canvas elements exist
    await expect(page.locator('#globalBandwidthChart')).toBeVisible();
    await expect(page.locator('#topUsersChart')).toBeVisible();

    // Check if the Client Distribution Table rendered our client
    await expect(page.locator('#client-stats-table-body td:has-text("Linux")')).toBeVisible();

    // Verify it doesn't say "No results found"
    await expect(page.locator('#client-stats-table-body')).not.toContainText('No results found.');

    // 10. Navigate back to Tunnels Tab and test the new Tunnel Details Modal
    await page.click('#nav-tunnels');
    
    // Find the action menu button
    const menuBtn = page.locator('#tunnels-table-body .action-menu-btn').first();
    await expect(menuBtn).toBeVisible();
    
    // Open the action menu
    await menuBtn.click();
    
    // Assert dropdown is visible and the trigger button has the 'active' class
    const dropdown = page.locator('.action-menu-dropdown.show').first();
    await expect(dropdown).toBeVisible();
    await expect(menuBtn).toHaveClass(/active/);
    
    // Press Escape to dismiss the dropdown
    await page.keyboard.press('Escape');
    
    // Assert dropdown is hidden and the trigger button no longer has the 'active' class
    await expect(dropdown).not.toBeVisible();
    await expect(menuBtn).not.toHaveClass(/active/);
    
    // Open the action menu again
    await menuBtn.click();
    await expect(dropdown).toBeVisible();
    await expect(menuBtn).toHaveClass(/active/);

    // Click the details item inside the open menu
    const detailsItem = page.locator('.action-menu-dropdown.show .action-menu-item:has-text("Details")').first();
    await expect(detailsItem).toBeVisible();
    await detailsItem.click();

    // Verify Tunnel Details modal is visible
    const detailsModal = page.locator('#tunnel-details-modal');
    await expect(detailsModal).toBeVisible();

    // Assert the content in the modal is populated correctly
    await expect(page.locator('#detail-tunnel-subdomain')).not.toBeEmpty();
    await expect(page.locator('#detail-tunnel-status')).toHaveText('up');
    await expect(page.locator('#detail-tunnel-owner')).toHaveText(adminEmail);
    await expect(page.locator('#detail-tunnel-limit')).toHaveText('100 RPS');
    await expect(page.locator('#detail-tunnel-client-ip')).not.toBeEmpty();

    // Verify Connected At timestamp tooltip structure
    const connectedEl = page.locator('#detail-tunnel-connected');
    await expect(connectedEl.locator('span.timestamp-tooltip')).toBeAttached();
    const connectedText = await connectedEl.innerText();
    expect(connectedText).not.toContain('<span');

    // Verify bandwidth statistics are not '0 Bytes' (traffic was generated)
    await expect(page.locator('#detail-tunnel-bytes-out')).not.toHaveText('0 Bytes');

    // Test Copy URL button and Toast
    await page.click('#tunnel-details-modal button:has-text("Copy URL")');
    await expect(page.locator('.toast:has-text("Copied tunnel URL to clipboard!")')).toBeVisible();

    // Test Refresh button and Toast
    await page.click('#tunnel-details-modal button:has-text("Refresh")');
    await expect(page.locator('.toast:has-text("Tunnel metrics refreshed!")')).toBeVisible();

    // Close the modal and assert it is hidden
    await page.click('#tunnel-details-modal button:has-text("Close")');
    await expect(detailsModal).not.toBeVisible();
  });
});
