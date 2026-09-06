import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Portal V1's inline styles referenced four custom properties no theme defines (#1774):
 * --text (11), --text-color (5), --accent (3) and --border-color (1). `var()` that resolves to
 * nothing makes the declaration invalid at computed-value time, and the browser then uses the
 * property's *unset* value -- NOT the next rule in the cascade. So the failure differs by
 * property, and only a browser can tell you which one you have:
 *
 *   color            inherited, so unset == inherit. Sixteen elements silently took their
 *                    parent's colour. Most parents are body, which is var(--text-main), so
 *                    they looked right by luck -- but two sit inside a <td> that sets
 *                    color: var(--text-muted), and rendered muted when the markup asks for
 *                    emphasis.
 *   border-*         not inherited, so unset == initial == `none` / `medium` / currentColor.
 *                    Two elements had NO border at all where one was written.
 *   background-color not inherited, so unset == `transparent`. The admin targeted-message
 *                    toast had no fill.
 *
 * scripts/check-theme-tokens.mjs now reads markup and scripts as well as stylesheets, so this
 * class of defect fails the build. These tests cover what a static scan still cannot: the value
 * the reader actually gets, in each of the four themes.
 *
 * dashboard.html and dashboard.js are //go:embed-ed, so nothing here means anything until the
 * image is rebuilt (`docker compose up -d --build`). A run against a stale container measures
 * the old markup and reports it as a pass.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

const THEMES = ['dark', 'light', 'liferay', 'high-contrast'] as const;

