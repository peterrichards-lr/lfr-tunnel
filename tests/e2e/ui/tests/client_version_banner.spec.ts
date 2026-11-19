import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V2 } from './utils/portal';

/**
 * V2 tells a user whose client is below min_client_version (#2522), from `min_version` on
 * /api/me. The server's state for each phase is Go-tested (min_version_notice_test.go); this is
 * the banner rendering each one, and rendering nothing when the field is absent -- which is what
 * the stack's own answer is, since its users run current or `dev` clients.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function signIn(page: any) {
  await clearMailpit();
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard**`);
}

// /api/me as the gateway answers it, with min_version set (or left alone when undefined).
async function stubMinVersion(page: any, minVersion?: object) {
  await page.route('**/api/me', async (route: any) => {
    if (route.request().method() !== 'GET') return route.continue();
    const res = await route.fetch();
    const body = await res.json();
    if (minVersion) body.min_version = minVersion;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
}

const base = {
  required: true,
  client_version: 'v1.40.7',
  min_version: 'v1.45.3',
  upgrade_command: 'lfr-tunnel -upgrade',
};

// One test with a step per state, signing in once: the gateway limits magic-link requests per
// address, so a sign-in per test hands the later ones a token already spent.
test('V2 says when the client is below min_client_version', async ({
  page,
}) => {
  await signIn(page);
  const banner = page.getByTestId('client-version-banner');

  await test.step('a current client gets no banner', async () => {
    await stubMinVersion(page);
    await page.reload();
    // The page rendered, so the absence below means something.
    await expect(page.locator('#access-tokens')).toBeVisible();
    await expect(banner).toHaveCount(0);
  });

  await test.step('before the window starts: upgrade required, no date', async () => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    await stubMinVersion(page, { ...base, phase: 'grace' });
    await page.reload();
    await expect(banner).toContainText('v1.40.7');
    await expect(banner).toContainText('v1.45.3');
    await expect(banner).toContainText('lfr-tunnel -upgrade');
    await expect(banner).toContainText(
      'Upgrade it to keep starting new tunnels',
    );
    await expect(banner).not.toContainText('Upgrade by');
  });

  await test.step('in the warning window: the deadline and the time left', async () => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    await stubMinVersion(page, {
      ...base,
      phase: 'warning',
      deadline: '2031-03-04T05:06:07Z',
      seconds_remaining: 3 * 86400 + 4 * 3600,
    });
    await page.reload();
    await expect(banner).toContainText('Upgrade by');
    await expect(banner).toContainText('2031');
    await expect(banner).toContainText('(3d 4h)');
  });

  await test.step('expired: new tunnels are refused', async () => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    await stubMinVersion(page, {
      ...base,
      phase: 'expired',
      deadline: '2020-01-01T00:00:00Z',
    });
    await page.reload();
    await expect(banner).toContainText('new tunnels are refused');
    await expect(banner).toContainText('lfr-tunnel -upgrade');
  });

  await page.unrouteAll({ behavior: 'ignoreErrors' });
});
