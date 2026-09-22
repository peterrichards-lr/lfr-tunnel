#!/usr/bin/env node
/**
 * Fails when the three access-control forms disagree about which fields a mode uses (#2155).
 *
 * The same table now lives in four places: the client Inspector, Portal V1, Portal V2 and --
 * as the rule that refuses a save -- the server's missingAccessControlValue (#2156). Four
 * copies of one rule is exactly the shape that produced the defect this gate exists for:
 *
 *   - V2 offered three modes and the other two offered five, so a reservation in `or` or `and`
 *     opened in V2 with no radio selected and no fields at all.
 *   - `and` with an empty IP list saved happily in all three, and the tunnel then displayed
 *     the strictest setting in the product while enforcing the passcode alone.
 *
 * Neither was noticed because each surface was only ever compared against itself.
 *
 * WHAT THIS CHECKS, and what it deliberately does not:
 *
 * It reads the `accessControlFieldsForMode` function out of each of the three client files and
 * asserts they map the same modes to the same fields. It does NOT execute them -- one is inside
 * an HTML <script>, one is a browser global and one is TypeScript, and standing up three
 * runtimes to compare a five-row table costs more than it protects. The parse is deliberately
 * strict: an unreadable function fails rather than passing silently, because "I could not find
 * it" and "it agrees" must never look the same.
 */
const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');

const SOURCES = [
  ['Inspector', 'pkg/client/dashboard.html'],
  ['Portal V1', 'pkg/server/static/dashboard.js'],
  ['Portal V2', 'ui/src/components/ReservationsPanel.tsx'],
];

// The canonical table. Changing it means changing every surface AND the server, which is the
// point of writing it down once here.
const EXPECTED = {
  public: { passcode: false, whitelist: false },
  passcode: { passcode: true, whitelist: false },
  whitelist: { passcode: false, whitelist: true },
  or: { passcode: true, whitelist: true },
  and: { passcode: true, whitelist: true },
};

const failures = [];

/**
 * Pull the function body out of a file and read its switch cases.
 *
 * Case labels stack (`case 'or':` falling through to `case 'and':`), so labels are accumulated
 * until a return is reached and the returned pair is then applied to all of them.
 */
function tableFrom(label, source) {
  const start = source.indexOf('function accessControlFieldsForMode');
  if (start === -1) {
    failures.push(`${label}: no accessControlFieldsForMode function found`);
    return null;
  }

  // Anchored on `switch (mode) {`, NOT on the first { after the signature.
  //
  // V2's copy is TypeScript and annotates its return type:
  //
  //     function accessControlFieldsForMode(mode: string): {
  //       passcode: boolean;
  //       whitelist: boolean;
  //     } {
  //
  // so the first { belongs to the type, and brace matching from it stopped before the body had
  // begun -- reporting every mode as unhandled on a file that handles them all. Caught on the
  // gate's first run, which is the argument for running a new check against known-good input
  // before trusting it against bad.
  const open = source.indexOf('{', source.indexOf('switch', start));
  let depth = 0;
  let end = -1;
  for (let i = open; i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) {
        end = i;
        break;
      }
    }
  }
  if (end === -1) {
    failures.push(
      `${label}: could not find the end of accessControlFieldsForMode`,
    );
    return null;
  }

  const body = source.slice(open, end + 1);
  const table = {};
  let pending = [];
  let sawDefault = false;

  // Comments carry mode names in prose ("a reservation in `or` or `and`"), and a naive scan
  // would read those as cases. Stripped first.
  const code = body.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');

  const token =
    /case\s+'([a-z]+)'\s*:|default\s*:|return\s*\{\s*passcode:\s*(true|false)\s*,\s*whitelist:\s*(true|false)\s*\}/g;
  let m;
  while ((m = token.exec(code)) !== null) {
    if (m[1]) {
      pending.push(m[1]);
    } else if (m[0].startsWith('default')) {
      sawDefault = true;
    } else {
      const pair = { passcode: m[2] === 'true', whitelist: m[3] === 'true' };
      if (sawDefault) {
        // The default arm covers public in every implementation; record it explicitly so a
        // surface that dropped the named case is still compared.
        table.public = table.public || pair;
        sawDefault = false;
      }
      pending.forEach((mode) => {
        table[mode] = pair;
      });
      pending = [];
    }
  }
  return table;
}

