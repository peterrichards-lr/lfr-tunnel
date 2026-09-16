import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Anonymous geographic distribution, both portals (#1152).
 *
 * The E2E stack ships no geo-IP database, and never will -- every vendor forbids
 * redistribution (#1921). So what runs here is the `available: false` path, which is not a degraded
 * case to be tolerated but the state most deployments are in permanently, and the one an
 * admin is most likely to see.
 *
 * That makes the assertion worth having: an empty panel must SAY it is switched off
 * rather than looking like a working panel with no users in it. Those two states are
 * indistinguishable in the data and mean completely different things.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

test.describe('Geographic distribution — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
  });

  test('the panel is present and explains that it is switched off', async ({
    page,
  }) => {
    const panel = page
      .locator('.card')
      .filter({ hasText: 'Geographic Distribution' });
    await expect(panel).toHaveCount(1);
    // Names the setting, so an operator who wants it knows what to set. If the field is
    // ever renamed, this fails rather than leaving the UI pointing at a dead setting --
    // which it did: the first version of this panel said `geoip_database_path`, which
    // has never existed. `country_db_path` since #1921; `geolite2_db_path` still works as
    // an alias but is not what a new deployment should be told to write.
    await expect(panel).toContainText('country_db_path');
  });

  test('it does not claim there are no users when it simply is not running', async ({
    page,
  }) => {
    const panel = page
      .locator('.card')
      .filter({ hasText: 'Geographic Distribution' });
    // The below-threshold copy is the OTHER empty state and must not be shown here.
    await expect(panel).not.toContainText('enough distinct users');
  });
});

test.describe('Geographic distribution — Portal V1', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    await page.click('#nav-analytics');
  });

  test('the panel is present and explains that it is switched off', async ({
    page,
  }) => {
    const headline = page.locator('#geo-distribution-headline');
    await expect(headline).toBeVisible();
    await expect(headline).toContainText('country_db_path');

    // The raw key, which is what t() falls back to when a translation is missing. V1 calls
    // t() with no default here, so an unadded key renders as "geo_unavailable" to the user.
    await expect(headline).not.toContainText('geo_unavailable');
  });

  // #1920. V1 set the headline through three branches and then rendered the table
  // regardless, so the sentence above said the feature was off while the table below said
  // "No results found." -- we looked, and there was nobody. Nothing had been looked at.
  // V2 renders the message instead of the table and always has; under #1866 the two arms
  // of the A/B test differing is the defect.
  test('switched off means no table, no column headers and no search box', async ({
    page,
  }) => {
    // Positive anchor first. Every assertion below is an absence, and absences pass just
    // as well on a page that rendered nothing at all (e2e-testing skill §3).
    await expect(page.locator('#geo-distribution-headline')).toContainText(
      'country_db_path',
    );

    // The tbody is asserted rather than the wrapper: the wrapper did not exist before the
    // fix, and toBeHidden() is satisfied by an element that is simply not there -- so it
    // would have passed against the defect it is here to catch.
    await expect(page.locator('#geo-distribution-table-body')).toBeHidden();
    await expect(
      page.locator('#geo-distribution-table-body'),
    ).not.toContainText('No results found.');
    // Hidden, not absent: the header cell is in the markup either way, so toHaveCount(0)
    // would be testing whether the panel exists rather than whether it renders a table.
    // "Country" appears as a column header exactly once in dashboard.html, so this is
    // unambiguous without scoping.
    await expect(page.locator('th:has-text("Country")')).toBeHidden();

    // renderTable() creates this input on its first call and names it after the tbody, so
    // a search control existing at all means the table was rendered.
    await expect(
      page.locator('#geo-distribution-table-body-search'),
    ).toHaveCount(0);
  });
});

