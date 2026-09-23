import type { Page } from '@playwright/test';
// The shared fixture, not @playwright/test's own: it fails a test on any uncaught page error,
// which is the difference between "the card did not render" and "the card threw".
import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Visitor session-key rotation, in BOTH portal arms (#2196, the portal half of #2184).
 *
 * The backend landed in #2195: `GET /api/admin/session-secrets` reports the current generation,
 * the schedule and the outcome of the last attempt, and `POST /api/admin/session-secrets/rotate`
 * is the manual trigger. This file is about the two views over it, and it is a parity spec
 * because a capability in one arm is not a capability (#2101, #2150, #2155, #2158).
 *
 * ## What each assertion is anchored on, and why
 *
 * "The control exists" is not worth asserting on its own -- a button wired to nothing, and a
 * card showing a hard-coded generation id, both satisfy it. So:
 *
 *   - the generation, the schedule and the connected-node roster are compared against what the
 *     API answers *in this run*, read back through the page's own session;
 *   - the rotation interval and the retirement lag are checked against a MOCKED status carrying
 *     values the gateway never produces (36h / 84h). A second copy hard-coded in a portal --
 *     which is what the endpoint states them to prevent -- cannot render those;
 *   - the abort case is mocked too. An abort needs a node that fails to acknowledge, and the
 *     base E2E stack has no edge; mocking the response exercises the real rendering path, which
 *     is the half this issue is about;
 *   - the manual trigger is asserted by the REQUEST it makes and by the outcome the page then
 *     shows, not by a click appearing to do something.
 *
 * ## Fixture
 *
 * Admin only, deliberately. Both endpoints sit behind requireAdmin and the rotate handler
 * re-reads the role from the database (#1760), and that authorisation is already covered by
 * TestANonAdminCannotRotateTheSessionKey in pkg/server/visitor_session_rotation_test.go -- a Go test
 * can assert the 403 without creating a portal user, and a user row created here outlives the
 * spec and widens a column another spec measures (e2e-testing SKILL §4). Nothing here creates
 * one.
 *
 * ## Staleness
 *
 * dashboard.html/dashboard.js are //go:embed-ed and ui/src is compiled into the image, so
 * nothing in this file means anything against a container built before the change. Rebuild
 * (`docker compose up -d --build`) and check the served bundle changed before believing a
 * result either way.
 */

const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml
const STATUS_URL = '**/api/admin/session-secrets';
const ROTATE_PATH = '/api/admin/session-secrets/rotate';

async function signInV1(page: Page) {
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

async function signInV2(page: Page) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

/**
 * An aborted status carrying values the gateway would never produce, so that anything rendering
 * them can only have got them from this response.
 *
 * The abort reason and both `why` strings are unique sentences: "the assertion must have exactly
 * one possible cause" (github-workflow §5c). A test looking for the word "failed" would be
 * satisfied by half the strings in the portal.
 */
const ABORTED_STATUS = {
  current_generation: 'gen-MOCK-CURRENT',
  accepted_generations: ['gen-MOCK-CURRENT', 'gen-MOCK-PENDING'],
  next_rotation_at: '2031-03-04T05:06:07Z',
  retiring_at: {},
  last_rotation: {
    at: '2031-03-03T05:06:07Z',
    trigger: 'periodic',
    outcome: 'aborted',
    generation: 'gen-MOCK-PENDING',
    previous_generation: 'gen-MOCK-CURRENT',
    acknowledged: ['edge-eu-MOCK'],
    unacknowledged: [
      { node_id: 'edge-us-MOCK', why: 'no acknowledgement inside the window' },
      {
        node_id: 'edge-sa-MOCK',
        why: 'the control connection dropped mid-push',
      },
    ],
    reason:
      'SENTINEL-ABORT-REASON: 2 of 3 connected node(s) did not confirm they hold the new generation',
  },
  connected_nodes: ['edge-eu-MOCK', 'edge-us-MOCK', 'edge-sa-MOCK'],
  // Neither of these is a value visitorSessionRotationInterval or visitorSessionRetirementLag
  // can produce, so a portal rendering "36h"/"84h" is reading them off the response.
  rotation_interval: '36h0m0s',
  retirement_lag: '84h0m0s',
};

async function mockAbortedStatus(page: Page) {
  await page.route(STATUS_URL, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(ABORTED_STATUS),
    }),
  );
}

test.describe('Visitor session keys, Portal V1', () => {
  test.beforeEach(async ({ page }) => {
    await signInV1(page);
  });

  test('the card is on the route a user takes, and shows what the API reports', async ({
    page,
  }) => {
    // A fresh document straight to the section, never a click through Maintenance first: a V1
    // loader wired to the wrong branch -- or to none -- leaves its markup permanently blank on
    // the route the user actually takes, and that has happened four times in this file
    // (#522/#525, #1785, #1995).
    await page.goto('/portal/system');

    const generation = page.locator('#session-keys-current-generation');
    await expect(generation).toBeVisible();

    // The page's own session, so this is the same answer the card was filled from.
    const res = await page.request.get('/api/admin/session-secrets');
    expect(res.status()).toBe(200);
    const status = await res.json();

    // Non-empty FIRST. Without it, "rendered === API" is satisfied by a blank card beside an
    // endpoint that answered with nothing, which is the vacuous-pass shape of e2e-testing §3.
    expect(status.current_generation).not.toEqual('');
    await expect(generation).toHaveText(status.current_generation);

    // The connected roster, which is what a commit is really gated on. Empty in the base stack
    // (no edge container), and the card must say so in words rather than leaving a blank.
    const nodes = page.locator('#session-keys-connected-nodes');
    await expect(nodes).toHaveText(
      status.connected_nodes && status.connected_nodes.length > 0
        ? status.connected_nodes.join(', ')
        : 'None connected right now',
    );

    // Formatted in the page, so this compares the VALUE rather than two locale conventions.
    const expectedNext = await page.evaluate(
      (iso) => new Date(iso).toLocaleString(),
      status.next_rotation_at,
    );
    await expect(page.locator('#session-keys-next-rotation')).toHaveText(
      expectedNext,
    );
  });

  test('an aborted rotation renders its reason and names the nodes that did not acknowledge', async ({
    page,
  }) => {
    await mockAbortedStatus(page);
    await page.goto('/portal/system');

    await expect(page.locator('#session-keys-last-outcome')).toHaveText(
      'Aborted',
    );
    // Manual or periodic, rendered. Without it a periodic rotation that keeps failing and a
    // manual one somebody is watching read identically.
    await expect(page.locator('#session-keys-last-trigger')).toHaveText(
      'Periodic',
    );

    // The reason, in full. A test for "Aborted" alone would pass on a card that says only that,
    // which is the invisible-abort problem this issue exists to remove.
    await expect(page.locator('#session-keys-last-reason')).toContainText(
      'SENTINEL-ABORT-REASON',
    );

    const unacked = page.locator('#session-keys-unacked');
    await expect(unacked).toContainText('edge-us-MOCK');
    await expect(unacked).toContainText('no acknowledgement inside the window');
    await expect(unacked).toContainText('edge-sa-MOCK');
    await expect(unacked).toContainText(
      'the control connection dropped mid-push',
    );
    // The node that DID acknowledge must not appear in the list of those that did not.
    await expect(unacked).not.toContainText('edge-eu-MOCK');

    // The interval and the lag come off the response. 36h/84h are not values the engine can
    // produce, so a portal holding its own copy of "24h"/"72h" fails here.
    const body = page.locator('#session-keys-body');
    await expect(body).toContainText('36h');
    await expect(body).toContainText('84h');
  });

  test('rotating asks first, and then shows the outcome the API returned', async ({
    page,
  }) => {
    await page.goto('/portal/system');
    await expect(
      page.locator('#session-keys-current-generation'),
    ).toBeVisible();

    // Playwright dismisses a native dialog unless a handler accepts it, so this click is a
    // cancelled confirmation. Nothing may be sent.
    let posted = 0;
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes(ROTATE_PATH)) posted += 1;
    });
    await page.click('#btn-rotate-session-keys');
    await expect
      .poll(() => posted, { timeout: 2000, intervals: [250] })
      .toBe(0);

    page.on('dialog', (d) => d.accept());
    const [response] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes(ROTATE_PATH) && r.request().method() === 'POST',
        { timeout: 20000 },
      ),
      page.click('#btn-rotate-session-keys'),
    ]);
    // 200 on a commit, 409 on an abort. Both are outcomes the card must render; a 409 that
    // reported a blanket failure would put the invisible abort back inside a status code.
    // Asserted on the STATUS, not the body: the outcome the card shows is compared against the
    // persisted record below, which is what the card is actually filled from.
    expect([200, 409]).toContain(response.status());

    // The gateway's own record of what just happened.
    const after = await (
      await page.request.get('/api/admin/session-secrets')
    ).json();
    expect(after.last_rotation).toBeTruthy();
    // Manual, because this test pressed the button. A periodic run recorded here instead would
    // mean the click did nothing and the scheduler happened to fire -- which is exactly the
    // "satisfied by the wrong cause" failure, and this distinguishes the two.
    expect(after.last_rotation.trigger).toBe('manual');
    expect(after.last_rotation.actor).toBe(adminEmail);

    await expect(page.locator('#session-keys-last-trigger')).toHaveText(
      'Manual',
    );
    await expect(page.locator('#session-keys-last-outcome')).toHaveText(
      after.last_rotation.outcome === 'committed' ? 'Committed' : 'Aborted',
    );
    // The card re-reads the status after a rotation, so the generation on screen must be the
    // one the gateway is minting with NOW -- not the one it held when the page loaded.
    await expect(page.locator('#session-keys-current-generation')).toHaveText(
      after.current_generation,
    );
  });
});

