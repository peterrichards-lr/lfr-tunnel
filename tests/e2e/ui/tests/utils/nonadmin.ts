import { request, APIRequestContext } from '@playwright/test';

/**
 * Creating an approved NON-ADMIN user (#1512).
 *
 * Every other spec signs in as admin@lfr-demo.local, so the non-admin view of either portal had
 * no coverage anywhere -- which is how #1512 shipped: Portal V2's analytics page renders a
 * personal section perfectly well and was simply unreachable for the people it was written for,
 * and no test could have noticed.
 *
 * This goes through the real registration and approval path rather than inserting a row: the
 * approval flow issues the personal access token and sets the fields the portal reads, so a
 * user conjured straight into the database would be subtly unlike a real one and the test would
 * pass against a state that never occurs.
 */

const API = 'http://localhost:8000';
const MAILPIT = 'http://localhost:8025';
const ADMIN_EMAIL = 'admin@lfr-demo.local';

/** Poll Mailpit for the newest message to `to` whose body matches, and return that body. */
async function waitForMail(
  ctx: APIRequestContext,
  to: string,
  matching: RegExp,
): Promise<string> {
  for (let i = 0; i < 40; i++) {
    const res = await ctx.get(`${MAILPIT}/api/v1/messages`);
    const messages = (await res.json()).messages || [];
    for (const m of messages) {
      if (!m.To || m.To[0].Address !== to) continue;
      const detail = await ctx.get(`${MAILPIT}/api/v1/message/${m.ID}`);
      const body = (await detail.json()).Text || '';
      if (matching.test(body)) return body;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`No mail to ${to} matching ${matching} within 20s`);
}

/**
 * Registers `email`, approves it as the admin would from the notification email, and leaves the
 * account ready to sign in with a magic link.
 *
 * Returns nothing: the caller signs in through the UI, because that is the path under test.
 */
export async function createApprovedUser(
  email: string,
  firstName = 'Nonadmin',
  lastName = 'Tester',
): Promise<void> {
  const ctx = await request.newContext();

  const reg = await ctx.post(`${API}/api/register-request`, {
    data: {
      email,
      first_name: firstName,
      last_name: lastName,
      preferred_name: firstName,
    },
  });
  if (!reg.ok()) {
    throw new Error(
      `register-request for ${email} failed: ${reg.status()} ${await reg.text()}`,
    );
  }

  // The admin is emailed an approval link carrying a one-time token. Taking the token from the
  // mail rather than from the database is deliberate -- it is the same value a human would
  // click, so the test exercises the flow an operator actually uses.
  const body = await waitForMail(ctx, ADMIN_EMAIL, /token=/);
  const token = body.match(/token=([a-zA-Z0-9]+)/)?.[1];
  if (!token) {
    throw new Error(`No approval token in the notification mail for ${email}`);
  }

  const approve = await ctx.post(`${API}/api/admin/approve`, {
    form: { email, token },
  });
  if (!approve.ok()) {
    throw new Error(
      `approving ${email} failed: ${approve.status()} ${await approve.text()}`,
    );
  }

  await ctx.dispose();
}
