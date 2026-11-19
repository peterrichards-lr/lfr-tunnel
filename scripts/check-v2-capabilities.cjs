#!/usr/bin/env node
/**
 * check-v2-capabilities.cjs -- Portal V2 must keep every capability it was given (#2529).
 *
 * This was check-portal-parity.cjs (#2101), which required each capability below to be present
 * in BOTH portal arms, because V1 and V2 were an A/B test (decision 0004). Decision 0007 ended
 * that: V2 is the portal and V1 is a frozen, admin-only fallback, so a capability in V2 alone is
 * no longer a defect and V1 is not read at all.
 *
 * What survives is V2's half, which was never about symmetry: each entry is a capability V2 was
 * fixed to have, often after a defect report, and a refactor that drops the marker drops the
 * capability with nothing else to notice. Most entries have no e2e spec behind them. The
 * per-entry comments are the history of each fix; where they say "both arms" or "V1", that is
 * the A/B period they were written in.
 *
 * Wired up is not the same as findable (#2114): V2 could always reach the access-control
 * endpoint, and the owner still reported the feature missing, because V2's only affordance was a
 * bare padlock. So where a capability names its opener, the control that opens it must say what
 * it does IN WORDS.
 *
 * Every marker is matched by findMarker() from scripts/lib/token-match.cjs, NOT by
 * String.includes(): a rename that EXTENDS the old name (`session_keys_unacked` ->
 * `session_keys_unacked_MUTANT`) must not go on satisfying it (#2201). A marker containing `/` is
 * a URL path and matches loosely on purpose -- a sub-route is still the capability. Both halves
 * are asserted in tests/hooks/test-gate-scope-boundaries.sh.
 *
 * KNOWN LIMIT, stated so it is not mistaken for coverage: CAPABILITIES is a closed, hand-written
 * allow-list. A capability nobody added to it is invisible here. And it asserts SOURCE presence,
 * not render-time reachability -- #2241 shipped a control present in source and unreachable on
 * screen with this green throughout. tests/e2e/ui/tests/ is the half that watches the screen.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const { findMarker } = require('./lib/token-match.cjs');

const ROOT = path.join(__dirname, '..');

// Each capability names where in V2 to look and what to look for. Deliberately behavioural
// markers -- the endpoint or the handler -- rather than wording, which changes without the
// capability changing.
const CAPABILITIES = [
  {
    // #2447. V2's login page never asked which SSO providers exist, so a V2 user had no way to
    // sign in with SSO at all -- and the Okta rollout would have depended on V1 alone.
    name: 'sign in with a configured SSO provider',
    files: ['ui/src/pages/Login.tsx'],
    needle: "'/api/auth/providers'",
  },
  {
    // Same defect, other half: V2 kept offering a magic-link form on a gateway that refuses
    // every magic link.
    name: 'withdraw email sign-in when the operator disables it',
    files: ['ui/src/pages/Login.tsx'],
    needle: '{!emailLoginDisabled && (',
  },
  {
    // An invite is an emailed sign-in link; SSO-only refuses it, so neither arm may offer it (#2449).
    name: 'withdraw admin invites when email sign-in is disabled',
    files: ['ui/src/pages/AdminUsers.tsx'],
    needle: 'data-testid="invite-disabled-note"',
  },
  {
    // #2430: an analytics export prints the wide sections on landscape pages.
    name: 'print the wide analytics sections on landscape pages',
    files: ['ui/src/pages/AdminAnalytics.tsx'],
    needle: 'card p-xl mb-xl print-wide',
  },
  {
    // #2430: charts are redrawn at their printed size rather than scaled from the screen.
    name: 'redraw analytics charts at their printed size',
    files: ['ui/src/pages/AdminAnalytics.tsx'],
    needle: 'usePrintSizedCharts();',
  },
  {
    // #2430: no personal traffic is stated, not left as an empty section.
    name: 'say so when there is no personal usage to show',
    files: ['ui/src/pages/AdminAnalytics.tsx'],
    needle: 'data-testid="personal-usage-empty"',
  },
  {
    name: 'set a reservation passcode / IP whitelist',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: '/api/portal/reservations/access-control',
    opener: {
      files: ['ui/src/components/ReservationsTable.tsx'],
      call: 'openAcModal(r)',
    },
  },
  {
    name: 'confirm a passcode before saving it',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: 'acPasscodeConfirm',
  },
  {
    // #2196. Rotating the fleet's visitor session-signing key is an operator action with no
    // other route -- there is no CLI for it -- so an arm without the control cannot do it at
    // all, which is the shape of #2101 exactly.
    name: 'trigger a visitor session-key rotation',
    files: ['ui/src/pages/AdminSettings.tsx'],
    needle: '/api/admin/session-secrets/rotate',
    opener: {
      files: ['ui/src/pages/AdminSettings.tsx'],
      call: 'setSessionKeysConfirming(true)',
    },
  },
  {
    // The half that is easy to drop, and the half that matters most: without the abort reason
    // rendered, a rotation that keeps failing closed looks exactly like one that works. An arm
    // that shows the button but not the outcome is worse than one that shows neither, because
    // it invites the click and then says nothing about it.
    name: 'see why the last session-key rotation aborted',
    files: ['ui/src/pages/AdminSettings.tsx'],
    needle: 'session_keys_reason',
  },
  {
    // #2223/#2224. The portal register control is the ONLY route to a custom domain for a user
    // who is not admin or owner: auto-reservation at connect time reads
    // RoleSettings[role].AllowAutoReservation, which ships true for exactly those two roles, and
    // every other role is refused at registration (#2221). So an arm without this control cannot
    // do the thing at all -- the shape of #2101 exactly, not a cosmetic difference.
    //
    // The needle is the endpoint rather than a symbol name: it is what the capability IS, and it
    // survives the renames a handler name does not. It contains `/`, so findMarker matches it
    // loosely by design -- a sub-route is still the capability.
    name: 'register a custom domain from the portal',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: '/api/portal/custom-domains',
    opener: {
      files: ['ui/src/components/ReservationsPanel.tsx'],
      call: 'register_custom_domain',
    },
  },
  {
    // The step the product cannot take for the user, and the reason a registered domain that
    // never gets a CNAME simply never provisions: the gateway proves control over ACME HTTP-01,
    // so a name that does not resolve here can never be served. An arm that asks for a domain
    // name without saying this sends the user away to find out why nothing happened.
    name: 'state the CNAME requirement where the domain is asked for',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: 'custom_domain_cname_hint',
  },
  {
    // #2233. The per-user provisioning view. The endpoint scopes to the caller
    // (handleListVanityDomainStatus), so it answers "did MY domain provision?" -- a question
    // #2223/#2224 made askable by letting a non-admin register one in the first place. V1's own
    // Custom Domains tab is NOT this capability: it reads /api/admin/vanity-domain-status and is
    // in ADMIN_ONLY_TABS, so a non-admin cannot reach it, which is why the needle names the
    // portal route specifically rather than `vanity-domain-status`.
    //
    // No opener: neither arm has one. The panel renders with its section, so there is no control
    // to name in words, and pointing openerLabel() at a button that does not exist would fail on
    // the marker rather than on the capability.
    //
    // What this entry CANNOT prove: it asserts source presence, not render-time reachability.
    // #2241 shipped a control present in both arms' source and unreachable in one, and this gate
    // was green throughout. portal_v1_custom_domain_status.spec.ts is the half that proves a
    // non-admin actually sees it.
    name: "see your own custom domain's provisioning status",
    files: ['ui/src/components/VanityDomainStatusPanel.tsx'],
    needle: '/api/portal/vanity-domain-status',
  },
  {
    // Which pinned above the rest: quota, conflict and invalid-request are three different
    // instructions (release one / it is someone else's / fix the name), and the endpoint answers
    // the first two on the SAME 400. An arm that shows one "failed" toast has thrown that away.
    name: 'distinguish the custom-domain refusals from each other',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: 'error_custom_domain_invalid',
  },
  {
    // Which nodes did not acknowledge, named. "2 of 3 nodes did not confirm" is the reason
    // string; this is the list, and it is what turns an abort into something actionable.
    name: 'name the nodes that did not acknowledge a rotation',
    files: ['ui/src/pages/AdminSettings.tsx'],
    needle: 'session_keys_unacked',
  },
  {
    // #2259: the token-expiry select offered different options in each arm, and each arm
    // decided the never-expiring one from `role === 'admin'` -- two gates that disagreed with
    // each other for months while the server enforced neither.
    //
    // The marker is the POLICY, not the wording. Both arms must read never_expires from
    // /api/version; an arm that renders the option from anything else is the defect, whatever
    // it calls it.
    // No `opener` here, deliberately: that mechanism asserts a BUTTON says what it does, and
    // this capability is an <option> inside a select. The needle is the whole assertion --
    // the option must be rendered from the advertised policy, in both arms.
    name: 'render the never-expiring token option from the operator policy',
    files: ['ui/src/pages/Dashboard.tsx'],
    needle: "neverExpires.tokens !== 'disabled' &&",
  },
  {
    // The admin extension queue offered "Approve Permanent" unconditionally, so on a gateway
    // whose policy is `disabled` it was a button that always answered 403 -- and it called a
    // custom domain a subdomain, because a custom domain IS a reservation with an empty
    // subdomain (#1004) and neither arm said so.
    name: 'gate the permanent approval on what the gateway will accept',
    files: ['ui/src/pages/AdminExtensions.tsx'],
    needle: 'req.permanence_allowed &&',
  },
  {
    // #2273. One handler serves both the subdomain and custom-domain tables in each arm, and
    // every string it showed was written for a subdomain -- so releasing demo.customer.com
    // asked the user to confirm releasing "this subdomain" and then said one was released.
    name: 'name the resource being released in the confirmation',
    files: ['ui/src/components/ReservationsPanel.tsx'],
    needle: 'confirm_release_custom_domain',
  },
  {
    // Same queue: which resource each row is about.
    name: 'name the resource an extension request is about',
    files: ['ui/src/pages/AdminExtensions.tsx'],
    needle: "req.resource_kind === 'custom_domain'",
  },
];

/**
 * Source with comments removed, string and template literals left intact.
 *
 * A needle found only in a COMMENT vouches for nothing, and the gate could not tell the two
 * apart: commenting out the one call that applies the token-expiry policy left this check green,
 * because the call text was still there behind a `//` (found reviewing #2266). That is the same
 * defect as a marker satisfied by a rename -- the gate reporting a control it cannot see.
 *
 * Quote-aware, and it has to be: several needles here are URL paths, and a naive strip from the
 * first `//` would cut `'https://...'` in half and make every one of them vanish.
 */