for (const [label, rel] of SOURCES) {
  const file = path.join(ROOT, rel);
  if (!fs.existsSync(file)) {
    failures.push(`${label}: ${rel} does not exist`);
    continue;
  }
  const table = tableFrom(label, fs.readFileSync(file, 'utf8'));
  if (!table) continue;

  for (const [mode, want] of Object.entries(EXPECTED)) {
    const got = table[mode];
    if (!got) {
      failures.push(
        `${label}: mode '${mode}' is not handled. A mode the other surfaces offer and this one ` +
          `does not is a capability gap, not a styling difference -- that is exactly how V2 came ` +
          `to have no way to express 'or' or 'and'.`,
      );
      continue;
    }
    if (got.passcode !== want.passcode || got.whitelist !== want.whitelist) {
      failures.push(
        `${label}: mode '${mode}' enables passcode=${got.passcode} whitelist=${got.whitelist}, ` +
          `expected passcode=${want.passcode} whitelist=${want.whitelist}`,
      );
    }
  }
}

// The modes each surface actually OFFERS, which is a different question from the table above.
//
// V2's defect was never in a table -- it was that its radio list had three entries while the
// two selects had five. A gate that only compared the field mapping would have passed that
// clean, because the mapping for a mode you cannot select is never wrong. So this reads the
// controls a user actually touches.
const OFFERED = [
  ['Inspector', 'pkg/client/dashboard.html', /id="ac-mode"[\s\S]*?<\/select>/],
  [
    'Portal V1',
    'pkg/server/dashboard.html',
    /id="reservation-ac-mode"[\s\S]*?<\/select>/,
  ],
  // V2 builds radios from an array of [value, icon, label] tuples rather than markup.
  [
    'Portal V2',
    'ui/src/components/ReservationsPanel.tsx',
    /role="radiogroup"[\s\S]*?\] as \[string, string, string\]\[\]/,
  ],
];

for (const [label, rel, region] of OFFERED) {
  const file = path.join(ROOT, rel);
  if (!fs.existsSync(file)) {
    failures.push(`${label}: ${rel} does not exist`);
    continue;
  }
  const source = fs.readFileSync(file, 'utf8');
  const block = source.match(region);
  if (!block) {
    failures.push(
      `${label}: could not locate the access-mode control in ${rel}. Failing rather than ` +
        `assuming it is fine -- "not found" and "agrees" must never look the same.`,
    );
    continue;
  }
  const text = block[0];
  for (const mode of Object.keys(EXPECTED)) {
    const offered =
      new RegExp(`value="${mode}"`).test(text) ||
      new RegExp(`'${mode}'`).test(text);
    if (!offered) {
      failures.push(
        `${label}: does not offer the '${mode}' mode. A mode a user cannot select in one arm ` +
          `and can in another is a capability gap -- V2 shipped without 'or' and 'and', so a ` +
          `reservation in either opened with no option selected and no fields at all.`,
      );
    }
  }
}

// The server is the one that actually refuses a save, so its rule has to agree with the forms.
// Read as text for the same reason as above.
const serverFile = path.join(ROOT, 'pkg/server/api_service_reservation.go');
if (!fs.existsSync(serverFile)) {
  failures.push('server: pkg/server/api_service_reservation.go does not exist');
} else {
  const src = fs.readFileSync(serverFile, 'utf8');
  if (!/func missingAccessControlValue\(/.test(src)) {
    failures.push(
      'server: missingAccessControlValue is gone. Without it the forms are the only thing ' +
        'stopping a mode that names a factor the tunnel does not have, and three forms are ' +
        'not a guarantee (#2156).',
    );
  }
  // Every mode the forms let you pick both factors for must be refused when one is absent.
  for (const mode of ['and', 'passcode', 'whitelist']) {
    if (!new RegExp(`case "${mode}":`).test(src)) {
      failures.push(
        `server: missingAccessControlValue does not validate mode '${mode}'`,
      );
    }
  }
  // ...and 'or' must NOT be, because an unset mode defaults to it and every unconfigured
  // reservation would otherwise become unsaveable.
  if (/case "or":/.test(src)) {
    failures.push(
      "server: missingAccessControlValue validates 'or'. An unset mode defaults to it, so " +
        'every reservation nobody has configured would stop saving.',
    );
  }
}

if (failures.length > 0) {
  console.error('check-access-mode-parity: FAILED\n');
  failures.forEach((f) => console.error(`  - ${f}`));
  console.error(
    '\n  One rule, four implementations. Update all of them, or the arms drift apart again.',
  );
  process.exit(1);
}

console.log(
  `check-access-mode-parity: OK -- ${SOURCES.length} form(s) and the server agree on all ` +
    `${Object.keys(EXPECTED).length} access modes, and each offers every one of them.`,
);
