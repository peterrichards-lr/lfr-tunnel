import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1's CLI-handoff failure banner must look different from the states it replaces (#1744).
 *
 * `dashboard.js` sets `#handoff-alert` to `alert` while the POST to the CLI is in flight, then
 * to `alert alert-success` or `alert alert-warning` depending on the outcome. `.alert-warning`
 * had no rule in any stylesheet V1 loads, so the failure banner fell back to the bare `.alert`
 * base -- padding, radius, font size, no colour -- and rendered identically to the neutral
 * "Attempting to send token to CLI..." state it had just replaced. On the one screen where the
 * colour IS the message, success and failure were indistinguishable.
 *
 * THIS SPEC DELIBERATELY DOES NOT ASSERT THE CLASS NAME. Portal V2's equivalent
 * (`portal_v2_cli_handoff.spec.ts`) asserts `toHaveClass(/alert-banner--warning/)`, which is
 * right for V2 and would have passed against V1 throughout the entire life of the bug: the class
 * was present the whole time, it just styled nothing. A class-name assertion tests the markup;
 * the defect was in the stylesheet. So the assertion here is on COMPUTED STYLE, compared against
 * reference elements built in-page from the sibling alert classes. That is the claim a reader
 * cares about -- "the warning does not look like the other three" -- and it is the only form of
 * it that fails when the rule is deleted.
 *
 * Two V1 quirks the assertions are shaped around, both of which produce a confident wrong answer:
 *
 *  - `#handoff-alert` sits inside a modal that animates, so `toBeVisible()` is a race and an
 *    `opacity: 0` element is never "visible" to Playwright. Computed colour resolves regardless,
 *    so the styling claim is made through `getComputedStyle`, with modal visibility asserted
 *    separately and only as an anchor.
 *  - Toast text is mirrored into an `sr-only` live region, so an unscoped `getByText` trips
 *    strict mode. Every locator below is scoped to `#handoff-alert` or `#raw-token-display`.
 *
 * 4444 is routed explicitly on every path. It is a popular port -- Selenium Grid's default among
 * others -- so an unrouted test would pass or fail on whatever happens to be listening on the
 * machine running it, which is the definition of a flake.
 */

const HANDOFF_URL = 'http://127.0.0.1:4444/handoff';
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// Short, and revoked afterwards. The database is shared across the whole suite in file order,
// so a fixture that accumulates rows is a neighbour's layout bug (see the e2e-testing skill,
// "the database is shared").
const tokenName = 'hw-test';

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

/**
 * Computed colour of `#handoff-alert`, alongside the same two properties read from reference
 * elements carrying the other three alert states. The references are built in the live document
 * and mounted next to the banner, so they resolve against the same theme tokens and the same
 * cascade -- comparing against hard-coded rgb() strings would instead be asserting one theme's
 * palette, and would have to be rewritten every time a token moves.
 */
async function alertPalette(page: any) {
  return page.evaluate(() => {
    const el = document.getElementById('handoff-alert');
    if (!el) throw new Error('#handoff-alert is not in the document');
    const host = el.parentElement as HTMLElement;

    const read = (cls: string) => {
      const ref = document.createElement('div');
      ref.className = cls;
      ref.textContent = 'x';
      host.appendChild(ref);
      const cs = getComputedStyle(ref);
      const out = { color: cs.color, background: cs.backgroundColor };
      ref.remove();
      return out;
    };

    const live = getComputedStyle(el);
    return {
      className: el.className,
      text: el.textContent || '',
      live: { color: live.color, background: live.backgroundColor },
      neutral: read('alert'),
      warning: read('alert alert-warning'),
      success: read('alert alert-success'),
      error: read('alert alert-error'),
    };
  });
}

async function generateToken(page: any) {
  await page.locator('#nav-tokens').click();
  await page.getByRole('button', { name: 'Generate New Token' }).click();
  await expect(page.locator('#token-name')).toBeVisible();
  await page.fill('#token-name', tokenName);
  await page.getByRole('button', { name: 'Generate Token' }).click();
  // The result step is revealed before the handoff fires, deliberately: a hung or refused CLI
  // must never hide the thing the user came for.
  await expect(page.locator('#token-result-step')).not.toHaveClass(/hidden/);
  await expect(page.locator('#raw-token-display')).not.toBeEmpty();
}

