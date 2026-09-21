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
];

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
    const at = src.indexOf(spec.call);
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

function findsIt(spec) {
  return spec.files.some((rel) => {
    const file = path.join(ROOT, rel);
    if (!fs.existsSync(file)) return false;
    return fs.readFileSync(file, 'utf8').includes(spec.needle);
  });
}

let failures = 0;

for (const cap of CAPABILITIES) {
  const inV1 = findsIt(cap.v1);
  const inV2 = findsIt(cap.v2);

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

console.log(
  failures === 0
    ? `✅ ${CAPABILITIES.length} capability/capabilities present, and named in words, in both ` +
        `portal arms.`
    : `\n❌ ${failures} parity problem(s): a capability is missing from an arm, or its control ` +
        `does not say what it does.`,
);
process.exit(failures === 0 ? 0 : 1);
