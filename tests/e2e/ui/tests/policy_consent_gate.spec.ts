import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The policy re-consent gate and banner, in both arms (#1707).
 *
 * `policy_consent` is stubbed onto /api/me rather than driven by configuring a real
 * policy_version on the gateway. Two reasons, and the first is the important one:
 * turning re-consent on for the shared stack would put a modal over every other spec's
 * page, so this spec would break the suite it runs in. The second is that the behaviour
 * under test is what each portal does with the value -- the server's phase arithmetic,
 * the refusal of new tunnels and the append-only history are covered by Go tests and
 * mutation-tested there.
 *
 * Same technique, and the same reasoning, as session_expiry_warning.spec.ts.
 *
 * No fixture data is created: with no policy_version configured the accept endpoint is a
 * no-op, so nothing is written to the shared database and there is nothing to clean up.
 */
const adminEmail = 'admin@lfr-demo.local';

type Phase = 'grace' | 'warning' | 'expired';

/** Rewrites /api/me so the account appears to owe acceptance of a new policy version. */
async function stubConsent(
  page: any,
  phase: Phase | null,
  opts: { suppressed?: boolean; secondsRemaining?: number } = {},
) {
  await page.route('**/api/me', async (route: any) => {
    const res = await route.fetch();
    const body = await res.json().catch(() => ({}));
    if (phase === null) {
      body.policy_consent = { required: false };
      body.policy_gate_suppressed = false;
    } else {
      body.policy_consent = {
        required: true,
        document_id: 'privacy_policy',
        version: '2',
        phase,
        deadline: new Date(
          Date.now() + (opts.secondsRemaining ?? 4 * 86400) * 1000,
        ).toISOString(),
        seconds_remaining: opts.secondsRemaining ?? 4 * 86400,
        policy_url: '/privacy',
        cookie_url: '/cookies',
      };
      body.policy_gate_suppressed = !!opts.suppressed;
    }
    await route.fulfill({
      response: res,
      body: JSON.stringify(body),
      headers: { ...res.headers(), 'content-type': 'application/json' },
    });
  });
}

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

async function loginV1(page: any) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
}

test.describe('Policy re-consent gate', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('V2 gates at login and offers Remind me later within the grace window', async ({
    page,
  }) => {
    await stubConsent(page, 'grace');
    await loginV2(page);

    const gate = page.getByTestId('policy-consent-gate');
    await expect(gate).toBeVisible();
    await expect(gate).toContainText(/Privacy Policy/i);
    await expect(
      gate.getByRole('button', { name: /I have read and accept/i }),
    ).toBeVisible();
    // Dismissible during grace -- that is the whole difference from the expired gate.
    await expect(
      gate.getByRole('button', { name: /Remind me later/i }),
    ).toBeVisible();
  });

  test('V2 removes the dismiss once the grace window has expired', async ({
    page,
  }) => {
    await stubConsent(page, 'expired', { secondsRemaining: 0 });
    await loginV2(page);

    const gate = page.getByTestId('policy-consent-gate');
    await expect(gate).toBeVisible();
    // Anchored on a positive assertion first: without it "no dismiss button" would pass
    // on a gate that failed to render at all.
    await expect(
      gate.getByRole('button', { name: /I have read and accept/i }),
    ).toBeVisible();
    await expect(
      gate.getByRole('button', { name: /Remind me later/i }),
    ).toHaveCount(0);
  });

  test('V2 shows an escalated banner once dismissed inside the warning window', async ({
    page,
  }) => {
    await stubConsent(page, 'warning', { suppressed: true });
    await loginV2(page);

    // Dismissed, so the modal is gone and the banner is what remains.
    await expect(page.getByTestId('policy-consent-gate')).toHaveCount(0);
    const banner = page.getByTestId('policy-consent-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toHaveClass(/policy-consent-banner--urgent/);
    await expect(
      banner.getByRole('button', { name: /Review and accept/i }),
    ).toBeVisible();
  });

  test('V2 stays quiet when nothing is outstanding', async ({ page }) => {
    await stubConsent(page, null);
    await loginV2(page);

    // Anchored on the portal having rendered, so the absences below cannot pass on a
    // blank page.
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.getByTestId('policy-consent-gate')).toHaveCount(0);
    await expect(page.getByTestId('policy-consent-banner')).toHaveCount(0);
  });

  test('Remind me later is recorded on the server, not in the browser', async ({
    page,
  }) => {
    await stubConsent(page, 'grace');
    await loginV2(page);

    const gate = page.getByTestId('policy-consent-gate');
    await expect(gate).toBeVisible();

    // The point of the button: the dismissal has to be held against the SESSION so the
    // gate returns at the next login. A purely client-side dismissal would survive
    // logout and leave the banner applying no pressure at all.
    let posted = false;
    page.on('request', (req: any) => {
      if (
        req.url().includes('/api/me/policy-consent/remind-later') &&
        req.method() === 'POST'
      )
        posted = true;
    });

    await gate.getByRole('button', { name: /Remind me later/i }).click();
    await expect.poll(() => posted).toBe(true);
    await expect(gate).toHaveCount(0);
  });

  test('V1 gates at login within the grace window', async ({ page }) => {
    await stubConsent(page, 'grace');
    await loginV1(page);

    const modal = page.locator('#policy-consent-modal');
    await expect(modal).toBeVisible();
    await expect(modal).toContainText(/Privacy Policy/i);
    await expect(page.locator('#policy-consent-later')).toBeVisible();
  });

  test('V1 removes the dismiss once the grace window has expired', async ({
    page,
  }) => {
    await stubConsent(page, 'expired', { secondsRemaining: 0 });
    await loginV1(page);

    const modal = page.locator('#policy-consent-modal');
    await expect(modal).toBeVisible();
    await expect(modal).toContainText(/Privacy Policy/i);
    await expect(page.locator('#policy-consent-later')).toBeHidden();
  });

  test('V1 stays quiet when nothing is outstanding', async ({ page }) => {
    await stubConsent(page, null);
    await loginV1(page);

    await expect(page.locator('#nav-overview')).toBeVisible();
    await expect(page.locator('#policy-consent-modal')).toBeHidden();
    await expect(page.locator('#policy-consent-banner')).toBeHidden();
  });
});