test.describe('Portal V1 CLI handoff banner', () => {
  test.beforeEach(async ({ page }) => {
    await loginV1(page, adminEmail);
  });

  test.afterEach(async ({ page }) => {
    // Revoked through the API with the session this test already holds; `page.request` shares
    // the browser context's cookies, so no second login is needed.
    const res = await page.request.get('/api/tokens');
    if (!res.ok()) return;
    for (const t of ((await res.json()) as any[]) || []) {
      if (t.name === tokenName) {
        await page.request.delete(`/api/tokens/${t.id}`);
      }
    }
  });

  test('a refused handoff renders a warning that is visually distinct', async ({
    page,
  }) => {
    let attempted = 0;
    await page.route(HANDOFF_URL, async (route: any) => {
      attempted += 1;
      // What a machine with no `lfr-tunnel login` waiting actually does.
      await route.abort('connectionrefused');
    });

    await generateToken(page);

    const banner = page.locator('#handoff-alert');

    // Positive anchors first. An absence-only assertion -- "the warning does not look neutral"
    // -- is satisfied perfectly by a modal that rendered nothing at all, and would report that
    // as a pass.
    await expect(banner).toHaveText(/manually copy your token below/i);
    await expect(banner).toHaveText(/lfr-tunnel login/);
    await expect(page.locator('#raw-token-display')).not.toBeEmpty();

    const p = await alertPalette(page);

    // The class is applied -- necessary, and precisely the part that was never the problem.
    expect(p.className).toContain('alert-warning');

    // The claim #1744 is actually about. Delete the `.alert-warning` rule and every one of
    // these fails, because the banner collapses onto the bare `.alert` base.
    expect(p.live.color).not.toBe(p.neutral.color);
    expect(p.live.background).not.toBe(p.neutral.background);

    // ...and it must not be mistakable for the other two outcomes either. "Failure looks like
    // success" is the specific harm; "failure looks like an error" would be a different bug but
    // an equally wrong signal on a screen whose colour carries the message.
    expect(p.live.color).not.toBe(p.success.color);
    expect(p.live.background).not.toBe(p.success.background);
    expect(p.live.color).not.toBe(p.error.color);
    expect(p.live.background).not.toBe(p.error.background);

    // A colour that resolves to nothing satisfies every `not.toBe` above. Assert it is a real
    // paint, so "unstyled" cannot pass as "distinct".
    expect(p.live.color).toMatch(/^rgba?\(/);
    expect(p.live.background).not.toBe('rgba(0, 0, 0, 0)');
    expect(p.live.background).not.toBe('transparent');

    // The banner tells the user to copy the token by hand, so the means of doing so has to be
    // present alongside it.
    await expect(
      page.getByRole('button', { name: 'Copy to Clipboard' }),
    ).toBeEnabled();

    expect(attempted).toBe(1);
  });

  test('a delivered handoff stays distinct from the warning', async ({
    page,
  }) => {
    await page.route(HANDOFF_URL, async (route: any) => {
      await route.fulfill({ status: 200, body: '' });
    });

    await generateToken(page);

    const banner = page.locator('#handoff-alert');
    await expect(banner).toHaveText(/successfully delivered to your CLI/i);

    const p = await alertPalette(page);
    expect(p.className).toContain('alert-success');

    // The success banner is itself distinct from the neutral base -- the property the warning
    // was missing, asserted here so a regression that flattened BOTH would not leave this test
    // silently comparing two identical things and passing.
    expect(p.live.color).not.toBe(p.neutral.color);
    expect(p.live.background).not.toBe(p.neutral.background);

    // And the pair, from the other side: whatever `.alert-warning` becomes, it must not
    // converge on success. Read from reference elements rather than the live banner, so a
    // future edit that made the two rules identical fails in both tests rather than only in
    // whichever one happened to be written first.
    expect(p.warning.color).not.toBe(p.success.color);
    expect(p.warning.background).not.toBe(p.success.background);
  });
});
