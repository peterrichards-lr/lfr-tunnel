import type { Page } from '@playwright/test';
import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V2 dashboard sections by path (#2527).
 *
 * V1 gives tokens, reservations and tunnels a route each (`/portal/tokens`); V2 puts them all
 * on one dashboard. `/portalv2/dashboard/<section>` is what the V1 -> V2 redirects (#2528)
 * and the system emails land on, so arriving there has to put that section in front of the
 * reader -- not merely load the dashboard, which is what every one of these URLs would do if
 * the section part were ignored.
 *
 * Every positive check reads the section's position, so a page that rendered nothing fails
 * it: there is no element to measure. The control at the
 * start of the first test shows the same measurement reporting "not in view" for the same
 * sections on the plain dashboard, which is what makes a pass here
 * mean the URL moved the page.
 */

// Path segment -> element id. Mirrors ui/src/dashboardSections.ts; the first test checks this
// list covers every section the jump nav offers, so a section added there without a path here
// fails rather than going untested.
const SECTIONS: Array<[string, string]> = [
  ['tunnels', 'active-tunnels'],
  ['reservations', 'reservations-overview'],
  ['subdomains', 'registered-subdomains'],
  ['custom-domains', 'custom-domains'],
  ['custom-domain-status', 'custom-domain-status'],
  ['tokens', 'access-tokens'],
];

// How far below the top of the viewport a section may start and still count as the one
// arrived at. scroll-margin-top is 24px (.scroll-target in index.css); the slack is for the
// rounding of fractional layouts, not for a section that merely happens to be on screen.
const NEAR_TOP_PX = 120;

type Placement = { top: number; vh: number; bottomed: boolean } | null;

/**
 * Where the element with this id sits in the viewport, measured rather than asserted visible:
 * Custom Domain Status renders nothing for an account with no custom domain, so its wrapper
 * can be zero-height, and toBeVisible() would reject a perfectly well-placed empty section.
 *
 * `bottomed` is whether the page cannot scroll any further. The last sections are shorter
 * than the viewport, so the browser stops with them part-way down -- that is arrival too.
 */
async function placement(page: Page, id: string): Promise<Placement> {
  return page.evaluate((elId) => {
    const el = document.getElementById(elId);
    if (!el) return null;
    const atEnd = (s: Element | null) =>
      !s || s.scrollTop + s.clientHeight >= s.scrollHeight - 2;
    return {
      top: Math.round(el.getBoundingClientRect().top),
      vh: window.innerHeight,
      bottomed:
        atEnd(document.scrollingElement) &&
        atEnd(document.querySelector('.main-content')),
    };
  }, id);
}

function arrivedAt(p: Placement): boolean {
  return (
    !!p && p.top >= 0 && p.top < p.vh && (p.top <= NEAR_TOP_PX || p.bottomed)
  );
}

/** The dashboard's own heading has scrolled off the top: the page moved. */
async function headingScrolledAway(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const h = document.getElementById('dashboard-overview');
    return !!h && h.getBoundingClientRect().bottom <= 0;
  });
}

/**
 * Navigate inside the app, the way a link or the router would, without a page load: the
 * dashboard stays mounted and keeps whatever scroll position it had, which is the state the
 * "leaving a section" rules are about. React Router reads the address on popstate.
 */
async function clientNavigate(page: Page, path: string) {
  await page.evaluate((p) => {
    window.history.pushState({}, '', p);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }, path);
}

/** Arrive at a section in-app and wait until it is placed. */
async function arriveAt(page: Page, segment: string, id: string) {
  await clientNavigate(page, `/portalv2/dashboard/${segment}`);
  await expect(page).toHaveURL(new RegExp(`/portalv2/dashboard/${segment}$`));
  await expect
    .poll(async () => arrivedAt(await placement(page, id)), {
      message: `in-app arrival at /dashboard/${segment} should put #${id} in view`,
      timeout: 10_000,
    })
    .toBe(true);
  expect(await headingScrolledAway(page)).toBe(true);
}

