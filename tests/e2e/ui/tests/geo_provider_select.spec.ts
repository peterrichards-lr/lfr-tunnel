import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Choosing the geo-IP vendor from System Settings, both portals (#1995).
 *
 * The vendor decides whose licence-required credit this deployment publishes, and every
 * supported vendor requires a DIFFERENT one -- so the screen shows the exact credit a chosen
 * vendor will publish before the admin publishes it on that vendor's behalf.
 *
 * THE CONTROL, and the reason this spec exists rather than a Go test alone: the credit must
 * actually DIFFER between two vendors. A <select> wired to nothing renders the same text for
 * both, which looks identical to a working one in any single-vendor assertion. The Go suite
 * proves the gateway resolves and serves the right vendor (pkg/server/geo_provider_settings_test.go);
 * only a browser can prove the control changes what is on screen.
 *
 * And with NO vendor chosen, no credit is rendered at all. That is the licence protection:
 * with nobody named there is nobody to credit, and printing any vendor's line would be a false
 * statement about provenance with the real supplier's licence still unmet. Anchored on a
 * positive assertion first, because an absence passes just as well on a page that rendered
 * nothing (e2e-testing skill §3).
 *
 * Nothing here needs a geo-IP database, which is why it can run at all: the E2E stack ships
 * none and never will, since no vendor permits redistribution. The vendor is a DISPLAY value
 * that never touches decoding, so the preview is exactly as real without one.
 *
 * Nothing here SAVES: the select is changed and the credit is read, and only the Save button
 * posts. So this spec writes no admin_settings row and leaves no fixture behind for the specs
 * that run after it (e2e-testing skill §4). The save round-trip and the precedence rule are
 * covered in Go, where they can be asserted without a shared database.
 *
 * dashboard.html/dashboard.js and the V2 bundle are baked into the image. A run against a
 * stale container measures the previous build (e2e-testing skill §2).
 */

const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

// The vendors the gateway declares, with something unique to each one's credit. Not a copy of
// the gateway's vocabulary -- the dropdown is asserted against what it actually offers below;
// these are the licence texts, which are prescribed by the vendors and are the thing that must
// not be interchangeable.
const MAXMIND = { value: 'maxmind', host: 'maxmind.com' };
const DBIP = { value: 'dbip', host: 'db-ip.com' };

test.describe('Geo-IP vendor selection — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/settings');
  });

  test('the vendors are a dropdown, offered by the gateway, with a not-set option', async ({
    page,
  }) => {
    const select = page.getByTestId('geo-provider-select');
    await expect(select).toBeVisible();

    // A <select>, not a text input: a typo has to be impossible rather than merely reported.
    expect(await select.evaluate((el) => el.tagName)).toBe('SELECT');

    // Exactly the gateway's vocabulary plus the honest default. Asserted as the whole list
    // rather than "contains dbip", so an option this build does not support cannot appear
    // and be refused on save.
    await expect
      .poll(() =>
        select.evaluate((el) =>
          Array.from((el as HTMLSelectElement).options).map((o) => o.value),
        ),
      )
      .toEqual(['', MAXMIND.value, DBIP.value, 'ip2location']);
  });

  test('changing the vendor changes the credit that will be published', async ({
    page,
  }) => {
    const select = page.getByTestId('geo-provider-select');
    await expect(select).toBeVisible();

    await select.selectOption(MAXMIND.value);
    const credit = page.getByTestId('geo-provider-credit');
    await expect(credit).toBeVisible();
    const maxmindText = (await credit.textContent()) || '';
    // The link is half the obligation -- DB-IP's licence asks specifically for one -- so the
    // anchor is asserted, not just the sentence.
    await expect(credit.locator(`a[href*="${MAXMIND.host}"]`)).toHaveCount(1);

    await select.selectOption(DBIP.value);
    await expect(credit.locator(`a[href*="${DBIP.host}"]`)).toHaveCount(1);
    const dbipText = (await credit.textContent()) || '';

    // THE CONTROL. Two vendors, two different prescribed acknowledgments: a select wired to
    // nothing renders the same text twice and fails here.
    expect(maxmindText.trim()).not.toBe('');
    expect(dbipText.trim()).not.toBe('');
    expect(dbipText).not.toBe(maxmindText);

    // And no restart was involved: the credit changed in a live page.
    await expect(credit.locator(`a[href*="${MAXMIND.host}"]`)).toHaveCount(0);
  });

  test('no vendor chosen means no credit is rendered', async ({ page }) => {
    const select = page.getByTestId('geo-provider-select');

    // Positive anchor first: prove the preview CAN render before asserting it does not.
    await select.selectOption(DBIP.value);
    await expect(page.getByTestId('geo-provider-credit')).toBeVisible();

    await select.selectOption('');
    await expect(page.getByTestId('geo-provider-credit')).toHaveCount(0);
    // No vendor link anywhere in the card either -- the credit element vanishing is not the
    // same as no vendor being credited.
    await expect(
      page.locator(
        'a[href*="db-ip.com"], a[href*="maxmind.com"], a[href*="ip2location.com"]',
      ),
    ).toHaveCount(0);
  });

  test('the screen says which source the vendor in force came from', async ({
    page,
  }) => {
    // The E2E gateway sets neither country_db_provider nor a portal row, which is the third
    // of the three precedence states and the one that must not silently look like the others.
    // The exact wording is asserted, not merely "server-config.yaml is mentioned": all three
    // states mention it, so a substring that loose would be satisfied by any of them.
    const source = page.getByTestId('geo-provider-source');
    await expect(source).toBeVisible();
    await expect(source).toContainText(
      'No vendor is set here or in server-config.yaml',
    );
  });

  test('the database path is shown and is not editable here', async ({
    page,
  }) => {
    // country_db_path stays YAML-only: it is a filesystem path on the gateway host, and an
    // admin session that could point the gateway at any readable path is a disclosure vector.
    await expect(page.getByTestId('geo-provider-path')).toBeVisible();
    await expect(
      page.locator('input[name="country_db_path"], #country-db-path'),
    ).toHaveCount(0);
  });
});

