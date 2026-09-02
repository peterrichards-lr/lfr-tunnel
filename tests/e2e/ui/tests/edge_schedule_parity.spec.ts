import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The power schedule on Edge Gateways, in BOTH portals (#1689).
 *
 * The screen showed a node's status and its own local time and nothing about its schedule,
 * so `edge-apac  disabled  02:57` read as a fault when 02:57 in Tokyo is squarely inside
 * that node's configured stop window. Showing the local time without the schedule is the
 * worst of both: enough to notice something is odd, not enough to explain it.
 *
 * Two specs per portal, deliberately. V1 and V2 are a live A/B test, so a capability in one
 * arm and not the other is a difference between the arms rather than merely a gap -- and it
 * is invisible from the other side.
 *
 * The health payload is stubbed rather than driven off real nodes. The e2e stack configures
 * no `edge_nodes` at all, so the table is empty here; and even with one, "is it inside its
 * window right now?" would depend on the wall clock when the suite ran. The stub below is
 * built RELATIVE to now (see makeNodes) so the asleep node is deterministically asleep and
 * the awake one deterministically awake, whatever time CI starts.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

const HOUR_MS = 60 * 60 * 1000;

function hhmmUTC(d: Date): string {
  return `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')}`;
}

/**
 * Three nodes, one per state the screen has to tell apart:
 *
 *   edge-night   stopped an hour ago, starts in three -- expected, not an incident.
 *   edge-day     inside its running hours, stops in three -- online.
 *   edge-dark    offline with no schedule at all -- this one IS an incident.
 *
 * None of those ids contains "asleep", "offline" or "online". A first draft named them
 * asleep-node/awake-node/dark-node and a row-wide toContainText(/asleep/i) then passed
 * against a deliberately severed classifier -- the id alone satisfied it.
 *
 * Times are in UTC and expressed as offsets from now, so the window is always the same
 * shape relative to the moment the test runs. The wrap over midnight is handled by the
 * modular arithmetic under test, so a run at 23:30 exercises it for free.
 */
function makeNodes() {
  const now = Date.now();
  const asleepStop = hhmmUTC(new Date(now - HOUR_MS));
  const asleepStart = hhmmUTC(new Date(now + 3 * HOUR_MS));
  const awakeStart = hhmmUTC(new Date(now - HOUR_MS));
  const awakeStop = hhmmUTC(new Date(now + 3 * HOUR_MS));

  return {
    payload: {
      nodes: {
        'edge-night': {
          status: 'Disabled',
          resolved_ip: '203.0.113.10',
          resolved_ipv4: '203.0.113.10',
          latency_ms: 0,
          last_check_at: Math.floor(now / 1000),
          error_message: '',
          version: 'v1.48.22',
          online_since: 0,
          timezone: 'UTC',
          schedule_enabled: true,
          schedule_stop_time: asleepStop,
          schedule_start_time: asleepStart,
        },
        'edge-day': {
          status: 'Online',
          resolved_ip: '203.0.113.11',
          resolved_ipv4: '203.0.113.11',
          latency_ms: 12,
          last_check_at: Math.floor(now / 1000),
          error_message: '',
          version: 'v1.48.22',
          online_since: Math.floor(now / 1000) - 600,
          timezone: 'UTC',
          schedule_enabled: true,
          schedule_stop_time: awakeStop,
          schedule_start_time: awakeStart,
        },
        'edge-dark': {
          status: 'Offline',
          resolved_ip: '203.0.113.12',
          resolved_ipv4: '203.0.113.12',
          latency_ms: 0,
          last_check_at: Math.floor(now / 1000),
          error_message: 'dial tcp: i/o timeout',
          version: 'v1.48.22',
          online_since: 0,
        },
      },
      outbound_ok: true,
      edge_power_actions_enabled: false,
    },
    asleepWindow: `${asleepStart}–${asleepStop}`,
    awakeWindow: `${awakeStart}–${awakeStop}`,
  };
}

// Torn down in afterEach: a route handler that outlives its test throws
// "route.fetch: Test ended" when a later navigation triggers it.
async function stubEdgeHealth(page: any, payload: unknown) {
  await page.route('**/api/portal/edge-health', async (route: any) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(payload),
    });
  });
}

