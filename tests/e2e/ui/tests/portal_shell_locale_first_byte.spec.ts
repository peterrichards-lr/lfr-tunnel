import { test, expect, APIRequestContext } from '@playwright/test';

/**
 * The portal shells declare their locale in the FIRST BYTES, before any bundle runs (#2271).
 *
 * `portal_html_lang.spec.ts` covers the steady state #2262 fixed: both arms assign
 * `document.documentElement.lang` wherever they assign `dir`. What that cannot reach is the window
 * before the JavaScript executes — `<html lang="en">` was literal in `pkg/server/dashboard.html`
 * and `ui/index.html`, so a Japanese user's every reload declared English until
 * `applyTranslations()` or the `I18nProvider` effect ran, and an RTL layout painted LTR first.
 *
 * Three things about the shape of this file are deliberate:
 *
 * 1. **`request`, not `page`.** Playwright's APIRequestContext does not execute scripts, so what
 *    it receives is exactly what the browser parses before the bundle — which is the entire
 *    subject. Driven through `page` this would be a race against the fix in `portal_html_lang`,
 *    and would pass against the unfixed server as soon as the bundle won it.
 *
 * 2. **The V2 half cannot be a Go test.** Portal V2's shell reaches the binary through
 *    `//go:embed ui-dist/*`, which is generated and never committed (#1196) — CI's Go test jobs
 *    stub it as an empty `index.html`. `TestPortalShellsDeclareTheResolvedLocale`
 *    (`pkg/server/privacy_disclosures_test.go`) therefore asserts the V1 route and the V2 shell's
 *    rewrite, and this is where V2's *route* is asserted against a real build.
 *
 * 3. **The root element is parsed out, not string-matched.** `body.includes('lang="ar"')` is
 *    satisfied by a `lang` on any element, and — worse — by a `lang="ar"` appended AFTER a
 *    surviving `lang="en"`, which is the specific way this fix could regress: HTML keeps the first
 *    of a duplicated attribute, so such a document is byte-different and behaviourally identical
 *    to the bug.
 *
 * It creates no data and signs nothing in, so it has no §4 fixture obligations.
 */

const V1_ROUTES = ['/admin', '/portal', '/admin/users'];
const V2_ROUTES = ['/portalv2/', '/portalv2/dashboard'];

/** The opening root element tag of a document, whatever attributes it carries. */
function rootElementTag(body: string): string {
  const m = body.match(/<html\b[^>]*>/i);
  expect(m, 'the response carried no root element tag at all').not.toBeNull();
  return m![0];
}

function attrValues(tag: string, name: string): string[] {
  const re = new RegExp(`\\s${name}\\s*=\\s*"([^"]*)"`, 'gi');
  return [...tag.matchAll(re)].map((m) => m[1]);
}

/**
 * Asserts the shell at `path` declares `lang`/`dir`, having first asserted it IS the shell.
 *
 * The anchor matters: every assertion here is about one attribute of one tag, and an error page, a
 * redirect body or a 500 would satisfy "does not declare Arabic" just as well as a broken fix does.
 */
async function expectShellDeclares(
  request: APIRequestContext,
  path: string,
  opts: { lang: string; dir: string; headers?: Record<string, string> },
) {
  const res = await request.get(path, { headers: opts.headers });
  expect(res.status(), `GET ${path}`).toBe(200);
  const body = await res.text();

  // Anchor. Written by the server for this document and by nothing else in either arm.
  const marker = path.startsWith('/portalv2')
    ? '<div id="root">'
    : '/static/dashboard.js?v=';
  expect(
    body,
    `GET ${path} did not return the portal shell, so the attribute assertions below would be meaningless`,
  ).toContain(marker);

  const tag = rootElementTag(body);
  expect(attrValues(tag, 'lang'), `lang attributes on ${tag}`).toEqual([
    opts.lang,
  ]);
  expect(attrValues(tag, 'dir'), `dir attributes on ${tag}`).toEqual([
    opts.dir,
  ]);
}

test.describe('the served shell declares its locale before any script runs', () => {
  for (const path of V1_ROUTES) {
    test(`V1 ${path} honours ?lang=`, async ({ request }) => {
      await expectShellDeclares(request, `${path}?lang=ar`, {
        lang: 'ar',
        dir: 'rtl',
      });
    });
  }

  for (const path of V2_ROUTES) {
    test(`V2 ${path} honours ?lang=`, async ({ request }) => {
      // The second route is a deep link, which reaches the shell through the SPA fallback rather
      // than as a file lookup. The fallback is a separate branch, and is the path a user who
      // bookmarked a page actually takes.
      await expectShellDeclares(request, `${path}?lang=ar`, {
        lang: 'ar',
        dir: 'rtl',
      });
    });
  }

  test('V1 honours Accept-Language for a visitor who has chosen nothing', async ({
    request,
  }) => {
    // The only signal the server has on a first visit. `?lang=` alone passing would leave every
    // first-time RTL visitor with an LTR first paint, which is the half of this the client-side
    // fix in #2262 could never cover.
    await expectShellDeclares(request, '/', {
      lang: 'ja',
      dir: 'ltr',
      headers: { 'Accept-Language': 'ja-JP,ja;q=0.9,en;q=0.8' },
    });
  });

  test('V2 honours Accept-Language for a visitor who has chosen nothing', async ({
    request,
  }) => {
    await expectShellDeclares(request, '/portalv2/', {
      lang: 'ar',
      dir: 'rtl',
      headers: { 'Accept-Language': 'ar-EG,ar;q=0.9,en;q=0.8' },
    });
  });

  test('an unsupported locale declares English rather than a language it is not rendering', async ({
    request,
  }) => {
    // BOUNDING, not FIRING: `lang="en"` is what the unfixed server sent too, so this passes
    // against it and is no evidence the fix works. It pins the deliberate edge — echo the
    // requested code and this goes red. Reachable because `?lang=` is arbitrary input and Portal
    // V2 seeds the shared `lfr_lang` preference from navigator.language, so `it` arrives from a
    // browser nobody configured; the server answers an unsupported locale with the English
    // bundle, and declaring Italian over English prose would be worse than declaring nothing.
    await expectShellDeclares(request, '/admin?lang=it', {
      lang: 'en',
      dir: 'ltr',
    });
    await expectShellDeclares(request, '/portalv2/?lang=it', {
      lang: 'en',
      dir: 'ltr',
    });
  });

  test('the shell announces that it varies by Accept-Language', async ({
    request,
  }) => {
    // The document now depends on a request header, so a cache keyed on the URL alone may hand
    // the first visitor's locale to the next. `?lang=` is already part of that key;
    // Accept-Language is not, which is what `Vary` is for.
    for (const path of ['/admin', '/portalv2/']) {
      const res = await request.get(path);
      expect(res.headers()['vary'], `Vary on ${path}`).toContain(
        'Accept-Language',
      );
    }

    // V2's shell used to be served by http.FileServer, whose Last-Modified/ETag describe the
    // FILE — validators that would now be lying about a body the server rewrites per request. A
    // revert to ServeContent brings them back, and this is what notices.
    const v2 = await request.get('/portalv2/');
    expect(v2.headers()['etag'], 'ETag on /portalv2/').toBeUndefined();
  });
});
