import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * V1 form labelling (#1584).
 *
 * Every table search box was unlabelled -- 13 of them, all from one generator in renderTable.
 * A placeholder is not an accessible name: screen readers do not announce it as a label, and it
 * vanishes the moment someone types, so the field had no name exactly while in use.
 *
 * V2 has had portal_v2_form_labels for a while; V1 had no equivalent, which is how this went
 * unnoticed. This is the V1 counterpart, written as a sweep rather than a list of known fields
 * so the NEXT unlabelled control fails here instead of accumulating quietly.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// Enough of V1 to cover every table, including the analytics page which carries three.
const SECTIONS = [
  'overview',
  'tokens',
  'reservations',
  'tunnels',
  'analytics',
  'registrations',
  'users',
  'blacklist',
  'audit',
  'magic',
  'network-health',
  'backups',
  'custom-domains',
  'account',
];

test.describe('Portal V1 form labels', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test('every visible control has an accessible name', async ({ page }) => {
    const unlabelled: string[] = [];
    let examined = 0;

    for (const section of SECTIONS) {
      await page.goto(`/admin/${section}`);
      await page.waitForTimeout(600);

      const result = await page.evaluate((sec) => {
        const bad: string[] = [];
        let seen = 0;
        document.querySelectorAll('input, select, textarea').forEach((el) => {
          const c = el as HTMLInputElement;
          if (c.type === 'hidden') return;
          if (c.offsetParent === null) return; // not on this section
          seen++;
          const named =
            (c.id &&
              document.querySelector(`label[for="${CSS.escape(c.id)}"]`)) ||
            c.getAttribute('aria-label') ||
            c.getAttribute('aria-labelledby') ||
            c.closest('label') ||
            c.getAttribute('title');
          if (!named) {
            bad.push(
              `${sec}: <${c.tagName.toLowerCase()} id="${c.id || '(none)'}">`,
            );
          }
        });
        return { bad, seen };
      }, section);

      unlabelled.push(...result.bad);
      examined += result.seen;
    }

    // Anchor first. Without this the sweep passes on a portal that rendered no controls at all,
    // which is the same shape of vacuous pass that hid the original problem.
    expect(
      examined,
      'the sweep should have examined a meaningful number of controls',
    ).toBeGreaterThan(15);

    expect(
      unlabelled,
      `controls with no accessible name:\n${unlabelled.join('\n')}`,
    ).toEqual([]);
  });

  // Analytics carries four tables. A generic "Search" on each would satisfy the sweep above
  // while leaving them indistinguishable to anyone navigating by form control.
  //
  // Asked of the name generator rather than counted off the rendered inputs, since #1920:
  // three of those four panels now render no table at all when they have nothing to show,
  // and that is this stack's permanent state -- no MaxMind database, no region probes. So
  // counting visible search boxes measured the fixture's data, not the naming, and the
  // count fell to one. searchLabelFor() is production code, and renderTable() labels every
  // box it creates with exactly this call, so asking it directly covers all four panels
  // instead of whichever happened to have rows today.
  test('search boxes on one page are named distinctly', async ({ page }) => {
    await page.goto('/admin/analytics');
    await page.waitForTimeout(800);

    const names = await page.evaluate(() => {
      const labelFor = (window as unknown as Record<string, unknown>)
        .searchLabelFor;
      if (typeof labelFor !== 'function') return null;
      return Array.from(
        document.querySelectorAll('#tab-analytics tbody[id$="-table-body"]'),
      ).map((el) => String((labelFor as (id: string) => string)(el.id)));
    });

    // Anchor: a null, or a list shorter than the panels actually on the page, means the
    // selector or the generator moved and every assertion below would pass on nothing.
    expect(names, 'searchLabelFor() was not found on the page').not.toBeNull();
    expect(names!.length).toBeGreaterThan(2);
    expect(names!.every((n) => n.trim().length > 0)).toBe(true);
    expect(new Set(names!).size, `names were: ${names!.join(', ')}`).toBe(
      names!.length,
    );

    // And the boxes that do render carry those names, so the generator is not being checked
    // in isolation from its only caller.
    const rendered = await page.evaluate(() =>
      Array.from(document.querySelectorAll('input[id$="-search"]'))
        .filter((el) => (el as HTMLElement).offsetParent !== null)
        .map((el) => el.getAttribute('aria-label') || ''),
    );
    expect(rendered.length).toBeGreaterThan(0);
    for (const name of rendered) {
      expect(names!).toContain(name);
    }
  });
});
