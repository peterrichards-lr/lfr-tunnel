import { test, expect } from "./utils/fixtures";
import { getMagicLinkToken, clearMailpit } from "./utils/mailpit";

/**
 * Portal V2 wide tables must be reachable on a narrow viewport (#1206).
 *
 * The reported symptom was that Role, Status, Auth Method, Quotas, Last Seen and the whole
 * Actions column run off-screen with "no horizontal scroll". Measuring it showed the
 * overflow itself was fine -- on a 400px viewport the Users table is 722px wide inside a
 * 318px wrapper and .table-responsive already carries overflow-x: auto.
 *
 * What was missing was any cue that scrolling was possible. With overlay scrollbars there
 * is nothing to see, so the columns read as clipped and unreachable. That is the same
 * failure as .sidebar-menu in #1204/#1213, where an invisible thumb left whole admin
 * sections undiscovered.
 *
 * These assert both halves: that the content is genuinely reachable by scrolling, and that
 * the affordance saying so is present. Testing only the first would pass on the build that
 * was reported as broken.
 */
test.describe("Portal V2 wide tables scroll on narrow viewports", () => {
  const adminEmail = "admin@lfr-demo.local"; // From tests/e2e/server-config.yaml

  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto("/portalv2/");
    await page.fill("#email-input", adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator("text=Magic link sent")).toBeVisible();

    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL("**/portalv2/dashboard");

    await page.goto("/portalv2/admin/users");
    await expect(page.locator("table").first()).toBeVisible();
  });

  test("the Actions column can be reached by scrolling on a phone viewport", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 400, height: 800 });
    const wrapper = page.locator(".table-responsive").first();

    const before = await wrapper.evaluate((el) => ({
      clientWidth: el.clientWidth,
      scrollWidth: el.scrollWidth,
      actionsWithinViewport:
        (
          el.querySelector("thead th:last-child") as HTMLElement
        ).getBoundingClientRect().right <= window.innerWidth,
    }));

    // Precondition: the table really is wider than the screen here. If a future change
    // made it fit, this test would silently stop exercising anything.
    expect(before.scrollWidth).toBeGreaterThan(before.clientWidth);
    expect(before.actionsWithinViewport).toBe(false);

    await wrapper.evaluate((el) => {
      el.scrollLeft = el.scrollWidth;
    });

    const after = await wrapper.evaluate((el) => ({
      scrollLeft: el.scrollLeft,
      actionsWithinViewport:
        (
          el.querySelector("thead th:last-child") as HTMLElement
        ).getBoundingClientRect().right <= window.innerWidth,
    }));

    expect(after.scrollLeft).toBeGreaterThan(0);
    expect(after.actionsWithinViewport).toBe(true);
  });

  test("the table advertises that it scrolls", async ({ page }) => {
    await page.setViewportSize({ width: 400, height: 800 });

    const style = await page
      .locator(".table-responsive")
      .first()
      .evaluate((el) => {
        const cs = getComputedStyle(el);
        return { overflowX: cs.overflowX, scrollbarWidth: cs.scrollbarWidth };
      });

    expect(style.overflowX).toBe("auto");
    // Without this the bar is an overlay that fades out, which is exactly why the columns
    // were reported as unreachable rather than merely off-screen.
    expect(style.scrollbarWidth).toBe("thin");
  });

  test("a full-width viewport needs no scrolling", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const wrapper = page.locator(".table-responsive").first();

    const state = await wrapper.evaluate((el) => ({
      overflows: el.scrollWidth > el.clientWidth,
      actionsWithinViewport:
        (
          el.querySelector("thead th:last-child") as HTMLElement
        ).getBoundingClientRect().right <= window.innerWidth,
    }));

    expect(state.overflows).toBe(false);
    expect(state.actionsWithinViewport).toBe(true);
  });
});
