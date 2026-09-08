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

/** The login link both portals put in a magic-link mail. Nothing else in the inbox looks like it. */
const MAGIC_LINK = /\/portal(?:v2\/login)?\?token=([a-zA-Z0-9]+)/;

/** Every message Mailpit is holding right now. Anything absent from this set arrived later. */
async function mailpitMessageIDs(ctx: APIRequestContext): Promise<Set<string>> {
  const res = await ctx.get(`${MAILPIT}/api/v1/messages`);
  const messages = (await res.json()).messages || [];
  return new Set<string>(messages.map((m: { ID: string }) => m.ID));
}

/**
 * Poll Mailpit for a message to `to` whose body matches, and return that body.
 *
 * `ignoreIDs` is what makes this safe to use on an inbox that was not cleared first. Mailpit
 * answers newest-first, so without it the first matching message wins -- which is the newly sent
 * one only if it has already been delivered. `POST /api/auth/magic-link` sends from a goroutine
 * and answers before it (deliberately: a blocking send would make SMTP latency a timing oracle
 * for whether an address exists, `handleAdminMagicLink` in pkg/server/server.go), so for a few
 * milliseconds after that call returns the newest matching mail is still the PREVIOUS one. Pass
 * the IDs seen before the request and the poll waits for the real answer instead of accepting a
 * stale one.
 */
async function waitForMail(
  ctx: APIRequestContext,
  to: string,
  matching: RegExp,
  ignoreIDs?: Set<string>,
): Promise<string> {
  for (let i = 0; i < 40; i++) {
    const res = await ctx.get(`${MAILPIT}/api/v1/messages`);
    const messages = (await res.json()).messages || [];
    for (const m of messages) {
      if (!m.To || m.To[0].Address !== to) continue;
      if (ignoreIDs?.has(m.ID)) continue;
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

/**
 * Deletes a user created by createApprovedUser.
 *
 * Not optional tidiness. Every spec shares one database and they run in file order, so a row
 * left behind is a row the next spec sees: portal_v2_table_scroll asserts the Admin Users table
 * fits a 1280px viewport, and an extra account with a longer address than admin@lfr-demo.local
 * widens the email column enough to overflow it -- in CI, where the font stack is wider than
 * macOS's, so it passes locally either way (#1512).
 *
 * Shortening the address was not enough: the column sizes to its widest cell, so any second
 * account wider than the first moves it. Leaving nothing behind is the only version of this that
 * does not depend on guessing how much slack the layout has.
 *
 * Every step is checked, and named in its own error. This used to request the link, read whatever
 * matched `token=` and post it to /api/auth/verify without looking at the answer, so a session
 * that was never established surfaced two steps later as
 * `deleting <email> failed: 401 Unauthorized: admin access required` -- a message about the
 * DELETE, on a run where the DELETE was the one thing that had behaved correctly (#1833).
 */
export async function deleteUser(email: string): Promise<void> {
  const ctx = await request.newContext();

  // An admin session, obtained the same way a person gets one. The context keeps the cookie.
  //
  // The inbox is NOT cleared first -- specs own their own mail -- so the mails already in it are
  // recorded and skipped. The admin has almost always just signed in, leaving a magic-link mail
  // whose token that login consumed, and requesting this one invalidates it a second time
  // (`InvalidateOtherMagicLinks`). Accepting it yields 401 "Invalid or already used token" from
  // verify, then 401 from the DELETE.
  const seen = await mailpitMessageIDs(ctx);
  const requested = await ctx.post(`${API}/api/auth/magic-link`, {
    data: { email: ADMIN_EMAIL },
  });
  if (!requested.ok()) {
    throw new Error(
      `requesting a cleanup magic link for the admin failed: ${requested.status()} ${await requested.text()}`,
    );
  }

  // Matched on the login-link shape rather than a bare `token=`: setup and approval mails carry
  // a `token=` too, and neither is a magic link.
  const mail = await waitForMail(ctx, ADMIN_EMAIL, MAGIC_LINK, seen);
  const token = mail.match(MAGIC_LINK)?.[1];
  if (!token) throw new Error('No magic-link token for the admin');
  const verified = await ctx.post(`${API}/api/auth/verify`, {
    data: { token, lang: 'en' },
  });
  if (!verified.ok()) {
    throw new Error(
      `the admin session for cleaning up ${email} could not be established: ` +
        `/api/auth/verify answered ${verified.status()} ${await verified.text()}`,
    );
  }

  const res = await ctx.delete(
    `${API}/api/admin/users/${encodeURIComponent(email)}`,
  );
  if (!res.ok()) {
    // Loud rather than silent: a cleanup that quietly failed would leave the next spec to fail
    // instead, somewhere that looks unrelated -- which is exactly how this was found.
    throw new Error(
      `deleting ${email} failed: ${res.status()} ${await res.text()}`,
    );
  }

  await ctx.dispose();
}
