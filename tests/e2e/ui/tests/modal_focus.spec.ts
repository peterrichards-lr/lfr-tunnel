import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

const adminEmail = process.env.E2E_ADMIN_EMAIL || 'admin@lfr-demo.local';

async function loginV2(page: any) {
  await clearMailpit();
  await page.goto('/portalv2/');
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`/portalv2/login?token=${token}`);
  await page.waitForURL('**/portalv2/dashboard');
}

// Every text input in every V2 modal lost focus on each keystroke (#2102).
//
// ModalShell's focus effect listed onClose among its dependencies, and every caller passes an
// inline arrow -- a new identity on each render. One keystroke therefore re-ran the effect,
// which called cardRef.focus() and pulled focus out of the field being typed into.
//
// This suite already opened modals and clicked buttons. Nothing TYPED more than one character
// into one and checked the text arrived, so a defect that only appears from the second
// keystroke onward was invisible to it.
test.describe('Modal text entry', () => {
  test('a multi-character value survives being typed into a modal field', async ({
    page,
  }) => {
    await loginV2(page);

    // Any modal carrying a text input proves it: the defect was in ModalShell, not in one
    // caller. The reservations Access Control dialog is the one the owner hit it on.
    const padlock = page
      .getByRole('button', { name: /access control/i })
      .first();
    if ((await padlock.count()) === 0) {
      test.skip(
        true,
        'no reservation rows in this run, so no modal to type into',
      );
    }
    await padlock.click();

    const field = page.locator('.modal-card input').first();
    await expect(field).toBeVisible();

    // pressSequentially, not fill(). fill() sets the value in one shot and passes even with
    // the focus bug present -- which is precisely how this escaped every existing test.
    const typed = 'correct-horse-battery';
    await field.click();
    await field.pressSequentially(typed, { delay: 20 });

    await expect(field).toHaveValue(typed);
    await expect(field).toBeFocused();
  });
});
