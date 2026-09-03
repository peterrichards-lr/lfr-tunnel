import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Every sidebar destination has a shortcut, and the overlay lists only what the current user can
 * actually reach (#1640).
 *
 * Both portals bound 10 of 17 (V2) and 10 of 19 (V1). Worse, V2 decided availability from
 * hand-maintained `adminOnly` flags that had drifted from the sidebar: `g a` and `g t` pointed
 * into the Admin Zone without the flag, so a non-admin was shown them and navigated somewhere
 * they could not use -- while their own Analytics and Account had no shortcut at all.
 *
 * The non-admin case is the one that mattered and the one nobody had covered, so it is asserted
 * first here.
 */
const adminEmail = 'admin@lfr-demo.local';
// Kept short: a long local part widens the Users table and breaks a layout spec that runs after
// this one alphabetically, in CI only (see the e2e-testing skill).
const nonAdminEmail = `kbd-${Date.now().toString().slice(-5)}@lfr-demo.local`;

test.beforeAll(async () => {
  await createApprovedUser(nonAdminEmail);
});

test.afterAll(async () => {
  await deleteUser(nonAdminEmail);
});

async function loginV2(page: any, email: string) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

async function loginV1(page: any, email: string) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

/** Every destination the sidebar is currently offering, by visible link text. */
async function visibleSidebarLinks(page: any): Promise<string[]> {
  return page.$$eval('.sidebar a[href]', (els: Element[]) =>
    els
      .filter((e) => (e as HTMLElement).offsetParent !== null)
      .map((e) => (e.textContent || '').trim())
      .filter(Boolean),
  );
}

// The overlay mounts a moment after the dashboard route settles. Pressing '?' before then is
// silently dropped -- there is no listener yet -- which reads exactly like the shortcut being
// broken. Waiting on its trigger button is the cheapest proof the component is live.
async function openShortcuts(page: any) {
  await expect(page.locator('.shortcuts-trigger')).toBeVisible();
  await page.keyboard.press('?');
}

test.describe('Keyboard shortcut coverage', () => {
  test('V2 admin: the overlay covers every admin destination', async ({
    page,
  }) => {
    await loginV2(page, adminEmail);
    await openShortcuts(page);

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();

    const listed = await dialog.locator('.shortcuts-row').count();
    // 16 go-to destinations for an admin. Asserted as a floor rather than an exact count so
    // adding a screen does not fail this for the wrong reason -- the per-destination checks
    // below are what pin the actual coverage.
    expect(listed).toBeGreaterThanOrEqual(16);

    for (const label of [
      'Registered Subdomains',
      'Telemetry',
      'IP Blacklist',
      'Magic Links',
      'Extension Requests',
      'Account Settings',
    ]) {
      await expect(dialog.getByText(label, { exact: true })).toBeVisible();
    }
  });

  test('V2 non-admin: only reachable destinations are offered', async ({
    page,
  }) => {
    await loginV2(page, nonAdminEmail);
    await openShortcuts(page);

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();

    // Positive anchor FIRST: their own destinations must be there. Without this the absence
    // checks below would pass on an empty overlay, which is the failure mode being fixed.
    await expect(dialog.getByText('Analytics', { exact: true })).toBeVisible();
    await expect(
      dialog.getByText('Account Settings', { exact: true }),
    ).toBeVisible();

    // ...and nothing they cannot reach. These were offered before #1640.
    for (const label of [
      'Users',
      'System Settings',
      'Audit Logs',
      'API Tokens',
    ]) {
      await expect(dialog.getByText(label, { exact: true })).toHaveCount(0);
    }
  });

  test('V2 non-admin: g a goes to their analytics, not the admin one', async ({
    page,
  }) => {
    await loginV2(page, nonAdminEmail);
    await expect(page.locator('.shortcuts-trigger')).toBeVisible();
    await page.keyboard.press('g');
    await page.keyboard.press('a');
    // Previously this navigated to /admin/analytics, which they cannot use.
    await expect(page).toHaveURL(/\/portalv2\/analytics$/);
  });

  /**
   * The shortcuts overlay that has just been opened.
   *
   * Filtered on visibility rather than taking the first match in DOM order. The
   * `[role="dialog"]` arm is there for Portal V2, where the overlay is a dialog; in V1 it
   * matched nothing until #1707 added a `role="dialog"` policy modal ABOVE the overlay in
   * the document, at which point `.first()` started returning that modal -- permanently
   * hidden, so the assertion failed on a page where the overlay had opened correctly.
   *
   * Any dialog added to either portal would have done the same, so this is the selector's
   * bug rather than that modal's.
   */
  function shortcutsOverlay(page: any) {
    return page
      .locator('#shortcuts-overlay, [role="dialog"]')
      .filter({ visible: true })
      .first();
  }

  test('V1 admin: every visible sidebar link has a shortcut row', async ({
    page,
  }) => {
    await loginV1(page, adminEmail);

    const links = await visibleSidebarLinks(page);
    expect(links.length).toBeGreaterThan(10);

    await page.keyboard.press('?');
    const overlay = shortcutsOverlay(page);
    await expect(overlay).toBeVisible();

    const rows = await overlay.locator('.shortcuts-row').allTextContents();
    const rowText = rows.join(' | ');

    // Sampled across the ones that had no shortcut before #1640, rather than looping every
    // link: sidebar labels carry badges and counts that make exact matching brittle.
    for (const label of [
      'Reservations',
      'Active Tunnels',
      'Telemetry',
      'IP Blacklist',
      'Magic Links',
      'Account Settings',
    ]) {
      expect(rowText).toContain(label);
    }
  });

  test('V1: no duplicate shortcut keys are listed', async ({ page }) => {
    await loginV1(page, adminEmail);
    await page.keyboard.press('?');
    const overlay = shortcutsOverlay(page);
    await expect(overlay).toBeVisible();

    const keys = await overlay.locator('.shortcuts-row kbd').allTextContents();
    expect(keys.length).toBeGreaterThan(10);
    // 'a' is listed twice in the source, for the personal and admin Analytics nav items, so the
    // renderer dedupes. Two identical rows would be a visible defect.
    expect(new Set(keys).size).toBe(keys.length);
  });
});
