import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * The table footer must be inset like the toolbar above it (#1651).
 *
 * The table card is `card p-0` so the table can reach the edges, and the toolbar re-adds its own
 * padding with `p-md`. The footer never did, so the pagination controls sat hard against the card
 * edge while the controls directly above them were inset -- same card, two different insets.
 *
 * Measured against the TOOLBAR rather than against a constant: "non-zero padding" would pass with
 * a value that visibly disagrees with the row above, which is the thing being reported.
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

test.describe('V2 table footer padding', () => {
  test('the pagination row lines up with the toolbar above it', async ({
    page,
  }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/users');

    const card = page.locator('.card.p-0').first();
    const toolbar = card.locator('.p-md.border-b').first();
    const pagination = card.locator('.pagination-row').first();

    // Positive anchors: all three must exist, or comparing geometry is meaningless. The
    // pagination row only renders when there are rows to page, so the table must have loaded.
    await expect(card).toBeVisible();
    await expect(toolbar).toBeVisible();
    await expect(pagination).toBeVisible();

    const [cardBox, toolbarInner, pagInner] = await Promise.all([
      card.boundingBox(),
      // The toolbar's own padding is what defines the inset, so measure its first child.
      toolbar.locator(':scope > *').first().boundingBox(),
      pagination.locator(':scope > *').first().boundingBox(),
    ]);

    expect(cardBox).not.toBeNull();
    expect(toolbarInner).not.toBeNull();
    expect(pagInner).not.toBeNull();

    const toolbarInset = toolbarInner!.x - cardBox!.x;
    const pagInset = pagInner!.x - cardBox!.x;

    // Non-zero first: this is the reported defect.
    expect(pagInset).toBeGreaterThan(0);
    // ...and equal to the toolbar's, within a pixel of sub-pixel rounding.
    expect(Math.abs(pagInset - toolbarInset)).toBeLessThanOrEqual(1);
  });

  test('the footer is inset on the right as well as the left', async ({
    page,
  }) => {
    await loginV2(page);
    await page.goto('/portalv2/admin/users');

    const card = page.locator('.card.p-0').first();
    const pagination = card.locator('.pagination-row').first();
    await expect(pagination).toBeVisible();

    const toolbar = card.locator('.p-md.border-b').first();
    const [cardBox, pagBox, toolbarInner] = await Promise.all([
      card.boundingBox(),
      // The last child sits at the right-hand end of a space-between row.
      pagination.locator(':scope > *').last().boundingBox(),
      toolbar.locator(':scope > *').first().boundingBox(),
    ]);

    const rightGap = cardBox!.x + cardBox!.width - (pagBox!.x + pagBox!.width);
    const toolbarRightGap =
      cardBox!.x + cardBox!.width - (toolbarInner!.x + toolbarInner!.width);

    // Compared against the toolbar rather than merely asserted non-zero. `> 0` passes with no
    // padding at all, because a space-between row narrower than the card leaves a gap anyway --
    // verified by mutation, where that weaker check survived removing the padding entirely.
    // Padding-left alone would also satisfy the first test while leaving the page buttons flush
    // against the edge, which is why this is a separate case.
    expect(Math.abs(rightGap - toolbarRightGap)).toBeLessThanOrEqual(1);
  });
});