/**
 * Per-provider attribution (#1921).
 *
 * The E2E stack ships no geo-IP database, so what is exercised here is the switched-off
 * state -- and the assertion that matters in that state is an ABSENCE: no vendor's credit
 * line may appear under a panel that is not using anyone's data. Getting this wrong would
 * publish, say, DB-IP's CC BY 4.0 acknowledgment on every deployment that never deployed a
 * DB-IP file.
 *
 * Each absence is anchored on a positive assertion first, because an absence passes just as
 * well on a page that rendered nothing at all (e2e-testing skill §3).
 *
 * "No credit" is asserted against the credit ELEMENT and the vendor links, never against the
 * vendor names as free text -- the off-state copy lists the databases that would work, which
 * is guidance rather than attribution.
 *
 * The populated side -- which vendor gets which credit -- is covered by Go tests against a
 * real database (pkg/geo/provider_compat_test.go) rather than here, because proving it in a
 * browser would mean shipping a licensed artefact into the E2E image, which no vendor allows.
 */
test.describe('Geographic distribution attribution — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
  });

  test('no vendor is credited while the feature is switched off', async ({
    page,
  }) => {
    const panel = page
      .locator('.card')
      .filter({ hasText: 'Geographic Distribution' });
    await expect(panel).toContainText('country_db_path');

    // The credit line itself, not the vendor's NAME. The switched-off panel names all three
    // supported databases on purpose -- an operator reading "set country_db_path" needs to
    // know which files satisfy it -- so banning the strings "DB-IP"/"MaxMind"/"IP2Location"
    // outright would forbid that guidance and did: this assertion failed the first time the
    // #1938 wording landed, against a panel that was crediting nobody.
    //
    // Naming a database as a suggestion is not using its data; the obligation DB-IP's CC BY
    // 4.0 and MaxMind's licence create is a credit shown where their results are displayed.
    // So the property is that no CREDIT and no vendor LINK is rendered while the feature is
    // off, which is what the two assertions below check -- V1 proves the same thing through
    // #geo-distribution-attribution.
    await expect(panel.locator('[data-testid="geo-attribution"]')).toHaveCount(
      0,
    );
    await expect(
      panel.locator(
        'a[href*="db-ip.com"], a[href*="maxmind.com"], a[href*="ip2location.com"]',
      ),
    ).toHaveCount(0);
  });
});

test.describe('Geographic distribution attribution — Portal V1', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
    await page.click('#nav-analytics');
  });

  test('the credit line exists in the markup and is empty when off', async ({
    page,
  }) => {
    // Positive anchor: the panel loaded and reported itself off.
    await expect(page.locator('#geo-distribution-headline')).toContainText(
      'country_db_path',
    );

    // Present in the DOM rather than absent -- dashboard.js fills this element, so a
    // missing element would mean the credit can never render in ANY state, which the
    // absence assertions below would happily pass over.
    const attribution = page.locator('#geo-distribution-attribution');
    await expect(attribution).toHaveCount(1);
    await expect(attribution).toHaveText('');
  });
});

test.describe('Geographic distribution — access', () => {
  // Short on purpose, and cleaned up afterwards: the e2e database is shared and specs run in
  // file order, so a long-lived row here widens the Admin Users table for every later spec
  // (the collision documented in portal_v2_nonadmin_analytics).
  const email = `ng-${Date.now().toString(36).slice(-5)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await clearMailpit();
    await createApprovedUser(email);
  });

  test.afterAll(async () => {
    await deleteUser(email);
  });

  test('the endpoint is not open to an unauthenticated caller', async ({
    request,
  }) => {
    const res = await request.get('/api/admin/analytics/locations');
    expect(res.status()).not.toBe(200);
  });

  test('a non-admin sees no geographic panel', async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', email);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(email);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    await page.goto('/portalv2/analytics');
    // Not merely hidden: #1512 was a non-admin reaching admin analytics at all, and this
    // panel reports where a deployment's users are.
    await expect(
      page.locator('.card').filter({ hasText: 'Geographic Distribution' }),
    ).toHaveCount(0);
  });
});