function stripComments(src) {
  let out = '';
  let quote = null;
  for (let i = 0; i < src.length; i += 1) {
    const c = src[i];
    if (quote) {
      out += c;
      if (c === '\\') {
        out += src[i + 1] ?? '';
        i += 1;
      } else if (c === quote) {
        quote = null;
      }
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      quote = c;
      out += c;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      while (i < src.length && src[i] !== '\n') i += 1;
      out += '\n';
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const end = src.indexOf('*/', i + 2);
      i = end === -1 ? src.length : end + 1;
      out += ' ';
      continue;
    }
    out += c;
  }
  return out;
}

/**
 * Whether `marker` is in any of `files` -- and, when it is not, whether a LOOSE match would have
 * found it anyway.
 *
 * `looseOnly` is the signature of the rename this gate used to be blind to, and it is worth
 * reporting separately: "the marker is gone" and "the marker is now the prefix of something else"
 * ask the reader to do different things.
 */
function locateMarker(files, marker) {
  let looseOnly = false;
  for (const rel of files) {
    const file = path.join(ROOT, rel);
    if (!fs.existsSync(file)) continue;
    const src = stripComments(fs.readFileSync(file, 'utf8'));
    if (findMarker(src, marker) !== -1)
      return { found: true, looseOnly: false };
    if (src.includes(marker)) looseOnly = true;
  }
  return { found: false, looseOnly };
}

