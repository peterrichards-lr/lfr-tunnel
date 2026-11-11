import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * Analytics PDF export, both portals (#2430).
 *
 * - The four wide sections print on landscape pages and the rest on portrait. Read off the PDF
 *   Chromium actually produces, page by page, not off the stylesheet.
 * - Charts are redrawn at their printed size on beforeprint and handed back on afterprint,
 *   instead of the screen bitmap being scaled onto paper.
 * - A person with no traffic is told so, rather than shown a heading over nothing.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV2(page: any, email: string) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

async function loginV1(page: any, email: string) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

// The Portal Usage chart is the one portrait chart that does not need tunnel traffic, and its data
// is a visit ping each portal sends on load -- which can land after the analytics query. So the
// ping is sent and awaited here, and the page reloaded until the chart is there, rather than
// the test depending on which request won.
async function waitForPortraitChart(page: any, portal: string, canvas: any) {
  const ping = await page.request.get(`/api/analytics/ping?portal=${portal}`);
  expect(ping.ok(), 'analytics ping').toBeTruthy();
  await expect(async () => {
    await page.reload();
    if (portal === 'v1') await page.click('#nav-analytics');
    await expect(canvas).toBeVisible({ timeout: 3000 });
  }).toPass({ timeout: 30000 });
}

async function openV2Analytics(page: any) {
  await loginV2(page, adminEmail);
  await page.goto('/portalv2/admin/analytics');
  await waitForPortraitChart(
    page,
    'v2',
    page.locator('.card:not(.print-wide) canvas').first(),
  );
  await expect(page.locator('.print-wide').first()).toBeVisible();
}

async function openV1Analytics(page: any) {
  await loginV1(page, adminEmail);
  await page.click('#nav-analytics');
  await waitForPortraitChart(
    page,
    'v1',
    page.locator('#tab-analytics .glass:not(.print-wide) canvas').first(),
  );
  await expect(
    page.locator('#tab-analytics .print-wide').first(),
  ).toBeVisible();
}

/** Each page's orientation, in order, read from the PDF's MediaBox entries. */
function pageOrientations(pdf: Buffer): string[] {
  const text = pdf.toString('latin1');
  const out: string[] = [];
  for (const m of text.matchAll(
    /\/MediaBox\s*\[\s*0\s+0\s+([\d.]+)\s+([\d.]+)\s*\]/g,
  )) {
    out.push(Number(m[1]) > Number(m[2]) ? 'L' : 'P');
  }
  return out;
}

/** Portrait pages, then the landscape run, then portrait again -- and nothing else. */
function expectWideRunBetweenPortrait(orientations: string[]) {
  const seq = orientations.join('');
  expect(seq, `page orientations ${seq}`).toMatch(/^P+L+P+$/);
}

type CanvasSize = { css: number; bitmap: number; wide: boolean };

/** The first visible chart canvas in a portrait card and in a wide card. */
async function chartSizes(page: any, cardSelector: string) {
  return page.evaluate((cardSel: string) => {
    const pick = (wide: boolean) => {
      for (const c of Array.from(document.querySelectorAll('canvas'))) {
        const card = c.closest(cardSel);
        if (!card || c.getBoundingClientRect().width === 0) continue;
        if (!!card.closest('.print-wide') !== wide) continue;
        return {
          css: Math.round(c.getBoundingClientRect().width),
          bitmap: (c as HTMLCanvasElement).width,
          wide,
        };
      }
      return null;
    };
    return { portrait: pick(false), wide: pick(true) };
  }, cardSelector) as Promise<{
    portrait: CanvasSize | null;
    wide: CanvasSize | null;
  }>;
}

async function expectChartsResizedForPrint(page: any, cardSelector: string) {
  const before = await chartSizes(page, cardSelector);
  // Anchor: there is a chart to resize. Without one, everything below would hold vacuously.
  expect(before.portrait, 'a chart in a portrait section').not.toBeNull();

  await page.evaluate(() => window.dispatchEvent(new Event('beforeprint')));
  const during = await chartSizes(page, cardSelector);
  // Redrawn, not scaled: the bitmap is the printed width at the print pixel ratio.
  expect(during.portrait!.bitmap).toBe(during.portrait!.css * 2);
  expect(during.portrait!.css).toBeLessThanOrEqual(680);
  expect(during.portrait!.css).not.toBe(before.portrait!.css);
  if (during.wide) {
    // A chart on a landscape page is wider than any portrait page allows.
    expect(during.wide.css).toBeGreaterThan(680);
    expect(during.wide.bitmap).toBe(during.wide.css * 2);
  }

  await page.evaluate(() => window.dispatchEvent(new Event('afterprint')));
  const after = await chartSizes(page, cardSelector);
  expect(after.portrait!.css).toBe(before.portrait!.css);
  expect(after.portrait!.bitmap).toBe(before.portrait!.bitmap);
}

// One login per portal: every test here signs the admin in by magic link, and four in quick
// succession made one wait out the mail poll.
test.describe('Analytics PDF export', () => {
  test('V2: charts redraw at printed size; wide sections print landscape', async ({
    page,
  }) => {
    await openV2Analytics(page);
    await expectChartsResizedForPrint(page, '.card');
    expectWideRunBetweenPortrait(
      pageOrientations(await page.pdf({ preferCSSPageSize: true })),
    );
  });

  test('V1: charts redraw at printed size; wide sections print landscape', async ({
    page,
  }) => {
    await openV1Analytics(page);
    await expectChartsResizedForPrint(page, '.glass');
    expectWideRunBetweenPortrait(
      pageOrientations(await page.pdf({ preferCSSPageSize: true })),
    );
  });
});

test.describe('Personal usage with no traffic', () => {
  // Removed afterwards: a leftover row widens the Admin Users table that a later spec measures.
  const email = `pe${Date.now().toString().slice(-6)}@lfr-demo.local`;

  test.beforeAll(async () => {
    await createApprovedUser(email);
  });

  test.afterAll(async () => {
    await deleteUser(email);
  });

  test('V2 says so instead of a bare heading', async ({ page }) => {
    await loginV2(page, email);
    await page.goto('/portalv2/analytics');
    await expect(
      page.getByRole('heading', { name: 'Personal Usage' }),
    ).toBeVisible();
    await expect(page.getByTestId('personal-usage-empty')).toBeVisible();
  });

  test('V1 says so instead of two empty cards', async ({ page }) => {
    await loginV1(page, email);
    await page.click('#nav-analytics-personal');
    await expect(page.locator('#tab-analytics')).toBeVisible();
    await expect(page.getByTestId('personal-usage-empty')).toBeVisible();
    await expect(page.locator('#my-usage-grid')).toBeHidden();
  });
});
