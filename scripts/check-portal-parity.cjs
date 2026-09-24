#!/usr/bin/env node
/**
 * Fail when a capability exists in one portal arm and not the other.
 *
 * V1 and V2 are an A/B test: the same person may be served either, so a feature in only one of
 * them is a defect rather than a roadmap item. Access control was exactly that -- V2 had a full
 * dialog, V1 could only DISPLAY the mode and whitelist in a read-only line, and nobody noticed
 * because each arm was only ever checked against itself (#2101).
 *
 * Asserts the CLASS: for each capability below, both arms must show evidence of it. Checking
 * "does V1 have access control" alone is what allowed this; the pairing is the test.
 *
 * Wired up is not the same as findable. V2 could always reach the access-control endpoint, so the
 * capability half of this gate was green throughout #2101 -- and the owner still reported the
 * feature missing from both arms, because V2's only affordance was a bare padlock sitting between
 * two text buttons (#2114). So where a capability names its opener, the control that opens it must
 * say what it does IN WORDS, in both arms.
 *
 * Every marker below is matched by findMarker(), NOT by String.includes(). A bare substring match
 * is satisfied by any rename that EXTENDS the old name -- `session_keys_unacked` still "found" in
 * a file that only has `session_keys_unacked_MUTANT` -- so the gate vouches for a marker that no
 * longer exists (#2201). That is the same rot openerLabel() has always refused to be quiet about;
 * the needle half simply had no equivalent. The one deliberate exception is a marker containing
 * `/`, which is a URL path: a sub-route is still the capability, so those go on matching loosely.
 * Both halves of that rule are asserted in tests/hooks/test-gate-scope-boundaries.sh rather than
 * stated only here -- prose does not fail a build (github-workflow SKILL 5b rule 6).
 */

'use strict';

const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..');

// Each capability names what to look for in each arm. Deliberately behavioural markers -- the
// endpoint or the handler -- rather than wording, which changes without the capability changing.
const CAPABILITIES = [
  {
    name: 'set a reservation passcode / IP whitelist',
    v1: {
      files: ['pkg/server/static/dashboard.js'],
      needle: '/api/portal/reservations/access-control',
      // The button a person actually clicks to get at it, named by its handler call.
      opener: {
        files: ['pkg/server/static/dashboard.js'],
        call: 'openReservationAcModal(',
      },
    },
    v2: {
      files: ['ui/src/components/ReservationsPanel.tsx'],
      needle: '/api/portal/reservations/access-control',
      opener: {
        files: ['ui/src/components/ReservationsTable.tsx'],
        call: 'openAcModal(r)',
      },
    },
  },
  {
    name: 'confirm a passcode before saving it',
    v1: {
      files: ['pkg/server/static/dashboard.js'],
      needle: 'reservation-ac-passcode-confirm',
    },
    v2: {
      files: ['ui/src/components/ReservationsPanel.tsx'],
      needle: 'acPasscodeConfirm',
    },
  },
  {
    // #2196. Rotating the fleet's visitor session-signing key is an operator action with no
    // other route -- there is no CLI for it -- so an arm without the control cannot do it at
    // all, which is the shape of #2101 exactly.
    name: 'trigger a visitor session-key rotation',
    v1: {
      files: ['pkg/server/static/dashboard.js'],
      needle: '/api/admin/session-secrets/rotate',
      opener: {
        files: ['pkg/server/dashboard.html'],
        call: 'rotateSessionKeys()',
      },
    },
    v2: {
      files: ['ui/src/pages/AdminSettings.tsx'],
      needle: '/api/admin/session-secrets/rotate',
      opener: {
        files: ['ui/src/pages/AdminSettings.tsx'],
        call: 'setSessionKeysConfirming(true)',
      },
    },
  },
  {
    // The half that is easy to drop, and the half that matters most: without the abort reason
    // rendered, a rotation that keeps failing closed looks exactly like one that works. An arm
    // that shows the button but not the outcome is worse than one that shows neither, because
    // it invites the click and then says nothing about it.
    name: 'see why the last session-key rotation aborted',
    v1: {
      files: ['pkg/server/static/dashboard.js'],
      needle: 'session_keys_reason',
    },
    v2: {
      files: ['ui/src/pages/AdminSettings.tsx'],
      needle: 'session_keys_reason',
    },
  },
  {
    // Which nodes did not acknowledge, named. "2 of 3 nodes did not confirm" is the reason
    // string; this is the list, and it is what turns an abort into something actionable.
    name: 'name the nodes that did not acknowledge a rotation',
    v1: {
      files: ['pkg/server/static/dashboard.js'],
      needle: 'session_keys_unacked',
    },
    v2: {
      files: ['ui/src/pages/AdminSettings.tsx'],
      needle: 'session_keys_unacked',
    },
  },
];