test.describe('Edge gateway power schedule', () => {
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('Portal V1 shows the schedule and distinguishes asleep from offline', async ({
    page,
  }) => {
    const { payload, asleepWindow, awakeWindow } = makeNodes();
    await stubEdgeHealth(page, payload);

    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();

    await page.goto('/admin/network-health');
    await page.reload();

    // Positive anchor first: every assertion below is about the contents of a row, and a
    // page that rendered no rows at all would satisfy several of them (e2e skill §3).
    const asleepRow = page.locator('#network-health-body tr', {
      hasText: 'edge-night',
    });
    await expect(asleepRow).toHaveCount(1);
    await expect(
      page.locator('#network-health-body tr', { hasText: 'edge-dark' }),
    ).toHaveCount(1);

    // The column header exists at all -- this is the "Schedule" the issue asked for.
    await expect(
      page.locator('#tab-network-health th', { hasText: 'Schedule' }),
    ).toHaveCount(1);

    // 1. The schedule is on the row, with the node's own timezone.
    await expect(asleepRow).toContainText(`${asleepWindow} UTC`);
    // 2. And the next transition, which is the part that answers "is this fine?".
    await expect(asleepRow.locator('.edge-schedule-next')).toContainText(
      /starts in/i,
    );
    // 3. It reads as asleep, not as the bare "Disabled" that also covers an operator stop.
    //    Scoped to the status cell, not the row: an id containing the word would satisfy a
    //    row-wide check no matter what the badge said (that is how this assertion was first
    //    written, and a mutation test caught it passing against a severed classifier).
    await expect(asleepRow.locator('.edge-status')).toContainText(/asleep/i);
    await expect(asleepRow.locator('.edge-status-dot--asleep')).toHaveCount(1);

    // The awake node counts down to its stop instead, and keeps its online treatment.
    const awakeRow = page.locator('#network-health-body tr', {
      hasText: 'edge-day',
    });
    await expect(awakeRow).toContainText(`${awakeWindow} UTC`);
    await expect(awakeRow.locator('.edge-schedule-next')).toContainText(
      /stops in/i,
    );
    await expect(awakeRow.locator('.edge-status-dot--online')).toHaveCount(1);

    // The whole point: a node that is dark with no schedule must NOT look asleep.
    const darkRow = page.locator('#network-health-body tr', {
      hasText: 'edge-dark',
    });
    await expect(darkRow.locator('.edge-status-dot--offline')).toHaveCount(1);
    await expect(darkRow.locator('.edge-status-dot--asleep')).toHaveCount(0);
    await expect(darkRow.locator('.edge-status')).toContainText(/offline/i);
    await expect(darkRow.locator('.edge-status')).not.toContainText(/asleep/i);
  });

  test('Portal V2 shows the schedule and distinguishes asleep from offline', async ({
    page,
  }) => {
    const { payload, asleepWindow, awakeWindow } = makeNodes();
    await stubEdgeHealth(page, payload);

    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');

    await page.goto('/portalv2/admin/edge-health');

    const asleepRow = page.locator('tbody tr', { hasText: 'edge-night' });
    await expect(asleepRow).toHaveCount(1);
    await expect(
      page.locator('tbody tr', { hasText: 'edge-dark' }),
    ).toHaveCount(1);

    // Titled the same as V1's, so the two arms describe one feature rather than two
    // similarly-shaped ones.
    await expect(page.locator('th', { hasText: 'Schedule' })).toHaveCount(1);

    await expect(asleepRow).toContainText(`${asleepWindow} UTC`);
    await expect(asleepRow).toContainText(/starts in/i);
    await expect(asleepRow.locator('.badge-asleep')).toHaveText(/asleep/i);

    const awakeRow = page.locator('tbody tr', { hasText: 'edge-day' });
    await expect(awakeRow).toContainText(`${awakeWindow} UTC`);
    await expect(awakeRow).toContainText(/stops in/i);
    await expect(awakeRow.locator('.badge-success')).toHaveText(/online/i);

    const darkRow = page.locator('tbody tr', { hasText: 'edge-dark' });
    await expect(darkRow.locator('.badge-danger')).toHaveText(/offline/i);
    await expect(darkRow.locator('.badge-asleep')).toHaveCount(0);

    // "asleep" is its own filter value, not a flavour of "disabled": filtering to offline
    // is how an operator asks "what is actually broken?", and a sleeping node must not
    // answer it.
    await page
      .getByRole('combobox', { name: 'Filter by status' })
      .selectOption('offline');
    await expect(
      page.locator('tbody tr', { hasText: 'edge-dark' }),
    ).toHaveCount(1);
    await expect(
      page.locator('tbody tr', { hasText: 'edge-night' }),
    ).toHaveCount(0);
  });
});
