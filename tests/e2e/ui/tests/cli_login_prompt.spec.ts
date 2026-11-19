import { test, expect } from './utils/fixtures';
import {
  getMagicLinkPath,
  getMagicLinkToken,
  clearMailpit,
} from './utils/mailpit';
import { V2 } from './utils/portal';

/**
 * `lfr-tunnel login` opens the portal with ?cli_login=1, and the portal must ask for a token (#2459).
 *
 * The CLI only receives a token when the user creates one; before this, a signed-in user landed on
 * the Overview, a signed-out one on the dashboard after signing in, and the terminal waited forever
 * either way. Both cases, in V2 -- the only portal a CLI sign-in reaches since #2528:
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

  async function signInV2(page: import('@playwright/test').Page) {
    await page.goto(`${V2}/`);
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`${V2}/login?token=${token}`);
    await page.waitForURL(`**${V2}/dashboard`);
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

  // Older clients open the gateway's root with the marker; it now redirects to V2 (#2528), and
  // the marker has to survive the redirect or their terminal waits forever.
  test('the old entry point keeps the marker through the redirect to V2', async ({
    page,
  }) => {
    await signInV2(page);
    await page.goto('/?cli_login=1');
    await page.waitForURL(`**${V2}/dashboard**`);
    await expectV2Prompt(page);
  });

  test('V2, signed in: the dialog opens straight away', async ({ page }) => {
    await signInV2(page);
    await page.goto(`${V2}/dashboard?cli_login=1`);
    await expectV2Prompt(page);
  });

  test('V2, signed out: the emailed link carries the marker and the dialog opens', async ({
    page,
  }) => {
    await page.goto(`${V2}/?cli_login=1`);
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');

    const link = await getMagicLinkPath(adminEmail);
    expect(link, 'the emailed link lost the CLI marker').toContain(
      'cli_login=1',
    );
    await page.goto(link);
    await page.waitForURL(`**${V2}/dashboard**`);
    await expectV2Prompt(page);
  });

  test('V2 control: an ordinary sign-in shows the dashboard without the dialog', async ({
    page,
  }) => {
    await signInV2(page);
    await expect(page.locator('h1, h2').first()).toBeVisible(); // anchor
    await expect(page.locator('#generate-token-title')).toBeHidden();
  });

  // The SSO buttons carry the marker too (#2459 review). The Go tests start at /sso/login with
  // the query already set, so only this proves the button puts it there. Each control anchors on
  // the button being visible, so it cannot pass on a page that rendered no button at all.
  test('V2: the SSO button carries the marker only for a CLI visit', async ({
    page,
  }) => {
    await page.goto(`${V2}/?cli_login=1`);
    const sso = page.locator('[data-testid="sso-providers"] a').first();
    await expect(sso).toBeVisible();
    expect(await sso.getAttribute('href')).toContain('cli_login=1');

    await page.goto(`${V2}/`);
    await expect(sso).toBeVisible();
    expect(await sso.getAttribute('href')).not.toContain('cli_login');
  });
});