// What can continue a symbol, an i18n key or an HTML id. `-` is in here because half the markers
// above are kebab-case, and without it `reservation-ac-passcode-confirm` would go on matching
// `reservation-ac-passcode-confirm-v2` -- the very rename this is here to catch.
const TOKEN_CHAR = /[A-Za-z0-9_$-]/;

/**
 * Where `marker` occurs in `src` as a WHOLE marker, or -1.
 *
 * Boundaries are required only on the sides where the marker's own edge is a token character, the
 * same rule `\b` uses: `openReservationAcModal(` ends in a delimiter already, so nothing may
 * precede it but anything may follow. A marker containing `/` is a URL path and is matched as a
 * plain substring on purpose -- `/api/x` should still be found in a tree that has moved it to
 * `/api/x/bulk`, because the capability is reached either way.
 *
 * Done with indexOf rather than a RegExp so the marker needs no escaping: these contain `/`, `(`,
 * `)` and `-`, and an escape helper is one more thing that can be wrong about a marker.
 */
function findMarker(src, marker) {
  if (!marker) return -1;
  if (marker.includes('/')) return src.indexOf(marker);
  const boundLeft = TOKEN_CHAR.test(marker[0]);
  const boundRight = TOKEN_CHAR.test(marker[marker.length - 1]);
  for (
    let at = src.indexOf(marker);
    at !== -1;
    at = src.indexOf(marker, at + 1)
  ) {
    const before = at === 0 ? '' : src[at - 1];
    const after = src[at + marker.length] || '';
    if (boundLeft && before && TOKEN_CHAR.test(before)) continue;
    if (boundRight && after && TOKEN_CHAR.test(after)) continue;
    return at;
  }
  return -1;
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
    const src = fs.readFileSync(file, 'utf8');
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
 * so that quote parity is right in both arms: V1's call sits INSIDE a double-quoted `onclick=`
 * attribute, and a scan beginning there reads every later quote inverted. JSX expressions are
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
    const src = fs.readFileSync(file, 'utf8');
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
  const hitV1 = locateMarker(cap.v1.files, cap.v1.needle);
  const hitV2 = locateMarker(cap.v2.files, cap.v2.needle);
  const inV1 = hitV1.found;
  const inV2 = hitV2.found;

  if (inV1 && inV2) {
    for (const [arm, spec] of [
      ['V1', cap.v1],
      ['V2', cap.v2],
    ]) {
      if (!spec.opener) continue;
      const label = openerLabel(spec.opener);
      if (label === null) {
        failures += 1;
        console.log(
          `  \u2717 "${cap.name}": ${arm}'s opener \`${spec.opener.call}\` is not in ` +
            `${spec.opener.files.join(', ')} any more. Point this check at the control that ` +
            `replaced it -- an unfindable marker vouches for nothing.`,
        );
      } else if (!/[A-Za-z]/.test(label)) {
        failures += 1;
        console.log(
          `  \u2717 "${cap.name}": ${arm}'s opener shows ${label ? `"${label}"` : 'nothing'} and ` +
            `no words.\n` +
            `      An icon alone is decoration until you already know what it does. The owner ` +
            `reported this capability missing from a portal that had it (#2114).`,
        );
      }
    }
    continue;
  }

  failures += 1;
  const present = inV1 ? 'V1' : 'V2';
  const missing = inV1 ? 'V2' : 'V1';

  // The rename case first, because it is a different instruction to the reader: the marker IS
  // in the file, as the prefix of something longer, and the arm may well still have the
  // capability under its new name.
  const rotted = [
    ['V1', cap.v1, hitV1],
    ['V2', cap.v2, hitV2],
  ].filter(([, , hit]) => hit.looseOnly);
  if (rotted.length > 0) {
    for (const [arm, spec] of rotted) {
      console.log(
        `  ✗ "${cap.name}": ${arm}'s marker \`${spec.needle}\` is only present as part of ` +
          `a longer symbol in ${spec.files.join(', ')}.\n      ${ROT_HINT}`,
      );
    }
    continue;
  }

  if (!inV1 && !inV2) {
    console.log(
      `  ✗ "${cap.name}" is in NEITHER arm. Either both lost it, or this check is ` +
        `looking for a marker that no longer exists -- which is worse than not checking.`,
    );
  } else {
    console.log(
      `  ✗ "${cap.name}" is in ${present} but not ${missing}.\n` +
        `      The portals are an A/B test: the same person may be served either arm, so a ` +
        `capability in one is not a capability.`,
    );
  }
}

