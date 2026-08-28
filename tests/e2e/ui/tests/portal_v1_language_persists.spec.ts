import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * A V1 language choice has to survive a reload (#1541).
 *
 * The issue was filed as "a signed-in user cannot change language". They can: the account tab
 * has had a working control all along, and choosing Spanish switches the interface. What did not
 * work was remembering it -- V1 never touched localStorage for language, so the choice lasted
 * exactly as long as the page.
 *
 * That also made it a parity gap. Portal V2 writes `lfr_lang`, so a language chosen there was
 * ignored by V1 entirely.
 *
 * Driven through the real control rather than by calling changePortalLanguage(), which is what
 * the issue asked for and what would have caught this: the function always worked, and a test
 * calling it directly would have passed throughout.
 */
test.describe('Portal V1 language preference', () => {
  const adminEmail = 'admin@lfr-demo.local';

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
    await page.click('#nav-account');
  });

  test.afterEach(async ({ page }) => {
    // Leave the browser profile as we found it -- specs share a context and a stray language
    // would change what the next one sees.
    await page.evaluate(() => localStorage.removeItem('lfr_lang'));
  });

  test('a choice made in the account tab survives a reload', async ({
    page,
  }) => {
    // Through the dropdown, as a person would.
    await page.click('#acc-custom-dropdown-trigger');
    await page
      .locator('#acc-custom-menu')
      .getByText(/Espa|Spanish/i)
      .first()
      .click();

    await expect(page.locator('#nav-account')).toHaveText(
      /Configuraci|cuenta/i,
    );

    await page.reload();

    // The whole defect: this used to come back in English.
    await expect(page.locator('#nav-account')).toHaveText(
      /Configuraci|cuenta/i,
    );
  });

  test('the account control shows the language actually in use after a reload', async ({
    page,
  }) => {
    await page.click('#acc-custom-dropdown-trigger');
    await page
      .locator('#acc-custom-menu')
      .getByText(/Espa|Spanish/i)
      .first()
      .click();
    await page.reload();
    await page.click('#nav-account');

    // A control reading "English" on a Spanish page is its own bug -- the user would have no
    // way to tell what is set.
    await expect(page.locator('#acc-custom-label')).toHaveText(/Espa/i);
  });

  test('a language chosen in Portal V2 is honoured by V1', async ({ page }) => {
    // V2 writes this key (I18nContext.tsx). V1 read nothing, so the portals disagreed.
    await page.evaluate(() => localStorage.setItem('lfr_lang', 'fr'));
    await page.reload();

    await expect(page.locator('#nav-account')).toHaveText(/compte|Param/i);
  });
});
