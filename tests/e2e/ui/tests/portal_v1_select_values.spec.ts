import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1 selects must always show the value that is actually stored (#1201).
 *
 * Assigning a <select> a value none of its <option>s carry leaves selectedIndex at -1,
 * which renders as an empty box -- no text, just the chevron. The user is shown nothing
 * at all rather than what is configured.
 *
 * This is not hypothetical. Portal V2 offers a 'liferay' theme V1 has no option for, and
 * the server stores theme_preference as free text with no whitelist (pkg/db/schema.go:23,
 * pkg/server/api_service_user.go:33 both accept any string). Anyone who picks "Liferay
 * Waffle" in V2 and then opens V1 Account Settings gets a blank control. Confirmed
 * against an unfixed build: selectedIndex -1, and data-theme left on 'liferay', which V1
 * has no stylesheet for.
 *
 * The same shape applies to System Settings' Default Domain Fallback when default_domain
 * names a domain absent from supported_domains.
 */
test.describe('Portal V1 selects never render blank', () => {
  const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

  // Waiting for "the profile has been applied" needs care: #acc-theme exists in the
  // static markup already selected on "System Default", so sampling it too early reads
  // as a pass and tests nothing. Rather than mutate the profile to get a signal, wait
  // until the control leaves that pristine state. Only valid for stored themes other
  // than 'system' -- which is exactly what these tests use.
  async function waitForProfileApplied(page: import('@playwright/test').Page) {
    await expect
      .poll(
        () =>
          page
            .locator('#acc-theme')
            .evaluate((el: HTMLSelectElement) =>
              el.selectedIndex === 0 && el.value === 'system'
                ? 'pristine'
                : 'applied',
            ),
        {
          message: 'portal never applied the stored profile to #acc-theme',
          timeout: 20000,
        },
      )
      .toBe('applied');
  }

  async function signIn(page: import('@playwright/test').Page) {
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
  }

  async function loadAccountWith(
    page: import('@playwright/test').Page,
    theme: string,
  ) {
    const res = await page.request.put('/api/me', {
      data: { theme_preference: theme },
    });
    expect(res.ok()).toBeTruthy();

    // After magic-link sign-in the portal rewrites the URL to /admin, so navigating to
    // /admin#account is a same-document hash change, not a reload -- the page would keep
    // the profile it fetched at sign-in and never see the value just written. Force a
    // real document load.
    await page.goto('/admin#account');
    await page.reload();
    await waitForProfileApplied(page);
  }

  test.beforeEach(async ({ page, context }) => {
    await clearMailpit();
    await context.addInitScript(() => {
      window.localStorage.setItem('v2_promo_dismissed', 'true');
    });
    await signIn(page);
  });

  test.afterEach(async ({ page }) => {
    // Restore, so an unknown theme cannot leak into whichever spec runs next.
    await page.request.put('/api/me', { data: { theme_preference: 'system' } });
  });

  test('liferay, chosen in Portal V2, is a real option in V1', async ({
    page,
  }) => {
    await loadAccountWith(page, 'liferay');

    const state = await page
      .locator('#acc-theme')
      .evaluate((el: HTMLSelectElement) => ({
        selectedIndex: el.selectedIndex,
        shown:
          el.selectedIndex >= 0
            ? el.options[el.selectedIndex].textContent
            : null,
        value: el.value,
      }));

    // selectedIndex is the actual defect. toBeVisible() passes on a blank select -- the
    // element is present and sized, it just paints no text.
    expect(state.selectedIndex).toBeGreaterThanOrEqual(0);
    expect(state.shown?.trim()).toBeTruthy();
    expect(state.shown).toContain('Liferay Waffle');
    // The stored preference must be reported back, not quietly rewritten to a default:
    // showing "System Default" for a user whose setting is 'liferay' is a different lie.
    expect(state.value).toBe('liferay');

    // It is now a first-class option rather than the "(unsupported in Portal V1)" placeholder
    // #1201 had to invent, because both portals read one shared set of theme files (#1522).
    const strays = await page
      .locator('#acc-theme')
      .evaluate(
        (el: HTMLSelectElement) =>
          el.querySelectorAll('option[data-unknown-value]').length,
      );
    expect(strays).toBe(0);
  });

  test('liferay actually renders in V1, rather than falling back', async ({
    page,
  }) => {
    await loadAccountWith(page, 'liferay');

    // The point of sharing the theme files (#1522): V1 owns `liferay` now. Before, V1 had no
    // stylesheet for it, so applyTheme rewrote it to system and the user silently got
    // something else.
    const dataTheme = await page.evaluate(() =>
      document.documentElement.getAttribute('data-theme'),
    );
    expect(dataTheme).toBe('liferay');

    // Rendering it is what matters, not just the attribute: the tokens have to resolve, which
    // they only do if the shared stylesheet was served and linked.
    const bg = await page.evaluate(() =>
      getComputedStyle(document.documentElement)
        .getPropertyValue('--bg-base')
        .trim(),
    );
    expect(bg).toBeTruthy();
  });

  test('an unknown theme does not leave the page on an unstyled data-theme', async ({
    page,
  }) => {
    // A value NEITHER portal knows. This used to be 'liferay', which V1 had no stylesheet for
    // -- setting data-theme to it fell back to :root defaults and ignored the OS setting
    // entirely. V1 owns liferay since #1522, so the case needs a value that is genuinely
    // unknown; theme_preference is still free text, so one can still arrive.
    await loadAccountWith(page, 'no-such-theme');

    const dataTheme = await page.evaluate(() =>
      document.documentElement.getAttribute('data-theme'),
    );
    expect(['light', 'dark']).toContain(dataTheme);
  });

  test('a theme V1 does own is selected normally', async ({ page }) => {
    await loadAccountWith(page, 'dark');

    const select = page.locator('#acc-theme');
    await expect(select).toHaveValue('dark');

    const shown = await select.evaluate(
      (el: HTMLSelectElement) => el.options[el.selectedIndex].textContent,
    );
    expect(shown).toContain('Dark');

    // The placeholder option exists only for values with no home; it must not linger once
    // a real one is selected, or the dropdown grows a junk entry per visit.
    const strays = await select.evaluate(
      (el: HTMLSelectElement) =>
        el.querySelectorAll('option[data-unknown-value]').length,
    );
    expect(strays).toBe(0);
  });
});