// Same data, same name.
//
// The capabilities above deliberately ignore WORDING, because wording changes without a
// capability changing. This is a different question: where both arms render the SAME field from
// the SAME endpoint, they must resolve it through the same i18n key. A key is not wording -- it
// is the claim that the two arms are naming one thing -- and the wording then follows from the
// bundle, in every locale, for both.
//
// V2's Client Versions table headed a user count "Active Tunnels" while V1 headed the identical
// column "User Count" from the identical endpoint (#2158). The figure is COUNT(*) FROM users
// grouped by last_client_version, so V2's header was wrong on both words, and no gate could see
// it: the string was hardcoded, so check-i18n-keys had no key to find missing.
const SHARED_LABELS = [
  {
    name: 'the Client Versions count column (/api/admin/analytics/clients)',
    key: 'th_user_count',
    v1: 'pkg/server/dashboard.html',
    v2: 'ui/src/pages/AdminAnalytics.tsx',
  },
  {
    // #2196. Both arms label current_generation from GET /api/admin/session-secrets, and an
    // operator comparing the two portals must be reading one field under one name.
    name: 'the current session-key generation (/api/admin/session-secrets)',
    key: 'session_keys_current',
    v1: 'pkg/server/static/dashboard.js',
    v2: 'ui/src/pages/AdminSettings.tsx',
  },
  {
    // The interval and the lag are STATED by the endpoint so that neither arm keeps a second
    // copy. Both arms must therefore render them through the one sentence, or one of them is
    // free to describe a cadence the engine does not run.
    name: 'the rotation interval and retirement lag (/api/admin/session-secrets)',
    key: 'session_keys_interval_note',
    v1: 'pkg/server/static/dashboard.js',
    v2: 'ui/src/pages/AdminSettings.tsx',
  },
];

for (const label of SHARED_LABELS) {
  for (const [arm, rel] of [
    ['V1', label.v1],
    ['V2', label.v2],
  ]) {
    const file = path.join(ROOT, rel);
    if (!fs.existsSync(file)) {
      failures += 1;
      console.log(
        `  \u2717 "${label.name}": ${arm}'s file ${rel} does not exist.`,
      );
      continue;
    }
    const hit = locateMarker([rel], label.key);
    if (hit.found) continue;
    failures += 1;
    if (hit.looseOnly) {
      console.log(
        `  \u2717 "${label.name}": ${arm}'s key '${label.key}' is only present as part of a ` +
          `longer key in ${rel}.\n      ${ROT_HINT}`,
      );
      continue;
    }
    console.log(
      `  \u2717 "${label.name}": ${arm} does not use the '${label.key}' key.\n` +
        `      Both arms render this column from one endpoint, so both must name it the same ` +
        `way. A hardcoded header is invisible to check-i18n-keys -- it has no key to report ` +
        `missing -- and goes untranslated in every locale besides English.`,
    );
  }
}

console.log(
  failures === 0
    ? `✅ ${CAPABILITIES.length} capability/capabilities present, and named in words, in both ` +
        `portal arms; ${SHARED_LABELS.length} shared column label(s) resolve through one key.`
    : `\n❌ ${failures} parity problem(s): a capability is missing from an arm, or its control ` +
        `does not say what it does.`,
);
process.exit(failures === 0 ? 0 : 1);
