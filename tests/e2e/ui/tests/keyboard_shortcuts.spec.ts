import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Keyboard shortcuts and the overlay documenting them (#1611).
 *
 * #1562 added sidebar arrow navigation and said so nowhere, which is how the owner found it by
 * accident. These cover the two things that make shortcuts usable rather than merely present:
 * they are discoverable, and they do not fire when you are typing.
 */
const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
  await expect(
    page.getByRole('heading', { name: /Dashboard Overview/i }),
  ).toBeVisible();
}

test.describe('V2 keyboard shortcuts', () => {
  test('? opens the overlay and Escape closes it', async ({ page }) => {
    await loginV2(page);

    await page.keyboard.press('?');
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(/Keyboard shortcuts/i);

    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('the overlay is reachable without knowing a shortcut', async ({
    page,
  }) => {
    // The point of the visible trigger: an overlay you can only open with the shortcut it
    // documents is no use to anyone who does not already know the shortcut.
    await loginV2(page);

    await page.getByRole('button', { name: /keyboard shortcuts/i }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
  });

  test('g then a letter navigates', async ({ page }) => {
    await loginV2(page);

    await page.keyboard.press('g');
    await page.keyboard.press('u');
    await page.waitForURL('**/portalv2/admin/users');
    expect(page.url()).toContain('/admin/users');
  });

  test('shortcuts are inert while typing', async ({ page }) => {
    // The guard that makes plain-key chords safe. Without it, typing "log" in any search box
    // would navigate away mid-word.
    await loginV2(page);
    await page.goto('/portalv2/admin/users');
    await expect(
      page.getByRole('heading', { name: /User Management/i }),
    ).toBeVisible();

    const search = page.getByPlaceholder(/search/i).first();
    await search.click();
    // 'g d' deliberately, not 'g u': we are already on the users page, so an unguarded 'g u'
    // would navigate to where we already are and the assertion could not tell the difference.
    // Mutation testing caught exactly that -- removing the guard left this test passing.
    await search.pressSequentially('gd');

    // Still here, and the text went into the field rather than being swallowed as a chord.
    expect(
      page.url(),
      'typing gd must not navigate to the dashboard',
    ).toContain('/admin/users');
    await expect(search).toHaveValue('gd');

    // And '?' typed into a field must not open the overlay either.
    await search.fill('');
    await search.pressSequentially('?');
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('modifier combinations are left to the browser', async ({ page }) => {
    await loginV2(page);

    // Ctrl+G is the browser's find-again. Ours must not swallow it and must not start a chord.
    await page.keyboard.press('Control+g');
    await page.keyboard.press('u');
    expect(
      page.url(),
      'Ctrl+G should not have started a go-to chord',
    ).toContain('/portalv2/dashboard');
  });

  test('the overlay documents the sidebar keys from #1562 too', async ({
    page,
  }) => {
    // It should answer "what can I do with the keyboard", not "what did this component bind".
    await loginV2(page);
    await page.keyboard.press('?');

    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText(/sidebar/i);
    await expect(dialog).toContainText(/Home/);
  });
});
