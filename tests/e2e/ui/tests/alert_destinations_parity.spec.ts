import { test, expect, type Page } from './utils/fixtures';
import { getMagicLinkToken, clearMailpit } from './utils/mailpit';

/**
 * Per-alert Email and Slack toggles, both portals (#2445).
 *
 * One toggle per alert used to decide whether it was raised at all, so an owner working from
 * Slack could not keep alerts already actioned there out of their inbox without also losing them
 * in Slack. Each alert now has an Email and a Slack toggle, and both arms must render both: a
 * column present in one portal and not the other is the A/B parity defect (#1866).
 *
 * THE CONTROL is the gateway itself. The expected rows and states are read from
 * /api/admin/settings, so a portal that rendered one column, dropped a row, or showed the email
 * value in the Slack box fails here -- and a portal that rendered nothing fails the positive
 * count before any state is compared (e2e-testing skill §3).
 *
 * Nothing here SAVES, so no admin_settings row is left for the specs after it (skill §4). The
 * save round trip and the routing are covered in Go (alert_settings_test.go,
 * alert_method_test.go), where they need no shared database.
 */

const adminEmail = 'admin@lfr-demo.local'; // owner in tests/e2e/server-config.yaml

type Declared = { key: string; slack_key: string };

async function gatewayAlerts(page: Page) {
  return page.evaluate(async () => {
    const res = await fetch('/api/admin/settings', { credentials: 'include' });
    const body = await res.json();
    return {
      declared: (body.alert_settings || []) as Declared[],
      values: body as Record<string, string>,
    };
  });
}

// Every alert's two boxes, located by the accessible name both arms give them.
async function assertBothColumns(page: Page, labelOf: (key: string) => string) {
  const { declared, values } = await gatewayAlerts(page);
  // PREMISE: the gateway declared alerts, each with a Slack key of its own.
  expect(declared.length, 'the gateway declared no alerts').toBeGreaterThan(1);
  for (const a of declared) {
    expect(a.slack_key, `${a.key} has no Slack toggle`).toBeTruthy();
    expect(a.slack_key).not.toBe(a.key);
  }

  const boxes = page.locator(
    'input[type="checkbox"][aria-label$="Email"], input[type="checkbox"][aria-label$="Slack"]',
  );
  await expect(boxes).toHaveCount(declared.length * 2);

  for (const a of declared) {
    const label = labelOf(a.key);
    for (const [key, column] of [
      [a.key, 'Email'],
      [a.slack_key, 'Slack'],
    ]) {
      const box = page.getByRole('checkbox', {
        name: `${label} — ${column}`,
        exact: true,
      });
      await expect(box, `${a.key}: no ${column} checkbox`).toHaveCount(1);
      if (values[key] === 'true') {
        await expect(box).toBeChecked();
      } else {
        await expect(box).not.toBeChecked();
      }
    }
  }
}

// The label text of each row, as rendered: the row's first cell is the label.
async function labelsByKey(page: Page): Promise<Record<string, string>> {
  const { declared } = await gatewayAlerts(page);
  const labels = await page
    .locator('input[type="checkbox"][aria-label$=" — Email"]')
    .evaluateAll((els) =>
      els.map((el) =>
        (el.getAttribute('aria-label') || '').replace(/ — Email$/, ''),
      ),
    );
  // Rendered in declaration order by both arms.
  return Object.fromEntries(declared.map((a, i) => [a.key, labels[i]]));
}

test.describe('Alert destinations — Portal V2', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/portalv2/');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    await expect(page.locator('text=Magic link sent')).toBeVisible();
    const token = await getMagicLinkToken(adminEmail);
    await page.goto(`/portalv2/login?token=${token}`);
    await page.waitForURL('**/portalv2/dashboard');
    await page.goto('/portalv2/admin/settings');
    await expect(page.getByTestId('alert-settings-grid')).toBeVisible();
  });

  test('every alert has an Email and a Slack toggle, matching the gateway', async ({
    page,
  }) => {
    const labels = await labelsByKey(page);
    await assertBothColumns(page, (k) => labels[k]);
  });

  test('an unsaved Slack method is not reported as a fallback, and the card saves itself', async ({
    page,
  }) => {
    const select = page.getByTestId('alert-method-select');
    await expect(select).toBeVisible();
    const save = page.getByTestId('alert-method-save');
    // Positive anchor: the card has its own Save, disabled while nothing has changed.
    await expect(save).toBeVisible();
    await expect(save).toBeDisabled();

    // "off" is always offered and differs from the server-config default, so selecting it is
    // exactly the unsaved change that used to raise the warning.
    await select.selectOption('off');
    await expect(save).toBeEnabled();
    await expect(
      page.getByText(/no longer configured in server-config\.yaml/),
    ).toHaveCount(0);
    // Not saved: leaving the page discards it, so no override row is left behind.
  });

  test('the test button says what it does', async ({ page }) => {
    await expect(
      page.getByRole('button', { name: 'Send Test Alert' }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: /Trigger Test Webhook/i }),
    ).toHaveCount(0);
  });
});

test.describe('Alert destinations — Portal V1', () => {
  test.beforeEach(async ({ page }) => {
    await clearMailpit();
    await page.goto('/admin');
    await page.click('#btn-show-email');
    await page.fill('#email-input', adminEmail);
    await page.click('button[type="submit"]');
    const token = await getMagicLinkToken(adminEmail);
    expect(token).toBeTruthy();
    await page.goto(`/admin?token=${token}`);
    await expect(
      page.locator('h2:has-text("Dashboard Overview")'),
    ).toBeVisible();
    await page.goto('/portal/system');
  });

  test('every alert has an Email and a Slack toggle, matching the gateway', async ({
    page,
  }) => {
    // Wait for the loader to fill the container before reading labels (skill §3b).
    await expect(
      page.locator('#alert-settings-container input[type="checkbox"]').first(),
    ).toBeVisible();
    const labels = await labelsByKey(page);
    await assertBothColumns(page, (k) => labels[k]);
  });
});
