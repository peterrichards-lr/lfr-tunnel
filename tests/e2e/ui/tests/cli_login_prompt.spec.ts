import { test, expect } from './utils/fixtures';
import {
  getMagicLinkPath,
  getMagicLinkToken,
  clearMailpit,
} from './utils/mailpit';

/**
 * `lfr-tunnel login` opens the portal with ?cli_login=1, and the portal must ask for a token (#2459).
 *
 * The CLI only receives a token when the user creates one; before this, a signed-in user landed on
 * the Overview, a signed-out one on the dashboard after signing in, and the terminal waited forever
 * either way. Both arms, both cases:
 *   - signed in: the create-token dialog opens straight away;
 *   - signed out: the marker survives the magic-link sign-in (the emailed link carries it) and the
 *     dialog opens once the link signs the user in.
 * The control -- an ordinary visit shows the dashboard WITHOUT the dialog -- is anchored on the
 * dashboard being visible, so it cannot pass on a blank page (e2e-testing SKILL section 3).
 */
test.describe('arriving from lfr-tunnel login opens the create-token dialog', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml
  const namePrefix = 'lfr-tunnel login ';
  const note = 'Your terminal is waiting for a token';

  test.beforeEach(async () => {
    await clearMailpit();
  });

  async function signInV1(page: import('@playwright/test').Page) {
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    // 15s, not the 5s default: on a cold stack the request has been seen still "Sending..." at 5s
    // (it hit whichever V1 test ran first; CI's two retries hide it in the other specs).
    await expect(page.locator('text=Magic Link Sent')).toBeVisible({
      timeout: 15000,
    });
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  }

  async function signInV2(page: import('@playwright/test').Page) {
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  }

  async function expectV1Prompt(page: import('@playwright/test').Page) {
    await expect(page.locator('#token-modal')).toBeVisible();
    await expect(page.locator('#token-cli-note')).toBeVisible();
    await expect(page.locator('#token-cli-note')).toContainText(note);
    expect(await page.locator('#token-name').inputValue()).toMatch(
      new RegExp('^' + namePrefix),
    );
    expect(page.url()).not.toContain('cli_login');
  }

  async function expectV2Prompt(page: import('@playwright/test').Page) {
    await expect(page.locator('#generate-token-title')).toBeVisible();
    await expect(
      page.getByRole('status').filter({ hasText: note }),
    ).toBeVisible();
    expect(await page.locator('#token-name-label').inputValue()).toMatch(
      new RegExp('^' + namePrefix),
    );
    expect(page.url()).not.toContain('cli_login');
  }

  test('V1, signed in: the dialog opens straight away', async ({ page }) => {
    await signInV1(page);
    await page.goto('/?cli_login=1');
    await expectV1Prompt(page);
  });

  test('V1, signed out: the emailed link carries the marker and the dialog opens', async ({
    page,
  }) => {
    await page.goto('/?cli_login=1');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible({
      timeout: 15000,
    });

    const link = await getMagicLinkPath(adminEmail);
    expect(link, 'the emailed link lost the CLI marker').toContain(
      'cli_login=1',
    );
    await page.goto(link);
    await expectV1Prompt(page);
  });

  test('V1 control: an ordinary sign-in shows the dashboard without the dialog', async ({
    page,
  }) => {
    await signInV1(page); // anchors: the dashboard heading is visible
    await expect(page.locator('#token-modal')).toBeHidden();
  });

  test('V2, signed in: the dialog opens straight away', async ({ page }) => {
    await signInV2(page);
    await page.goto('/portalv2/dashboard?cli_login=1');
    await expectV2Prompt(page);
  });

  test('V2, signed out: the emailed link carries the marker and the dialog opens', async ({
    page,
  }) => {
    await page.goto('/portalv2/?cli_login=1');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const link = await getMagicLinkPath(adminEmail);
    expect(link, 'the emailed link lost the CLI marker').toContain(
      'cli_login=1',
    );
    await page.goto(link);
    await page.waitForURL('**/portalv2/dashboard**');
    await expectV2Prompt(page);
  });

  test('V2 control: an ordinary sign-in shows the dashboard without the dialog', async ({
    page,
  }) => {
    await signInV2(page);
    await expect(page.locator('h1, h2').first()).toBeVisible(); // anchor
    await expect(page.locator('#generate-token-title')).toBeHidden();
  });
});