async function loginV1(page: any) {
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

const styleOf = (locator: any, prop: string) =>
  locator.evaluate(
    (el: Element, p: string) => getComputedStyle(el).getPropertyValue(p),
    prop,
  );

/**
 * Both spellings of one declaration, measured side by side in the same place.
 *
 * The "before" value cannot be read off the current build -- it is fixed -- and rebuilding the
 * image twice to compare would measure two different containers. Instead both declarations are
 * evaluated in the SAME document, in the SAME parent, against the SAME served stylesheets, and
 * only the property name differs. That isolates the variable the change actually moves.
 */
async function compare(
  page: any,
  parentSelector: string,
  tag: string,
  before: string,
  after: string,
  prop: string,
): Promise<{ before: string; after: string; parent: string }> {
  return page.evaluate(
    ({ parentSelector, tag, before, after, prop }: any) => {
      const parent = document.querySelector(parentSelector);
      if (!parent) throw new Error(`no such parent: ${parentSelector}`);
      const read = (decl: string) => {
        const el = document.createElement(tag);
        el.setAttribute('style', decl);
        el.textContent = 'x';
        parent.appendChild(el);
        const v = getComputedStyle(el).getPropertyValue(prop);
        el.remove();
        return v;
      };
      return {
        before: read(before),
        after: read(after),
        parent: getComputedStyle(parent).getPropertyValue(prop),
      };
    },
    { parentSelector, tag, before, after, prop },
  );
}

const tokenValue = (page: any, name: string) =>
  page.evaluate(
    (n: string) =>
      getComputedStyle(document.documentElement).getPropertyValue(n).trim(),
    name,
  );

/** Applies a theme by attribute. The stylesheets are all linked, so the tokens re-resolve. */
const setTheme = (page: any, theme: string) =>
  page.evaluate(
    (t: string) => document.documentElement.setAttribute('data-theme', t),
    theme,
  );

test.describe('Portal V1 inline theme-token references (#1774)', () => {
  /**
   * The headline of the issue: sixteen `color` references that were "inheriting the value they
   * were reaching for anyway". This asserts that claim rather than trusting it -- and it is
   * false in one place. The tunnels table in the user-details modal writes
   *
   *     <td style="... color: var(--text-muted);">
   *         <div>In: <strong style="color: var(--text)">…</strong></div>
   *
   * so the emphasised figure inherited --text-muted and rendered identically to the label
   * beside it. That element only exists while a tunnel is connected, so the <td>/<strong> pair
   * is rebuilt here from the markup dashboard.js emits.
   */
  for (const theme of THEMES) {
    test(`emphasis inside a muted cell is no longer muted (${theme})`, async ({
      page,
    }) => {
      await loginV1(page);
      await setTheme(page, theme);

      const muted = await tokenValue(page, '--text-muted');
      const main = await tokenValue(page, '--text-main');
      expect(muted, 'the theme did not reach the page').toBeTruthy();
      expect(main).toBeTruthy();
      expect(main).not.toBe(muted);

      const cellId = 'probe-1774-muted-cell';
      await page.evaluate((id: string) => {
        const td = document.createElement('div');
        td.id = id;
        td.setAttribute('style', 'color: var(--text-muted);');
        document.body.appendChild(td);
      }, cellId);

      const r = await compare(
        page,
        `#${cellId}`,
        'strong',
        'color: var(--text);', // the pre-#1774 spelling
        'color: var(--text-main);', // what dashboard.js now emits
        'color',
      );

      // Before: invalid -> unset -> inherit -> the cell's muted colour. The emphasis was lost.
      expect(r.before).toBe(r.parent);
      // After: the emphasis colour the markup asks for, distinct from the label beside it.
      expect(r.after).not.toBe(r.parent);
      expect(r.after).toBe(
        await page.evaluate(() => {
          const p = document.createElement('span');
          p.style.color = 'var(--text-main)';
          document.body.appendChild(p);
          const v = getComputedStyle(p).color;
          p.remove();
          return v;
        }),
      );
    });
  }

  /**
   * The other fifteen `color` sites, whose parents are var(--text-main) either way. Asserting
   * they did NOT move is the measurement the issue asks for: it is what makes "no pixel
   * changed" a finding rather than an assumption. Checked in every theme, because "the same"
   * under dark says nothing about liferay.
   */
  for (const theme of THEMES) {
    test(`a colour whose parent is --text-main is unchanged (${theme})`, async ({
      page,
    }) => {
      await loginV1(page);
      await setTheme(page, theme);

      // #dashboard-screen inherits body's color: var(--text-main), which is the ancestry every
      // one of the other fifteen sites resolves through.
      const r = await compare(
        page,
        '#dashboard-screen',
        'span',
        'color: var(--text);',
        'color: var(--text-main);',
        'color',
      );
      expect(r.before).toBe(r.after);
      expect(r.after).toBe(r.parent);

      // --text-color took exactly the same route.
      const r2 = await compare(
        page,
        '#dashboard-screen',
        'span',
        'color: var(--text-color);',
        'color: var(--text-main);',
        'color',
      );
      expect(r2.before).toBe(r2.after);
    });
  }

  /**
   * A real element on a real page, and the mutation target: revert `var(--primary)` to
   * `var(--accent)` in dashboard.html and border-left-style computes to `none` again, because
   * an invalid var() in a shorthand unsets every longhand it writes -- it does not fall back to
   * .glass's own `border: 1px solid var(--border)`.
   */
  test('the backups restore callout has its accent bar', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-backups');

    const callout = page.locator('#tab-backups > div.glass').first();
    await expect(callout).toBeVisible();
    // Anchored on presence: an absence-only assertion passes on an empty tab.
    await expect(callout).toContainText(/Restore via CLI only/i);

    expect(await styleOf(callout, 'border-left-style')).toBe('solid');
    expect(await styleOf(callout, 'border-left-width')).toBe('3px');

    // And it is the accent, not the 1px hairline .glass gives the other three sides.
    const primary = await tokenValue(page, '--primary');
    expect(primary).toBeTruthy();
    expect(await styleOf(callout, 'border-left-color')).not.toBe(
      await styleOf(callout, 'border-right-color'),
    );
  });

  /**
   * The second border, and the second mutation target. `--border-color` is the legal pages'
   * spelling of `--border`; nothing dashboard.html loads defines it, so the divider above Save
   * Settings was not drawn.
   */
  test('the system settings actions row has its divider', async ({ page }) => {
    await loginV1(page);
    await page.click('#nav-system');

    const save = page.locator(
      '#tab-system button[onclick="saveSystemSettings()"]',
    );
    await expect(save).toBeVisible();
    const row = save.locator('xpath=..');

    expect(await styleOf(row, 'border-top-style')).toBe('solid');
    expect(await styleOf(row, 'border-top-width')).toBe('1px');
    expect(await styleOf(row, 'border-top-color')).not.toBe('rgba(0, 0, 0, 0)');
  });

  /**
   * The admin targeted-message toast. `background-color` is not inherited, so the unresolved
   * var() left it `transparent` -- the message rendered as loose text over whatever it covered.
   * --primary is the obvious rename and the wrong one (check-theme-contrast.cjs documents it as
   * a foreground), so this also measures that the fill it did get can carry .toast's own text
   * colour at AA.
   */
  for (const theme of THEMES) {
    test(`the targeted-message toast has a legible fill (${theme})`, async ({
      page,
    }) => {
      await loginV1(page);
      await setTheme(page, theme);

      const measured = await page.evaluate(() => {
        const mk = (decl: string) => {
          const el = document.createElement('div');
          el.className = 'toast show';
          el.setAttribute('style', decl);
          el.textContent = 'Admin Message';
          document.body.appendChild(el);
          const cs = getComputedStyle(el);
          const v = {
            background: cs.backgroundColor,
            color: cs.color,
            border: cs.borderTopColor,
          };
          el.remove();
          return v;
        };
        return {
          before: mk('background-color: var(--accent); z-index: 999999;'),
          after: mk(
            'background-color: var(--status-info-bg); border-color: var(--status-info-border); z-index: 999999;',
          ),
          page: getComputedStyle(document.body).backgroundColor,
        };
      });

      // Before: no fill at all.
      expect(measured.before.background).toBe('rgba(0, 0, 0, 0)');
      // After: an actual surface.
      expect(measured.after.background).not.toBe('rgba(0, 0, 0, 0)');

      // And the text on it clears AA. The fill can be translucent, so it is composited over the
      // page behind it before the ratio is taken -- the same thing the eye does.
      const ratio = await page.evaluate(
        ({ fg, bg, behind }: any) => {
          const parse = (c: string) =>
            (c.match(/[\d.]+/g) || []).map(Number) as number[];
          const over = (top: number[], bottom: number[]) => {
            const a = top.length > 3 ? top[3] : 1;
            return [0, 1, 2].map((i) => top[i] * a + bottom[i] * (1 - a));
          };
          const lum = (rgb: number[]) => {
            const c = rgb.map((v) => {
              const s = v / 255;
              return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
            });
            return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
          };
          const surface = over(parse(bg), parse(behind));
          const text = over(parse(fg), surface);
          const [a, b] = [lum(text), lum(surface)].sort((x, y) => y - x);
          return (a + 0.05) / (b + 0.05);
        },
        {
          fg: measured.after.color,
          bg: measured.after.background,
          behind: measured.page,
        },
      );
      expect(
        ratio,
        `toast text ${measured.after.color} on ${measured.after.background} in ${theme}`,
      ).toBeGreaterThanOrEqual(4.5);
    });
  }

  /**
   * The served artefact, not the source tree. dashboard.html and dashboard.js are embedded in
   * the binary, so this is the assertion that catches a rebuild that never happened -- and it
   * fails on any reintroduction of the four names anywhere in either file, including in shapes
   * the tests above do not reach.
   */
  test('no undefined token name survives in the served assets', async ({
    page,
  }) => {
    for (const path of ['/admin', '/static/dashboard.js']) {
      const body = await (await page.request.get(path)).text();
      expect(body.length, `${path} served nothing`).toBeGreaterThan(1000);
      for (const name of [
        '--text',
        '--text-color',
        '--accent',
        '--border-color',
      ])
        expect(
          new RegExp(`var\\(\\s*${name}\\s*[,)]`).test(body),
          `${path} still references var(${name})`,
        ).toBe(false);
    }
  });
});
