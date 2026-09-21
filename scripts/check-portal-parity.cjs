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
    },
    v2: {
      files: ['ui/src/components/ReservationsPanel.tsx'],
      needle: '/api/portal/reservations/access-control',
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

  if (inV1 && inV2) continue;

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
    ? `✅ ${CAPABILITIES.length} capability/capabilities present in both portal arms.`
    : `\n❌ ${failures} capability/capabilities missing from one arm.`,
);
process.exit(failures === 0 ? 0 : 1);
