import { test, expect } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';
import { createApprovedUser, deleteUser } from './utils/nonadmin';

/**
 * V1 telemetry screen (#1559).
 *
 * V1 has consumed /api/portal/telemetry/ws all along, but only used the payload to refresh other
 * views -- it had no telemetry screen, while V2 has had one. So the two arms of the A/B test
 * differed in what an admin could see about live traffic, despite both receiving the same data.
 *
 * The aggregates are asserted against a stubbed payload rather than live tunnels: the test stack
 * has no traffic, so the interesting values are all zero and a broken calculation would look
 * identical to a correct one.
 */
const adminEmail = 'admin@lfr-demo.local'; // From tests/e2e/server-config.yaml

async function loginV1(page: any, email: string) {
  await clearMailpit();
  await page.goto('/admin');
  await page.click('#btn-show-email');
  await page.fill('#email-input', email);
  await page.click('button[type="submit"]');
  const token = await getMagicLinkToken(email);
  expect(token).toBeTruthy();
  await page.goto(`/admin?token=${token}`);
  await expect(page.locator('h2:has-text("Dashboard Overview")')).toBeVisible();
}

test.describe('Portal V1 telemetry', () => {
  const nonAdminEmail = `tm${Date.now().toString().slice(-6)}@lfr-demo.local`;

  // Created in beforeAll rather than inside the test that uses it. When an earlier test fails
  // the run can stop before reaching that one, leaving afterAll to delete a user that was never
  // created -- which surfaces as a confusing "User not found" alongside the real failure.
  test.beforeAll(async () => {
    await createApprovedUser(nonAdminEmail);
  });

  test.afterAll(async () => {
    await deleteUser(nonAdminEmail);
  });

  test('an admin reaches the screen and sees the aggregates', async ({
    page,
  }) => {
    await loginV1(page, adminEmail);
    await page.locator('#nav-telemetry').click();

    await expect(page.locator('#tab-telemetry')).toBeVisible();
    await expect(page.locator('#telemetry-active-tunnels')).toBeAttached();
    await expect(page.locator('#telemetry-total-bandwidth')).toBeAttached();
    // The aggregate V1 had nowhere before this.
    await expect(page.locator('#telemetry-active-nodes')).toBeAttached();
  });

  test('the aggregates are computed, not just displayed', async ({ page }) => {
    await loginV1(page, adminEmail);
    await page.locator('#nav-telemetry').click();
    await expect(page.locator('#tab-telemetry')).toBeVisible();

    // Two tunnels sharing one node, plus a third on another: Active Gateways must be 2, not 3.
    // Counting rows instead of distinct nodes is the obvious way to get this wrong, and with
    // real traffic absent from the test stack nothing else would reveal it.
    // Pushed through handleTelemetryPayload, the real socket entry point, rather than by
    // assigning window.currentUser -- `let currentUser` does not create a window property, so
    // that would set a different variable the renderer never reads. Going through the handler
    // also exercises the live-update path rather than just the renderer.
    await page.evaluate(() => {
      (window as any).handleTelemetryPayload({
        broadcast_message: '',
        maintenance_mode: 'false',
        tunnels: [
          {
            full_host: 'a.example.test',
            node_id: 'edge-1',
            client_ip: '10.0.0.1',
            bytes_in: 1024,
            bytes_out: 2048,
            status: 'active',
            visitor_ips: ['1.1.1.1', '2.2.2.2'],
          },
          {
            full_host: 'b.example.test',
            node_id: 'edge-1',
            client_ip: '10.0.0.2',
            bytes_in: 1024,
            bytes_out: 0,
            status: 'active',
            visitor_ips: [],
          },
          {
            full_host: 'c.example.test',
            node_id: 'edge-2',
            client_ip: '10.0.0.3',
            bytes_in: 0,
            bytes_out: 0,
            status: 'active',
            visitor_ips: ['3.3.3.3'],
          },
        ],
      });
    });

    await expect(page.locator('#telemetry-active-tunnels')).toHaveText('3');
    await expect(page.locator('#telemetry-active-nodes')).toHaveText('2');
    // 1024 + 2048 + 1024 = 4096 bytes in and out combined.
    await expect(page.locator('#telemetry-total-bandwidth')).toContainText('4');

    const rows = page.locator('#telemetry-table-body tr');
    await expect(rows).toHaveCount(3);
    await expect(rows.first()).toContainText('edge-1');
    // Visitors is a count of visitor_ips, not the raw array.
    await expect(rows.first()).toContainText('2');
  });

  test('a non-admin cannot reach it', async ({ page }) => {
    await loginV1(page, nonAdminEmail);

    await expect(page.locator('#nav-telemetry')).toBeHidden();
    await page.goto('/portal/telemetry');
    await expect(page.locator('#tab-telemetry')).toBeHidden();
  });
});
