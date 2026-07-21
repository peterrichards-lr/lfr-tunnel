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
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

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
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

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

  test('Verify Docker panel and user details modal HTML escaping', async ({ page }) => {
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
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

    // 4. Verify Docker Panel is visible and displays the configured image
    await expect(page.locator('#docker-container-box')).toBeVisible();
    await expect(page.locator('#docker-pull-text')).toContainText('docker pull peterjrichards/lfr-tunnel:latest');

    // 5. Navigate to Users tab and open User Details modal
    await page.click('#nav-users');
    await expect(page.locator('h2:has-text("Users")')).toBeVisible();

    // Find and click the administrator's email link to open the details modal
    await page.click(`a:has-text("${adminEmail}")`);
    
    // Assert modal is open and has user details
    await expect(page.locator('#user-details-modal')).toBeVisible();
    await expect(page.locator('#detail-user-email')).toHaveText(adminEmail);

    // Verify Joined Date tooltip rendered as HTML (not raw text)
    const joinedEl = page.locator('#detail-user-joined');
    await expect(joinedEl.locator('span.timestamp-tooltip')).toBeAttached();
    
    const joinedText = await joinedEl.innerText();
    expect(joinedText).not.toContain('<span');

    // Close user details modal
    await page.click('#user-details-modal button:has-text("Close")');

    // 7. Verify dynamic platform configuration overrides render correctly on the Overview page
    await page.click('#nav-overview');
    const bannerContainer = page.locator('#cli-client-banner-container');
    await expect(bannerContainer).toBeVisible();
    const textContent = await bannerContainer.innerText();
    expect(textContent).toMatch(/(Recommended Installation|Upgrade Command|Client Up to Date|Client Info)/);
    await expect(page.locator('text=6d4aef719dee798e611139e422d9231b226ec3538617d025fad08accf8fc63d6')).toBeVisible();
    
    // Direct binary download is disabled in server-config.yaml, so the download button should NOT be visible
    await expect(page.locator('text=⬇️ Download Signed Binary')).not.toBeVisible();

    // Verify package manager sections (Brew, Scoop) are hidden in the guide modal
    await page.click('button:has-text("Other Operating Systems")');
    await expect(page.locator('#installer-guide-modal')).toBeVisible();
    await expect(page.locator('#guide-macos-brew-section')).not.toBeVisible();
    
    await page.click('#tab-btn-windows');
    await expect(page.locator('#guide-windows-scoop-section')).not.toBeVisible();
    
    await page.click('#installer-guide-modal button:has-text("Close")');
  });

  test('Subdomain Reservations flow', async ({ page }) => {
    // 1. Login
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);

    // 2. Navigate to Reservations
    await page.click('#nav-reservations');
    await expect(page.locator('h2:has-text("Subdomain Reservations")')).toBeVisible();

    // 3. Test Generator and reservation fields
    await expect(page.locator('#res-subdomain')).toBeVisible();
    await expect(page.locator('#res-domain')).toBeVisible();
    
    // Select style and generate
    await page.selectOption('#res-style-select', 'words');
    await page.click('#btn-generate-subdomain');
    
    // Check that subdomain input is now populated (not empty)
    await expect(page.locator('#res-subdomain')).toHaveValue(/^[a-z]+-[a-z]+-[a-z]+$/); // word-word-word
    const val = await page.inputValue('#res-subdomain');

    // Click Reserve Subdomain
    await page.click('#btn-reserve-subdomain');

    // Wait for the subdomain to show up in the reservations table
    await expect(page.locator('#reservations-table-body')).toContainText(val);

    // Verify visual quota limit progress bar updates
    await expect(page.locator('#reservation-quota-text')).toContainText('subdomains reserved');
  });

  test('Verify Telemetry Maintenance Countdown Widget', async ({ page }) => {
    // 1. Login
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

    // The maintenance countdown box should not be visible initially
    await expect(page.locator('#overview-maintenance-box')).not.toBeVisible();
    await expect(page.locator('#global-maintenance-banner')).not.toBeVisible();

    // Register dialog handler to automatically accept confirmation prompts
    page.on('dialog', dialog => dialog.accept());

    // 2. Navigate to Gateway Maintenance and schedule a mock soft maintenance
    await page.click('#nav-maintenance');
    await expect(page.locator('h2:has-text("Gateway Maintenance")')).toBeVisible();

    // Select countdown (e.g. 5 minutes)
    await page.selectOption('#maint-countdown-select', '5');
    await page.fill('#maint-soft-reason', 'E2E Testing Maintenance');
    await page.click('#btn-toggle-maint');

    // Verify soft maintenance is now pending in control panel
    await expect(page.locator('#maint-status-text')).toContainText('SCHEDULED');

    // 3. Navigate back to Overview and verify countdown banner is visible
    await page.click('#nav-overview');
    await expect(page.locator('#global-maintenance-banner')).toBeVisible();
    await expect(page.locator('#global-maintenance-banner')).toContainText('Scheduled Maintenance starting in');

    // 4. Cancel maintenance from Gateway Maintenance tab
    await page.click('#nav-maintenance');
    await page.click('#btn-toggle-maint');
    await expect(page.locator('#maint-status-text')).toContainText('INACTIVE');

    // 5. Verify global banner disappears
    await page.click('#nav-overview');
    await expect(page.locator('#global-maintenance-banner')).not.toBeVisible();
  });

  test('Verify Collapsible Sidebar Sections and Persistence', async ({ page }) => {
    // 1. Login
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

    // 2. Verify all sections are expanded initially
    const personalSection = page.locator('#section-personal');
    const adminSection = page.locator('#section-administration');
    await expect(personalSection).toBeVisible();
    await expect(adminSection).toBeVisible();
    await expect(personalSection).not.toHaveClass(/collapsed/);
    await expect(adminSection).not.toHaveClass(/collapsed/);

    // 3. Click personal section header to collapse it
    await page.click('.sidebar-section-header:has-text("Personal")');
    await expect(personalSection).toHaveClass(/collapsed/);

    // 4. Reload page and check that personal section remains collapsed
    await page.reload();
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();
    await expect(page.locator('#section-personal')).toHaveClass(/collapsed/);

    // 5. Expand personal section again
    await page.click('.sidebar-section-header:has-text("Personal")');
    await expect(page.locator('#section-personal')).not.toHaveClass(/collapsed/);
  });

  test('Verify Collapsible Sidebar Toggle and Persistence', async ({ page }) => {
    // 1. Login
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

    // 2. Verify sidebar is expanded initially
    const sidebar = page.locator('.sidebar');
    await expect(sidebar).not.toHaveClass(/collapsed/);

    // 3. Click sidebar toggle button to collapse it
    await page.click('#sidebar-toggle-btn');
    await expect(sidebar).toHaveClass(/collapsed/);

    // 4. Reload page and check that sidebar remains collapsed
    await page.reload();
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();
    await expect(page.locator('.sidebar')).toHaveClass(/collapsed/);

    // 5. Expand sidebar again
    await page.click('#sidebar-toggle-btn');
    await expect(page.locator('.sidebar')).not.toHaveClass(/collapsed/);
  });

  test('Verify Mobile Responsive Layout and Sidebar Slide-Over Drawer', async ({ page }) => {
    // 1. Set viewport size to a mobile dimension
    await page.setViewportSize({ width: 375, height: 812 });

    // 2. Login
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);

    // Wait for Dashboard to load
    await expect(page.locator('h2:has-text("Welcome back")')).toBeVisible();

    // 3. Verify that the sidebar is collapsed and backdrop not visible initially
    const sidebar = page.locator('.sidebar');
    const backdrop = page.locator('#sidebar-backdrop');
    await expect(sidebar).not.toHaveClass(/active/);
    await expect(backdrop).not.toHaveClass(/visible/);

    // 4. Click sidebar toggle button to open it
    await page.click('#sidebar-toggle-btn');
    await expect(sidebar).toHaveClass(/active/);
    await expect(backdrop).toHaveClass(/visible/);

    // 5. Click the backdrop to close the sidebar
    await backdrop.click();
    await expect(sidebar).not.toHaveClass(/active/);
    await expect(backdrop).not.toHaveClass(/visible/);
  });
});