test.describe('Visitor session keys, Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await signInV2(page);
  });

  test('the card is on the settings page, and shows what the API reports', async ({
    page,
  }) => {
    await page.goto('/portalv2/admin/settings');

    const generation = page.getByTestId('session-keys-current');
    await expect(generation).toBeVisible();

    const res = await page.request.get('/api/admin/session-secrets');
    expect(res.status()).toBe(200);
    const status = await res.json();

    expect(status.current_generation).not.toEqual('');
    await expect(generation).toHaveText(status.current_generation);

    await expect(page.getByTestId('session-keys-connected')).toHaveText(
      status.connected_nodes && status.connected_nodes.length > 0
        ? status.connected_nodes.join(', ')
        : 'None connected right now',
    );

    const expectedNext = await page.evaluate(
      (iso) => new Date(iso).toLocaleString(),
      status.next_rotation_at,
    );
    await expect(page.getByTestId('session-keys-next')).toHaveText(
      expectedNext,
    );
  });

  test('an aborted rotation renders its reason and names the nodes that did not acknowledge', async ({
    page,
  }) => {
    await mockAbortedStatus(page);
    await page.goto('/portalv2/admin/settings');

    await expect(page.getByTestId('session-keys-last-outcome')).toHaveText(
      'Aborted',
    );
    await expect(page.getByTestId('session-keys-last-trigger')).toHaveText(
      'Periodic',
    );
    await expect(page.getByTestId('session-keys-reason')).toContainText(
      'SENTINEL-ABORT-REASON',
    );

    const unacked = page.getByTestId('session-keys-unacked');
    await expect(unacked).toContainText('edge-us-MOCK');
    await expect(unacked).toContainText('no acknowledgement inside the window');
    await expect(unacked).toContainText('edge-sa-MOCK');
    await expect(unacked).toContainText(
      'the control connection dropped mid-push',
    );
    await expect(unacked).not.toContainText('edge-eu-MOCK');

    const card = page.getByTestId('session-keys-card');
    await expect(card).toContainText('36h');
    await expect(card).toContainText('84h');
  });

  test('rotating asks first, and then shows the outcome the API returned', async ({
    page,
  }) => {
    await page.goto('/portalv2/admin/settings');
    await expect(page.getByTestId('session-keys-current')).toBeVisible();

    let posted = 0;
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes(ROTATE_PATH)) posted += 1;
    });

    // Opening the dialog must not rotate anything, and cancelling it must not either.
    await page.getByTestId('session-keys-rotate').click();
    await expect(page.getByTestId('session-keys-rotate-confirm')).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect
      .poll(() => posted, { timeout: 2000, intervals: [250] })
      .toBe(0);

    await page.getByTestId('session-keys-rotate').click();
    const [response] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes(ROTATE_PATH) && r.request().method() === 'POST',
        { timeout: 20000 },
      ),
      page.getByTestId('session-keys-rotate-confirm').click(),
    ]);
    expect([200, 409]).toContain(response.status());

    const after = await (
      await page.request.get('/api/admin/session-secrets')
    ).json();
    expect(after.last_rotation).toBeTruthy();
    expect(after.last_rotation.trigger).toBe('manual');
    expect(after.last_rotation.actor).toBe(adminEmail);

    await expect(page.getByTestId('session-keys-last-trigger')).toHaveText(
      'Manual',
    );
    await expect(page.getByTestId('session-keys-last-outcome')).toHaveText(
      after.last_rotation.outcome === 'committed' ? 'Committed' : 'Aborted',
    );
    await expect(page.getByTestId('session-keys-current')).toHaveText(
      after.current_generation,
    );
  });
});
