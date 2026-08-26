import { test, expect } from "./utils/fixtures";
import { getMagicLinkToken, clearMailpit } from "./utils/mailpit";

/**
 * The "Switch back to V1" banner must be dismissible (#1203).
 *
 * The dismiss button was in the DOM and in the accessibility tree but not visibly
 * rendered anywhere, so the banner occupied a strip of every page forever.
 *
 * Cause: this project declares tailwindcss but never runs it (#1383), so the button's
 * `right-xl`, `top-1/2` and `-translate-y-1/2` were inert while `absolute` was not --
 * leaving it absolutely positioned with every offset at `auto`, landing at its static
 * position inside a justify-center row, on top of the banner text and with no colour of
 * its own. The wrapper was equally unstyled: `bg-primary`, `text-white`, `py-xs`, `px-xl`
 * and `shadow-sm` are all inert too, so the banner had no background at all.
 *
 * These hit-test rather than check visibility -- `toBeVisible()` passed throughout the bug.
 *
 * They also locate the control by its accessible name and the banner by its text, both
 * unchanged by the fix. Targeting the new CSS class would make a red run fail merely
 * because the class did not exist yet, which proves nothing about the defect.
 */
test.describe("Portal V2 legacy-interface banner can be dismissed", () => {
  const adminEmail = "admin@lfr-demo.local"; // From tests/e2e/server-config.yaml
  const BANNER_TEXT = "Need the legacy interface?";
  // title="Dismiss promo banner" before the fix, aria-label of the same text after, so
  // this resolves against either markup.
  const DISMISS_SELECTOR =
    '[aria-label="Dismiss promo banner"], [title="Dismiss promo banner"]';

  // Located by attribute, not by role+name. Before the fix the button's accessible name
  // was "\u00d7" -- `title` only supplies a name when an element has no text content, and
  // this one contains &times; -- so getByRole({name: 'Dismiss promo banner'}) did not
  // resolve at all on the old markup, and the test would have failed for the wrong
  // reason. The aria-label added by the fix is itself the correction to that.
  const dismiss = (page: import("@playwright/test").Page) =>
    page.locator(DISMISS_SELECTOR);

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto("/portalv2/");
    await page.fill("#email-input", adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator("text=Magic link sent")).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL("**/portalv2/dashboard");
    // Deliberately NOT dismissing the banner: it is the subject.
    await expect(page.getByText(BANNER_TEXT)).toBeVisible();
  });

  test("the dismiss control is actually clickable, not just present", async ({
    page,
  }) => {
    const onTop = await page.evaluate((sel) => {
      const el = document.querySelector(sel) as HTMLElement | null;
      if (!el) return "missing";
      const b = el.getBoundingClientRect();
      if (b.width === 0 || b.height === 0) return "zero-size";
      const top = document.elementFromPoint(
        b.x + b.width / 2,
        b.y + b.height / 2,
      );
      if (!top) return "nothing";
      return el === top || el.contains(top)
        ? "self"
        : `blocked by ${top.tagName}`;
    }, DISMISS_SELECTOR);
    expect(onTop).toBe("self");
  });

  test("it sits at the banner edge, not over the message", async ({ page }) => {
    const geom = await page.evaluate((sel) => {
      const btn = document.querySelector(sel) as HTMLElement;
      const banner = btn.parentElement as HTMLElement;
      const text = banner.querySelector("p") as HTMLElement;
      const b = banner.getBoundingClientRect();
      const d = btn.getBoundingClientRect();
      const t = text.getBoundingClientRect();
      return {
        withinBanner: d.right <= b.right + 1 && d.left >= b.left,
        towardRightEdge: d.left > b.left + b.width / 2,
        overlapsText: d.left < t.right && d.right > t.left,
      };
    }, DISMISS_SELECTOR);

    expect(geom.withinBanner).toBe(true);
    expect(geom.towardRightEdge).toBe(true);
    // Landing on top of the message is exactly what the inert offsets caused.
    expect(geom.overlapsText).toBe(false);
  });

  test("the banner is styled, not bare text", async ({ page }) => {
    // bg-primary was inert, so the banner had no background at all.
    const bg = await page.evaluate((sel) => {
      const btn = document.querySelector(sel) as HTMLElement;
      return getComputedStyle(btn.parentElement as HTMLElement).backgroundColor;
    }, DISMISS_SELECTOR);

    expect(bg).not.toBe("rgba(0, 0, 0, 0)");
    expect(bg).not.toBe("transparent");
  });

  test("clicking it dismisses the banner, and it stays dismissed", async ({
    page,
  }) => {
    await dismiss(page).click();
    await expect(page.getByText(BANNER_TEXT)).toHaveCount(0);

    await page.reload();
    await page.waitForURL("**/portalv2/dashboard");
    await expect(page.getByText(BANNER_TEXT)).toHaveCount(0);
  });
});
