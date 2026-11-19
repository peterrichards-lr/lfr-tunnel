import { expect, type Page } from '@playwright/test';
import { clearMailpit, getMagicLinkToken } from './mailpit';

/**
 * Where each portal is served (#2528), so a spec names a portal rather than restating its path.
 *
 * V2 is the portal, at PORTAL_BASE (ui/src/portalBase.ts). V1 is served only to an admin
 * session, at the gateway's classic_portal_path -- tests/e2e/server-config.yaml sets it to
 * this value.
 */
export const V2 = '/portal';
export const V1 = '/classic-e2e-4k7q9z';

/**
 * Signs in by magic link `token` -- through V2, the only sign-in page there is -- and opens V1
 * at `path` beneath its base. V1 serves an admin session only; anyone else is sent to V2.
 */
export async function openV1(
  page: Page,
  token: string,
  path = '',
): Promise<void> {
  await page.goto(`${V2}/login?token=${token}`);
  await page.waitForURL(`**${V2}/dashboard**`);
  await page.goto(`${V1}${path}`);
}

/** Signs the admin in by magic link and opens V1 at `path`, waiting for its dashboard. */
export async function signInToV1(page: Page, path = ''): Promise<void> {
  const email = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml
  await clearMailpit();
  await page.goto(`${V2}/login`);
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await openV1(page, token, path);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}
