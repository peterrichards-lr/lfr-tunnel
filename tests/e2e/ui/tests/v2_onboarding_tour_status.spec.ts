import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { V2 } from './utils/portal';

/**
 * V2's onboarding tour reports what actually happened to /api/me/onboarding (#2535), as V1 does:
 * `in_progress` from `welcome` when it starts (a rerun flagged when started from Help &
 * Resources), then `completed` only if the user reached the last step, otherwise `aborted`, with
 * the 0-based step reached as `step_<n>`. It used to post `completed` however the tour ended, so
 * the admin user modal could never show Aborted for a V2 tour.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

type OnboardingPost = {
  status: string;
  last_step: string;
  is_rerun: boolean;
  // The gateway's answer, asserted in the test body: an expect() in the listener would throw
  // inside the event emitter and surface only as a poll timeout.
  httpStatus: number;
};

async function signIn(page: any) {
  await clearMailpit();
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', adminEmail);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(adminEmail);
  expect(token).toBeTruthy();
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard**`);
}

// Every body the page POSTs to /api/me/onboarding, in order, once the gateway has answered it --
// so a step does not reload the page while the last report is still in flight and abort it.
function recordOnboardingPosts(page: any): OnboardingPost[] {
  const posts: OnboardingPost[] = [];
  page.on('response', (res: any) => {
    const req = res.request();
    if (req.method() === 'POST' && req.url().endsWith('/api/me/onboarding')) {
      posts.push({ ...req.postDataJSON(), httpStatus: res.status() });
    }
  });
  return posts;
}

// The gateway has answered these in anything from milliseconds to 16s on a stack that has just
// started, and the tour waits for the first one before it opens.
const SLOW = { timeout: 30000 };

async function startRerun(page: any) {
  await page.reload();
  await page.getByRole('button', { name: /What's New & Help/ }).click();
  await page
    .getByRole('button', { name: 'Onboarding Tour', exact: true })
    .click();
}

const popover = (page: any) => page.locator('.driver-popover');

// One test with a step per case, signing in once: the gateway limits magic-link requests per
// address, so a sign-in per test hands the later ones a token already spent.
test('the V2 onboarding tour reports how it ended', async ({ page }) => {
  test.setTimeout(180000);
  await signIn(page);
  const posts = recordOnboardingPosts(page);

  await test.step('a rerun closed on its first step is aborted at step_0', async () => {
    posts.length = 0;
    await startRerun(page);
    await expect(popover(page)).toBeVisible(SLOW);
    await page.locator('.driver-popover-close-btn').click();
    await expect(popover(page)).toHaveCount(0);
    await expect.poll(() => posts.length, SLOW).toBe(2);
    expect(posts[0]).toEqual({
      status: 'in_progress',
      last_step: 'welcome',
      is_rerun: true,
      httpStatus: 200,
    });
    expect(posts[1]).toEqual({
      status: 'aborted',
      last_step: 'step_0',
      is_rerun: false,
      httpStatus: 200,
    });
  });

  await test.step('a rerun closed part-way is aborted at the step reached', async () => {
    posts.length = 0;
    await startRerun(page);
    await page.locator('.driver-popover-next-btn').click();
    await expect(popover(page)).toContainText(/2 of \d+/);
    await page.keyboard.press('Escape');
    await expect(popover(page)).toHaveCount(0);
    await expect.poll(() => posts.length, SLOW).toBe(2);
    expect(posts[1]).toEqual({
      status: 'aborted',
      last_step: 'step_1',
      is_rerun: false,
      httpStatus: 200,
    });
  });

  await test.step('a tour followed to Done is completed at the last step', async () => {
    posts.length = 0;
    await startRerun(page);
    const progress = page.locator('.driver-popover-progress-text');
    await expect(progress).toBeVisible(SLOW);
    const total = Number(
      /of\s+(\d+)/.exec((await progress.textContent()) || '')![1],
    );
    for (let i = 1; i < total; i++) {
      await page.locator('.driver-popover-next-btn').click();
      await expect(progress).toContainText(`${i + 1} of ${total}`);
    }
    // The last step's Next button is Done.
    await expect(page.locator('.driver-popover-next-btn')).toHaveText(/Done/);
    await page.locator('.driver-popover-next-btn').click();
    await expect(popover(page)).toHaveCount(0);
    await expect.poll(() => posts.length, SLOW).toBe(2);
    expect(posts[1]).toEqual({
      status: 'completed',
      last_step: `step_${total - 1}`,
      is_rerun: false,
      httpStatus: 200,
    });
  });

  await test.step('a first run is not a rerun', async () => {
    // /api/me does not send onboarding_status today, so the tour never starts by itself for
    // anyone; this is the answer a pending user would get once it does.
    await page.route('**/api/me', async (route: any) => {
      if (route.request().method() !== 'GET') return route.continue();
      const res = await route.fetch();
      const body = await res.json();
      body.onboarding_status = 'pending';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      });
    });
    posts.length = 0;
    await page.reload();
    await expect(popover(page)).toBeVisible(SLOW);
    await page.locator('.driver-popover-close-btn').click();
    await expect.poll(() => posts.length, SLOW).toBe(2);
    expect(posts[0]).toEqual({
      status: 'in_progress',
      last_step: 'welcome',
      is_rerun: false,
      httpStatus: 200,
    });
    expect(posts[1].status).toBe('aborted');
    expect(posts[1].httpStatus).toBe(200);
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });
});
