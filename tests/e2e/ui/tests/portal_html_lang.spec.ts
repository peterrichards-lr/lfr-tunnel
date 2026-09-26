import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * `<html lang>` follows the chosen locale, in BOTH portal arms (#2262).
 *
 * Both arms set `document.documentElement.dir` on every language change and neither set `lang`,
 * so `<html lang="en">` — hardcoded in `pkg/server/dashboard.html:2` and `ui/index.html:2` —
 * survived every switch. VoiceOver, NVDA and JAWS all pick the speech synthesiser from `lang`,
 * so nine locales' worth of translated prose was read aloud with an English voice.
 *
 * Three things about the shape of this file are deliberate:
 *
 * 1. **Every locale, not just one.** Asserting only `ar` would pass on a `dir`-only
 *    implementation, since `ar` is the one locale whose `dir` already changed — which is
 *    exactly the bug (#2262's own implementation plan, point 4). The loop runs the whole
 *    supported set, so a fix that special-cases anything goes red.
 *
 * 2. **Both arms.** Both were wrong, and they share no code, so one arm passing says nothing
 *    about the other.
 *
 * 3. **Each switch asserts the translation pass ran BEFORE it asserts the attribute.** A
 *    `changePortalLanguage` that silently failed, or a `<select>` that never fired, would leave
 *    `lang` at `en` and turn a broken harness into a confident "the fix is missing". The
 *    sidebar/label text is the anchor: if it did not change, the language never changed, and
 *    the failure names that instead.
 */

const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

// The codes in `supportedLocales` (pkg/server/static/dashboard.js) and `DEFAULT_LANGUAGES`
// (ui/src/contexts/I18nContext.tsx). Both lists carry the same ten; a divergence between them
// is check-portal-parity's job, not this file's.
const NON_ENGLISH = ['ar', 'de', 'es', 'fr', 'ja', 'ko', 'pt', 'ro', 'zh'];

declare function changePortalLanguage(
  lang: string,
  persist?: boolean,
): Promise<void>;

test.describe('Portal V1 declares its locale on <html lang>', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    // The context is shared, and `lfr_lang` outranks everything else in init()'s precedence —
    // so a preference left behind by an earlier spec would decide this one's baseline. Pinned
    // rather than removed, because removing it falls back to Accept-Language negotiation.
    await page.evaluate(() => localStorage.setItem('lfr_lang', 'en'));
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic Link Sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
  });

  test.afterEach(async ({ page }) => {
    // Specs share a browser context, so a stray preference would change what every spec
    // sorting after this one sees (§4 of the e2e skill).
    await page.evaluate(() => localStorage.removeItem('lfr_lang'));
  });

  test('every supported locale reaches the root element', async ({ page }) => {
    // The steady state before any switch, and the value the whole bug consisted of leaving
    // behind. Asserting it here means the loop below starts from a known place.
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    const english = await page.locator('#nav-account').innerText();

    for (const locale of NON_ENGLISH) {
      await page.evaluate((l) => changePortalLanguage(l), locale);

      // Anchor: the pass really ran. Without this a failure below is ambiguous between "lang
      // was not set" and "the language never changed at all".
      await expect(
        page.locator('#nav-account'),
        `switching to ${locale} did not retranslate the sidebar`,
      ).not.toHaveText(english);

      await expect(
        page.locator('html'),
        `<html lang> after switching to ${locale}`,
      ).toHaveAttribute('lang', locale);
    }
  });

  test('lang and dir are set together, not one without the other', async ({
    page,
  }) => {
    // `dir` was handled and `lang` was not. Pinning the pair is what stops the next change
    // from reintroducing half of it.
    await page.evaluate(() => changePortalLanguage('ar'));
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ar');

    await page.evaluate(() => changePortalLanguage('ja'));
    await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ja');
  });

  test('the account dropdown, driven as a person would, sets it too', async ({
    page,
  }) => {
    // changePortalLanguage() above is the function the controls invoke; this drives the real
    // control once so the two cannot diverge. (The login screen's #portal-language-selector is
    // hidden once signed in — the account tab is where a signed-in V1 user changes language,
    // per #1541.)
    await page.click('#nav-account');
    await page.click('#acc-custom-dropdown-trigger');
    await page
      .locator('#acc-custom-menu')
      .getByText(/Espa|Spanish/i)
      .first()
      .click();

    await expect(page.locator('#nav-account')).toHaveText(
      /Configuraci|cuenta/i,
    );
    await expect(page.locator('html')).toHaveAttribute('lang', 'es');
  });

  test('a restored preference is declared on load, not only on a change', async ({
    page,
  }) => {
    // Setting it on *change* alone leaves the first render after a reload declaring English
    // for someone whose stored preference is not — the half-fix that is hardest to notice,
    // because every manual click-through looks right.
    await page.evaluate(() => localStorage.setItem('lfr_lang', 'ja'));
    await page.reload();

    await expect(page.locator('#nav-account')).toHaveText('アカウント設定');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ja');
  });

  test('an unsupported locale declares English rather than a language it is not rendering', async ({
    page,
  }) => {
    // `?lang=` is arbitrary input and V2 seeds the shared preference from navigator.language,
    // so a code with no bundle is reachable. The server answers those with the English bundle
    // (handleGetI18n), and echoing the code would claim Italian over English prose — worse
    // than the lang="en" this fix replaces.
    await page.evaluate(() => changePortalLanguage('it'));
    await expect(page.locator('#nav-account')).toHaveText('Account Settings');
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  });
});

test.describe('Portal V2 declares its locale on <html lang>', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    // Same reason as V1: the shared context carries `lfr_lang` between specs.
    await page.evaluate(() => localStorage.setItem('lfr_lang', 'en'));
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/account');
    await expect(page.locator('#language')).toBeVisible();
  });

  test.afterEach(async ({ page }) => {
    await page.evaluate(() => localStorage.removeItem('lfr_lang'));
  });

  test('every supported locale reaches the root element', async ({ page }) => {
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    const label = page.locator('label[for="language"]');
    const english = await label.innerText();

    for (const locale of NON_ENGLISH) {
      // The real control, not setLanguage() through the context: this select is the only way a
      // V2 user changes language, and it is what has to work.
      await page.selectOption('#language', locale);

      await expect(
        label,
        `switching to ${locale} did not retranslate the account form`,
      ).not.toHaveText(english);

      await expect(
        page.locator('html'),
        `<html lang> after switching to ${locale}`,
      ).toHaveAttribute('lang', locale);
    }
  });

  test('lang and dir are set together, not one without the other', async ({
    page,
  }) => {
    await page.selectOption('#language', 'ar');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ar');

    await page.selectOption('#language', 'ja');
    await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ja');
  });

  test('a restored preference is declared on load, not only on a change', async ({
    page,
  }) => {
    // The I18nProvider effect is keyed on `language`, whose initial value comes from
    // localStorage — so this is the same line, exercised on the mount path.
    await page.evaluate(() => localStorage.setItem('lfr_lang', 'de'));
    await page.reload();

    await expect(page.locator('label[for="language"]')).toHaveText(
      'Spracheinstellung',
    );
    await expect(page.locator('html')).toHaveAttribute('lang', 'de');
  });
});
