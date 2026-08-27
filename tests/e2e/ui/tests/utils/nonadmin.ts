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

  // 1. Request registration. This does NOT notify the admin -- it emails the applicant a
  //    verification link, and the admin hears nothing until that link is used. Worth stating
  //    because it is not obvious from the endpoint name, and assuming otherwise is what made
  //    the first version of this helper time out waiting for mail that was never going to
  //    arrive.
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

  // 2. Complete setup, using the token from the applicant's own verification mail. This is
  //    what the /setup page posts when a real person fills the form in.
  const verifyMail = await waitForMail(ctx, email, /setup\?token=/);
  const verifyToken = verifyMail.match(/setup\?token=([a-f0-9]+)/)?.[1];
  if (!verifyToken) {
    throw new Error(`No verification token in the setup mail for ${email}`);
  }
  const setup = await ctx.post(`${API}/api/complete-setup`, {
    data: {
      token: verifyToken,
      first_name: firstName,
      last_name: lastName,
      preferred_name: firstName,
      policy_consent: true,
    },
  });
  if (!setup.ok()) {
    throw new Error(
      `complete-setup for ${email} failed: ${setup.status()} ${await setup.text()}`,
    );
  }

  // 3. Approve, as the admin would by clicking the link they are now sent. Matched on the
  //    applicant's address as well as the token, because the admin receives two mails about
  //    every registration and only one of them carries an approval link.
  const approvalMail = await waitForMail(
    ctx,
    ADMIN_EMAIL,
    new RegExp(
      `admin/approve\\?email=${encodeURIComponent(email).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}&token=`,
    ),
  );
  const approvalToken = approvalMail.match(
    /admin\/approve\?email=[^&\s]+&token=([a-f0-9]+)/,
  )?.[1];
  if (!approvalToken) {
    throw new Error(`No approval token in the notification mail for ${email}`);
  }

  const approve = await ctx.post(`${API}/api/admin/approve`, {
    form: { email, token: approvalToken },
  });
  if (!approve.ok()) {
    throw new Error(
      `approving ${email} failed: ${approve.status()} ${await approve.text()}`,
    );
  }

  await ctx.dispose();
}