test.describe('Geo-IP vendor selection — Portal V1', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
    await page.goto('/portal/system');
  });

  test('the vendors are a dropdown, offered by the gateway, with a not-set option', async ({
    page,
  }) => {
    const select = page.locator('#geo-provider-select');
    await expect(select).toBeVisible();
    expect(await select.evaluate((el) => el.tagName)).toBe('SELECT');
    await expect
      .poll(() =>
        select.evaluate((el) =>
          Array.from((el as HTMLSelectElement).options).map((o) => o.value),
        ),
      )
      .toEqual(['', MAXMIND.value, DBIP.value, 'ip2location']);
  });

  // The same control as V2's. The two portals are a live A/B test (#1866), so a control that
  // works in one arm and not the other is the defect, not a presentation difference.
  test('changing the vendor changes the credit that will be published', async ({
    page,
  }) => {
    const select = page.locator('#geo-provider-select');
    await expect(select).toBeVisible();

    await select.selectOption(MAXMIND.value);
    const credit = page.locator('#geo-provider-credit');
    await expect(credit).toBeVisible();
    const maxmindText = (await credit.textContent()) || '';
    await expect(credit.locator(`a[href*="${MAXMIND.host}"]`)).toHaveCount(1);

    await select.selectOption(DBIP.value);
    await expect(credit.locator(`a[href*="${DBIP.host}"]`)).toHaveCount(1);
    const dbipText = (await credit.textContent()) || '';

    expect(maxmindText.trim()).not.toBe('');
    expect(dbipText.trim()).not.toBe('');
    expect(dbipText).not.toBe(maxmindText);
    await expect(credit.locator(`a[href*="${MAXMIND.host}"]`)).toHaveCount(0);
  });

  test('no vendor chosen means no credit is rendered', async ({ page }) => {
    const select = page.locator('#geo-provider-select');
    const container = page.locator('#geo-provider-credit-container');

    // Positive anchor first.
    await select.selectOption(DBIP.value);
    await expect(container).toBeVisible();

    await select.selectOption('');
    // Hidden, not absent: dashboard.js fills this element, so asserting absence would pass
    // on a build where the preview can never render in ANY state.
    await expect(container).toBeHidden();
    await expect(page.locator('#geo-provider-credit')).toHaveText('');
  });

  test('the screen says which source the vendor in force came from', async ({
    page,
  }) => {
    const source = page.locator('#geo-provider-source');
    await expect(source).toBeVisible();
    await expect(source).toContainText(
      'No vendor is set here or in server-config.yaml',
    );
  });
});
