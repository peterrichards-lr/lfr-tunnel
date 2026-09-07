import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * System Settings, reached the way a user reaches it (#1785).
 *
 * V1's sections all ship hidden and showTab() reveals one at a time, but several cards inside
 * a section additionally ship `display: none` and are revealed only by the loader that fills
 * them. So a loader wired to the wrong branch does not merely fetch late -- its markup is
 * unreachable on the route the user takes. Three loaders were wired that way:
 *
 *   loadServerConfig()      maintenance  -> #card-server-config      (in #tab-system)
 *   loadMaintenanceStatus() maintenance  -> #test-integration-target (in #tab-system)
 *   loadDomains()           reservations -> #acc-preferred-domain    (in #tab-account)
 *
 * Every existing assertion against #card-server-config had to click #nav-maintenance first,
 * which is what made the defect invisible: the workaround was in the test. So the load here
 * is deliberately a fresh `page.goto('/portal/system')` -- a new document, no prior click on
 * any other section -- and the guard test below proves that route is really unvisited.
 *
 * dashboard.html and dashboard.js are //go:embed-ed. Nothing in this file means anything
 * against a stale container: rebuild (`docker compose up -d --build`) or the run measures the
 * previous image and reports it as a pass.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

async function signIn(page: import('@playwright/test').Page) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('Portal V1 System Settings on a direct visit', () => {
  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // The premise the other tests rest on. If a fresh goto() to /portal/system quietly ran the
  // maintenance branch too, every assertion below would pass for the wrong reason and this
  // file would be measuring the old workaround again.
  test('the direct route really does not run the Maintenance branch', async ({
    page,
  }) => {
    await page.goto('/portal/system');

    // Polled, not read once: showTab() runs after the session check resolves, so a bare
    // read here races the app and the whole premise would be measured before it is true.
    await expect
      .poll(() =>
        page
          .locator('.main-content > div[id^="tab-"]:not(.hidden)')
          .evaluateAll((els) => els.map((e) => e.id.slice(4))),
      )
      .toEqual(['system']);
    await expect(page.locator('#nav-system')).toHaveAttribute(
      'aria-current',
      'page',
    );
    // Gateway Maintenance's own markup is untouched -- nothing has revealed its cards.
    await expect(page.locator('#tab-maintenance')).toHaveClass(/hidden/);
  });

  test('the Server Configuration card renders and is populated', async ({
    page,
  }) => {
    await page.goto('/portal/system');

    const card = page.locator('#card-server-config');
    await expect(card).toBeVisible();

    // Present is not the same as populated: the card is revealed in the same statement that
    // renders the tree, and an empty tree would still satisfy toBeVisible(). Anchor on real
    // rows from /api/admin/config-view.
    const keys = page.locator('#config-tree-container .config-tree-key');
    await expect(keys.first()).toBeVisible();
    expect(await keys.count()).toBeGreaterThan(5);
    // A real value out of tests/e2e/server-config.yaml, so this cannot pass on a tree of
    // empty keys. Note the JSON keys are Go field names -- ServerConfig.Domains carries no
    // json tag -- so the key is "Domains"; the value is the thing worth anchoring on.
    await expect(page.locator('#config-tree-container')).toContainText(
      'lfr-demo.local',
    );
    // The placeholder the card ships with must have been replaced.
    await expect(page.locator('#config-tree-container')).not.toContainText(
      'Loading server configuration',
    );
  });

  test('Save Settings is reachable and is not a child of the config card', async ({
    page,
  }) => {
    await page.goto('/portal/system');

    // Located by handler, not by label: the button carries data-i18n="save_settings" and the
    // text changes with the portal language.
    const save = page.locator(
      '#tab-system button[onclick="saveSystemSettings()"]',
    );
    await expect(save).toHaveCount(1);
    await expect(save).toBeVisible();

    // The structural half of #1785. #card-server-config's closing tag was lost in #606, so
    // the save row was reparented into the read-only config viewer and inherited both its
    // hidden default and its admin/403 gate -- the whole tab's only save control, gated on a
    // card that is not the form it saves.
    await expect(
      page.locator(
        '#card-server-config button[onclick="saveSystemSettings()"]',
      ),
    ).toHaveCount(0);
  });

  test('the integration test target renders', async ({ page }) => {
    await page.goto('/portal/system');

    // Second instance of the same defect, in the same branch: this line lives in the Test
    // Integrations card in #tab-system and used to be filled in by loadMaintenanceStatus()
    // because the two share an endpoint.
    const container = page.locator('#test-integration-target-container');
    await expect(container).toBeVisible();
    await expect(page.locator('#test-integration-target')).toContainText(
      adminEmail,
    );
  });

  test('Account offers its preferred-domain options without a Reservations visit', async ({
    page,
  }) => {
    await page.goto('/portal/account');

    // Third instance: #acc-preferred-domain ships with only "None (Auto)" and its real
    // options come from loadDomains(), whose only call site was loadReservations().
    // Anchored on the option that must be present, not on the absence of anything: the
    // control always carries its "None (Auto)" placeholder, so a count check alone would
    // pass on the broken build too.
    await expect(
      page.locator('#acc-preferred-domain option[value="lfr-demo.local"]'),
    ).toHaveCount(1);
    expect(
      await page.locator('#acc-preferred-domain option').count(),
    ).toBeGreaterThan(1);
  });
});
