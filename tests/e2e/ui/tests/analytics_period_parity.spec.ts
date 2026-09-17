import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The Analytics period is stated on the page, and it is the period the server honoured (#1981).
 *
 * The screen had a range control but no time dimension anywhere on it: every figure was an
 * unlabelled aggregate. So metrics recorded before the v1.48.35 watermark fix (#1970) -- inflated
 * about 54x -- could not be told apart from corrected ones, and "last week versus this week" was
 * a question the page could not answer at all.
 *
 * The trap this guards is a selector that renders but does not filter. Asserting only that the
 * control appears, or that its options are right, passes on a dropdown wired to nothing.
 *
 * Division of labour, deliberately: whether the WINDOW is applied to the data is proved by
 * pkg/db/analytics_period_test.go, which seeds rows either side of a boundary and requires the
 * totals to change. This file proves the other half, which no Go test can see -- that both
 * portals ask for the window the reader picked and print the bounds the server answered with.
 * The bounds are read off the response, so a cosmetic control leaves the line unchanged and
 * these tests go red.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// "Showing 2026-09-16 14:03 to 2026-09-17 14:03 (UTC)" -> the two instants.
const BOUNDS =
  /(\d{4}-\d{2}-\d{2} \d{2}:\d{2}).*?(\d{4}-\d{2}-\d{2} \d{2}:\d{2})/;

function spanHours(text: string): number {
  const m = text.match(BOUNDS);
  expect(m, `period line did not carry two UTC bounds: ${text}`).not.toBeNull();
  const from = Date.parse(`${m![1].replace(' ', 'T')}:00Z`);
  const to = Date.parse(`${m![2].replace(' ', 'T')}:00Z`);
  return (to - from) / 3_600_000;
}

test.describe('Analytics period bounds', () => {
  test('V2 states the window and restates it when the range changes', async ({
    page,
  }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/analytics');
    await expect(
      page.getByRole('heading', { name: /Analytics/i }),
    ).toBeVisible();

    const period = page.getByTestId('analytics-period');
    // A positive assertion first: an absence check alone passes on a blank page.
    await expect(period).toBeVisible();

    // Default is 30 days, and it must be bounded -- an all-time default is what made one
    // anomalous historical session distort every number on the screen permanently.
    const thirty = (await period.textContent()) || '';
    expect(Math.round(spanHours(thirty))).toBe(30 * 24);

    await page.getByLabel('Time range').selectOption('1');
    await expect(period).not.toHaveText(thirty);
    const day = (await period.textContent()) || '';
    // 24 hours, not "since midnight yesterday". The floor used to round down to a date, so a
    // sub-day window was inexpressible and this assertion could not have been written.
    expect(Math.round(spanHours(day))).toBe(24);

    await page.getByLabel('Time range').selectOption('0');
    const all = (await period.textContent()) || '';
    expect(all).not.toBe(day);
    // All Time has no lower bound, so it must NOT render a from-to span.
    expect(all).not.toMatch(BOUNDS);
    expect(
      all.length,
      'All Time must still say what it covers',
    ).toBeGreaterThan(0);
  });

  test('V1 states the same window, from the same response', async ({
    page,
  }) => {
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
    await page.click('#nav-analytics');
    await expect(page.locator('#tab-analytics')).toBeVisible();

    const period = page.locator('#analytics-period-bounds');
    await expect(period).toBeVisible();
    const thirty = (await period.textContent()) || '';
    expect(Math.round(spanHours(thirty))).toBe(30 * 24);

    await page.locator('#analytics-range').selectOption('1');
    await expect(period).not.toHaveText(thirty);
    expect(Math.round(spanHours((await period.textContent()) || ''))).toBe(24);

    await page.locator('#analytics-range').selectOption('0');
    expect((await period.textContent()) || '').not.toMatch(BOUNDS);
  });

  test('the API reports the window it honoured, and a per-gateway breakdown inside it', async ({
    page,
  }) => {
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

    const day = await (await page.request.get('/api/analytics?days=1')).json();
    const all = await (await page.request.get('/api/analytics?days=0')).json();

    expect(day.period, 'every response states its window').toBeTruthy();
    expect(day.period.days).toBe(1);
    expect(Date.parse(day.period.to) - Date.parse(day.period.from)).toBe(
      24 * 3_600_000,
    );
    // Unbounded is expressed as an empty `from`, which is what both portals branch on.
    expect(all.period.from).toBe('');
    expect(all.period.days).toBe(0);
    expect(
      day.period.from,
      'a bounded window and an unbounded one must not resolve alike',
    ).not.toBe(all.period.from);

    // The per-node breakdown the issue asks for: totals scoped to the window, and a row for
    // every known gateway so one that has stopped reporting shows as a zero rather than
    // vanishing from the table.
    expect(day.global, 'an admin receives the global block').toBeTruthy();
    expect(day.global).toHaveProperty('node_totals');
    expect(day.global).toHaveProperty('totals');
    expect(Array.isArray(day.global.node_totals)).toBeTruthy();
    expect(day.global.node_totals.length).toBeGreaterThan(0);
    for (const n of day.global.node_totals) {
      expect(n).toHaveProperty('node_id');
      expect(n).toHaveProperty('bytes_in');
      expect(n).toHaveProperty('bytes_out');
      expect(n).toHaveProperty('sessions');
    }
    // The headline must be the breakdown's own sum, not a second query against a database that
    // has moved on -- otherwise the figure and the table under it can report different periods.
    const summed = day.global.node_totals.reduce(
      (acc: number, n: any) => acc + n.bytes_in + n.bytes_out,
      0,
    );
    expect(day.global.totals.bytes_in + day.global.totals.bytes_out).toBe(
      summed,
    );
  });
});
