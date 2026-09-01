import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The Block IP button must line up with the inputs beside it (#1627).
 *
 * .input-field carries margin-bottom: 16px, and the row aligned on items-end -- so each input's
 * box ended 16px below its visible edge and the button sat out of line with both.
 *
 * Measured rather than eyeballed: a screenshot test would not say WHY it moved, and the
 * geometry is the actual requirement.
 */
const adminEmail = 'admin@lfr-demo.local';

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

test.describe('V2 Block IP form alignment', () => {
  test('the button and the inputs share a bottom edge', async ({ page }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/blacklist');

    const ip = page.locator('#ip-address');
    const reason = page.locator('#reason');
    const button = page.getByRole('button', { name: /Block IP/i });

    // Positive anchors: all three must have rendered, or comparing boxes is meaningless.
    await expect(ip).toBeVisible();
    await expect(reason).toBeVisible();
    await expect(button).toBeVisible();

    const [ipBox, reasonBox, btnBox] = await Promise.all([
      ip.boundingBox(),
      reason.boundingBox(),
      button.boundingBox(),
    ]);
    expect(ipBox).not.toBeNull();
    expect(reasonBox).not.toBeNull();
    expect(btnBox).not.toBeNull();

    const bottom = (b: any) => b.y + b.height;
    // 1px of tolerance for sub-pixel rounding, not for a 16px margin.
    expect(Math.abs(bottom(btnBox!) - bottom(ipBox!))).toBeLessThanOrEqual(1);
    expect(Math.abs(bottom(btnBox!) - bottom(reasonBox!))).toBeLessThanOrEqual(
      1,
    );
  });

  test('the controls are the same height as each other', async ({ page }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/blacklist');

    const ip = page.locator('#ip-address');
    const button = page.getByRole('button', { name: /Block IP/i });
    await expect(ip).toBeVisible();
    await expect(button).toBeVisible();

    const ipBox = await ip.boundingBox();
    const btnBox = await button.boundingBox();
    // A button noticeably taller or shorter than the field reads as misaligned even when the
    // bottoms match, which is what the report was about.
    expect(Math.abs(btnBox!.height - ipBox!.height)).toBeLessThanOrEqual(2);
  });
});