/**
 * A scroll with no wheel, key or pointer behind it, as a screen reader or a script makes:
 * every candidate scroller is moved to the top.
 */
async function programmaticScrollToTop(page: Page) {
  await page.evaluate(() => {
    for (const s of [
      document.scrollingElement,
      document.querySelector('.main-content'),
    ]) {
      s?.scrollTo({ top: 0, behavior: 'instant' });
    }
  });
}

/** Grow the dashboard above every section, as a panel loading its data does. */
async function growDashboard(page: Page, px: number) {
  await page.evaluate((h) => {
    const root = document
      .getElementById('dashboard-overview')!
      .closest('.animate-fade-in')!;
    const spacer = document.createElement('div');
    spacer.style.height = `${h}px`;
    root.prepend(spacer);
  }, px);
}

test.describe('Portal V2 dashboard section links', () => {
  const userEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', userEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();

    const token = await getMagicLinkToken(userEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
  });

  // Three tests rather than one per section: every test signs in, and the control plane
  // rate-limits magic links by IP, which a local stack shares with everything else.
  test('each section URL lands with that section in view', async ({ page }) => {
    // CONTROL first, on the plain dashboard this test signed in to: the measurement used
    // below must report these sections as NOT arrived at here, or a pass below would say
    // nothing about the URL.
    await expect(
      page.getByRole('heading', { name: 'Dashboard Overview' }),
    ).toBeInViewport();
    await page.waitForLoadState('networkidle');
    for (const id of ['reservations-overview', 'access-tokens']) {
      const p = await placement(page, id);
      expect(p, `#${id} should exist on the dashboard`).not.toBeNull();
      expect(
        arrivedAt(p),
        `CONTROL: #${id} counted as arrived at on the plain dashboard: ${JSON.stringify(p)}`,
      ).toBe(false);
    }
    expect(
      await headingScrolledAway(page),
      'CONTROL: the plain dashboard should open at the top',
    ).toBe(false);

    // Coverage: the jump nav is the page's own list of its sections.
    const nav = page.getByRole('navigation', { name: 'Dashboard sections' });
    await expect(nav.locator('a').first()).toBeAttached();
    const navTargets = await nav
      .locator('a')
      .evaluateAll((links) =>
        links.map((l) => (l.getAttribute('href') || '').replace(/^#/, '')),
      );
    expect(navTargets.sort()).toEqual(SECTIONS.map(([, id]) => id).sort());

    for (const [segment, id] of SECTIONS) {
      await page.goto(`/portalv2/dashboard/${segment}`);
      await expect(page).toHaveURL(
        new RegExp(`/portalv2/dashboard/${segment}$`),
      );
      // Positive anchor: the dashboard itself rendered.
      await expect(
        page.getByRole('heading', { name: 'Dashboard Overview' }),
      ).toBeAttached();

      // Ten seconds, not the default five: a section below a panel that loads its own data
      // can only be placed once that data has arrived.
      let last: Placement = null;
      await expect
        .poll(
          async () => {
            last = await placement(page, id);
            return arrivedAt(last);
          },
          {
            message: `/dashboard/${segment} should put #${id} at the top of the view`,
            timeout: 10_000,
          },
        )
        .toBe(true)
        .catch((e: Error) => {
          throw new Error(
            `${e.message}\nlast placement: ${JSON.stringify(last)}`,
          );
        });
      expect(
        await headingScrolledAway(page),
        `/dashboard/${segment} should scroll the page, not leave it at the top`,
      ).toBe(true);

      // Still there once every panel above it has loaded its data and grown. One scroll on
      // arrival is undone by that growth, which pushes the section back down the page.
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      const settled = await placement(page, id);
      expect(
        arrivedAt(settled),
        `#${id} drifted out of view after the dashboard finished loading: ${JSON.stringify(settled)}`,
      ).toBe(true);
    }

    // A section is still the Overview page as far as the sidebar is concerned.
    const overview = page.locator('.sidebar a[href="/portalv2/dashboard"]');
    await expect(overview).toHaveClass(/\bactive\b/);
    await expect(overview).toHaveAttribute('aria-current', 'page');
  });

  test('an unknown section falls back to the top of the dashboard', async ({
    page,
  }) => {
    await page.goto('/portalv2/dashboard/no-such-section');
    // The address is corrected, so what is bookmarked next is a real page.
    await expect(page).toHaveURL(/\/portalv2\/dashboard$/);
    const heading = page.getByRole('heading', { name: 'Dashboard Overview' });
    await expect(heading).toBeVisible();
    await expect(heading).toBeInViewport();
    await expect(
      page.getByRole('navigation', { name: 'Dashboard sections' }),
    ).toBeVisible();
    await page.waitForLoadState('networkidle');
    expect(
      await headingScrolledAway(page),
      'an unknown section should leave the dashboard at the top',
    ).toBe(false);

    // The same, reached in-app from a dashboard that is scrolled down: falling back means the
    // top, not wherever the reader happened to be.
    await arriveAt(page, 'tokens', 'access-tokens');
    await clientNavigate(page, '/portalv2/dashboard/no-such-section');
    await expect(page).toHaveURL(/\/portalv2\/dashboard$/);
    await expect
      .poll(() => headingScrolledAway(page), {
        message:
          'an unknown section reached from a scrolled dashboard should show the top',
      })
      .toBe(false);
  });

  // Its own test, so a third sign-in: kept to three in all because of the rate limit noted
  // on the first test.
  test('leaving a section returns to the top, and a reader scroll ends re-anchoring', async ({
    page,
  }) => {
    await page.waitForLoadState('networkidle');

    // A scroll the reader makes without wheel, key or pointer must stop the page pulling
    // them back. The first growth is only a sanity check that the section is held (the
    // browser's own scroll anchoring may hold it too); what proves the rule is the second
    // growth, after scrolling to the top, where the browser does not anchor -- so only the
    // hook could pull the reader back down. The mutation that drops the scroll check makes
    // exactly that happen.
    await arriveAt(page, 'tokens', 'access-tokens');
    await growDashboard(page, 400);
    await expect
      .poll(async () => arrivedAt(await placement(page, 'access-tokens')), {
        message: 'growth above the section should leave it in place',
      })
      .toBe(true);
    await programmaticScrollToTop(page);
    await expect.poll(() => headingScrolledAway(page)).toBe(false);
    // Let the scroll event be delivered before the page grows. Scroll events fire on the next
    // frame; growing the page inside that frame would make the reader's scroll and the
    // panel's growth one event, which the hook (rightly) attributes to the growth.
    await page.evaluate(
      () =>
        new Promise((r) =>
          requestAnimationFrame(() => requestAnimationFrame(r)),
        ),
    );
    await growDashboard(page, 400);
    await page.waitForTimeout(500);
    expect(
      await headingScrolledAway(page),
      'a reader scroll was undone by re-anchoring when the page grew',
    ).toBe(false);

    // The sidebar's Overview from a section: back to the top.
    await arriveAt(page, 'reservations', 'reservations-overview');
    await page.locator('.sidebar a[href="/portalv2/dashboard"]').click();
    await expect(page).toHaveURL(/\/portalv2\/dashboard$/);
    await expect
      .poll(() => headingScrolledAway(page), {
        message: 'Overview from a section should show the top of the dashboard',
      })
      .toBe(false);

    // Back from a section to the plain dashboard: the same.
    await arriveAt(page, 'tokens', 'access-tokens');
    await page.goBack();
    await expect(page).toHaveURL(/\/portalv2\/dashboard$/);
    await expect
      .poll(() => headingScrolledAway(page), {
        message: 'Back to /dashboard from a section should show the top',
      })
      .toBe(false);
  });
});