const ROT_HINT =
  'A rename that EXTENDS the old name leaves the marker a substring of its replacement, so ' +
  'matching loosely would vouch for something that no longer exists (#2201). Point this check ' +
  'at the name that replaced it.';

/**
 * The children of the <button> that contains `call`, as a reader would see them.
 *
 * Walks from the `<button` that encloses the handler call to the `>` that closes its open tag --
 * tracking quotes and braces, because `onClick={() => openAcModal(r)}` contains a `>` that ends
 * nothing -- then takes everything up to `</button>`. It starts at the tag rather than at the call
 * so that quote parity is right wherever the call sits: inside a double-quoted `onclick=`
 * attribute (as V1's did), a scan beginning at the call reads every later quote inverted. JSX expressions are
 * reduced to the default string of a `t(key, default)` lookup where there is one, since that is
 * the text that reaches the screen.
 *
 * Returns null when the call is not found at all: a marker that has rotted away must fail loudly
 * rather than quietly vouch for nothing.
 */
function openerLabel(spec) {
  for (const rel of spec.files) {
    const file = path.join(ROOT, rel);
    if (!fs.existsSync(file)) continue;
    const src = stripComments(fs.readFileSync(file, 'utf8'));
    const at = findMarker(src, spec.call);
    if (at === -1) continue;
    const open = src.lastIndexOf('<button', at);
    if (open === -1) continue;

    let i = open;
    let depth = 0;
    let quote = null;
    for (; i < src.length; i += 1) {
      const c = src[i];
      if (quote) {
        if (c === '\\') i += 1;
        else if (c === quote) quote = null;
        continue;
      }
      if (c === '"' || c === "'" || c === '`') quote = c;
      else if (c === '{') depth += 1;
      else if (c === '}') depth -= 1;
      else if (c === '>' && depth <= 0 && src[i - 1] !== '=') break;
    }
    const close = src.indexOf('</button>', i);
    if (close === -1) continue;

    return src
      .slice(i + 1, close)
      .replace(
        /\{[^{}]*?t\(\s*['"][^'"]*['"]\s*,\s*['"]([^'"]*)['"][\s\S]*?\}/g,
        '$1',
      )
      .replace(/\{[\s\S]*?\}/g, '')
      .replace(/<[^>]*>/g, '')
      .trim();
  }
  return null;
}

let failures = 0;

for (const cap of CAPABILITIES) {
  const hit = locateMarker(cap.files, cap.needle);

  if (hit.found) {
    if (!cap.opener) continue;
    const label = openerLabel(cap.opener);
    if (label === null) {
      failures += 1;
      console.log(
        `  \u2717 "${cap.name}": the opener \`${cap.opener.call}\` is not in ` +
          `${cap.opener.files.join(', ')} any more. Point this check at the control that ` +
          `replaced it -- an unfindable marker vouches for nothing.`,
      );
    } else if (!/[A-Za-z]/.test(label)) {
      failures += 1;
      console.log(
        `  \u2717 "${cap.name}": the opener shows ${label ? `"${label}"` : 'nothing'} and ` +
          `no words.\n` +
          `      An icon alone is decoration until you already know what it does. The owner ` +
          `reported this capability missing from a portal that had it (#2114).`,
      );
    }
    continue;
  }

  failures += 1;
  if (hit.looseOnly) {
    // The rename case first, because it is a different instruction to the reader: the marker IS
    // in the file, as the prefix of something longer, and V2 may well still have the capability
    // under its new name.
    console.log(
      `  \u2717 "${cap.name}": the marker \`${cap.needle}\` is only present as part of a ` +
        `longer symbol in ${cap.files.join(', ')}.\n      ${ROT_HINT}`,
    );
  } else {
    console.log(
      `  \u2717 "${cap.name}" is not in Portal V2 (${cap.files.join(', ')}).\n` +
        `      Either V2 lost a capability it was fixed to have, or this check is looking for ` +
        `a marker that no longer exists -- which is worse than not checking. Decision 0007 ` +
        `makes V2 the only portal anyone is sent to, so there is no other arm to fall back on.`,
    );
  }
}

// Same data, same name: where V2 renders a field from an endpoint, it must do so through the
// i18n key, not a hardcoded string. A hardcoded header is invisible to check-i18n-keys -- it has
// no key to report missing -- and goes untranslated in every locale besides English. V2's Client
// Versions table headed a user count "Active Tunnels" from a hardcoded string (#2158).
// The i18n keys V2 must render these labels through (they were shared with V1 until #2529).
const SHARED_LABELS = [
  {
    // #2266. A custom domain is a reservation with an empty subdomain (#1004), so the
    // extension queue called every row a subdomain and showed an admin a blank name cell.
    // Both arms now render resource_kind, and an admin comparing the two portals must be
    // reading one field under one name.
    name: 'the extension queue Type column (/api/admin/reservations/extensions)',
    key: 'th_type',
    file: 'ui/src/pages/AdminExtensions.tsx',
  },
  {
    name: 'the Client Versions count column (/api/admin/analytics/clients)',
    key: 'th_user_count',
    file: 'ui/src/pages/AdminAnalytics.tsx',
  },
  {
    // #2196. Both arms label current_generation from GET /api/admin/session-secrets, and an
    // operator comparing the two portals must be reading one field under one name.
    name: 'the current session-key generation (/api/admin/session-secrets)',
    key: 'session_keys_current',
    file: 'ui/src/pages/AdminSettings.tsx',
  },
  {
    // The interval and the lag are STATED by the endpoint so that neither arm keeps a second
    // copy. Both arms must therefore render them through the one sentence, or one of them is
    // free to describe a cadence the engine does not run.
    name: 'the rotation interval and retirement lag (/api/admin/session-secrets)',
    key: 'session_keys_interval_note',
    file: 'ui/src/pages/AdminSettings.tsx',
  },
];

for (const label of SHARED_LABELS) {
  const file = path.join(ROOT, label.file);
  if (!fs.existsSync(file)) {
    failures += 1;
    console.log(`  \u2717 "${label.name}": ${label.file} does not exist.`);
    continue;
  }
  const hit = locateMarker([label.file], label.key);
  if (hit.found) continue;
  failures += 1;
  if (hit.looseOnly) {
    console.log(
      `  \u2717 "${label.name}": the key '${label.key}' is only present as part of a ` +
        `longer key in ${label.file}.\n      ${ROT_HINT}`,
    );
    continue;
  }
  console.log(
    `  \u2717 "${label.name}": ${label.file} does not use the '${label.key}' key.\n` +
      `      Render this label through the i18n key, or it goes untranslated in every locale ` +
      `besides English and check-i18n-keys cannot see it.`,
  );
}

console.log(
  failures === 0
    ? `\u2705 ${CAPABILITIES.length} capability/capabilities present in Portal V2, each opener ` +
        `named in words; ${SHARED_LABELS.length} label(s) resolve through their i18n key.`
    : `\n\u274c ${failures} problem(s): Portal V2 is missing a capability, or its control ` +
        `does not say what it does.`,
);
process.exit(failures === 0 ? 0 : 1);
