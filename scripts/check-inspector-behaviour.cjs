#!/usr/bin/env node
/**
 * Execute functions from the client inspector page and assert what they DO.
 *
 * Every other guard on dashboard.html reads it as text, and text cannot throw. That is not a
 * small gap: #2089 called annotateSettings with `data.launch_overrides` where the response is
 * `cfg` and no `data` exists, so the whole Settings tab failed to load -- and all five text
 * guards passed. The try/catch swallowed the ReferenceError into "Failed to load
 * configuration", leaving blank fields as the only symptom.
 *
 * Playwright caught it. Twice. At roughly sixteen minutes a cycle, in Docker, on CI (#2094).
 *
 * This is the missing middle: pull a function out of the page, run it against a DOM stub, and
 * check the result. Under a second, no browser, no containers. It does NOT replace the E2E
 * suite -- it catches the class that reaches CI precisely because the cheap guards cannot run
 * the code.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const PAGE = path.join(__dirname, '..', 'pkg', 'client', 'dashboard.html');

let failures = 0;
let checks = 0;

function check(name, condition, detail) {
  checks += 1;
  if (!condition) {
    failures += 1;
    console.log(`  ✗ ${name}`);
    if (detail) console.log(`      ${detail}`);
  }
}

/** Extract a top-level function from the page by name, up to the next one. */
function extractFunction(page, signature) {
  const start = page.indexOf(signature);
  if (start < 0) {
    throw new Error(
      `${signature} not found in dashboard.html -- this gate is asserting about code that no ` +
        `longer exists, which is worse than not checking at all`,
    );
  }
  const rest = page.slice(start);
  const next = rest.slice(30).search(/\n {8}(async )?function /);
  return next > 0 ? rest.slice(0, next + 30) : rest;
}

/** A DOM stub with only what these functions touch. */
function makeElement(id) {
  const isCheckbox = id.includes('preserve') || id.includes('insecure');
  const parent = {
    children: [],
    appendChild(node) {
      this.children.push(node);
    },
    querySelectorAll(selector) {
      const wanted = selector.replace('.', '');
      return this.children.filter((c) => c.className === wanted);
    },
  };
  return {
    id,
    type: isCheckbox ? 'checkbox' : 'text',
    value: '',
    checked: false,
    readOnly: false,
    disabled: false,
    style: {},
    parentElement: parent,
  };
}

const FIELD_IDS = [
  'cfg-server-url',
  'cfg-auth-token',
  'cfg-dest-port',
  'cfg-target-host',
  'cfg-subdomain',
  'cfg-preserve-host',
  'cfg-insecure-skip-verify',
  'cfg-log-dir',
];

function runAnnotate(page, saved, effective, launchOverrides, canRestart) {
  const elements = {};
  FIELD_IDS.forEach((id) => {
    elements[id] = makeElement(id);
  });

  const sandbox = {
    document: {
      getElementById: (id) => elements[id] || null,
      createElement: () => ({
        style: { cssText: '' },
        className: '',
        textContent: '',
      }),
    },
    t: (key) => key,
  };

  const source = extractFunction(page, 'function annotateSettings(');
  // `note` lives beside it and is called by it.
  const noteSource = extractFunction(page, 'function note(');

  // restartNeededFields / restartAvailable are module-level in the page; declare them here so
  // the function under test assigns to something real.
  const body = `let restartNeededFields = []; let restartAvailable = false;
${source}
${noteSource}
annotateSettings(saved, effective, launchOverrides, canRestart);
return {restartNeededFields, restartAvailable};`;

  const fn = new Function(
    'document',
    't',
    'saved',
    'effective',
    'launchOverrides',
    'canRestart',
    body,
  );
  const out = fn(
    sandbox.document,
    sandbox.t,
    saved,
    effective,
    launchOverrides,
    canRestart,
  );
  return { elements, ...out };
}

const page = fs.readFileSync(PAGE, 'utf8');

console.log('Executing inspector functions against a DOM stub...\n');

// The E2E container exactly: launched with -server, no config file at all.
{
  let result;
  try {
    result = runAnnotate(
      page,
      {},
      {
        server_url: 'http://tunnel.lfr-demo.local',
        subdomain: 'client-ui-test',
      },
      { server_url: '-server' },
      true,
    );
  } catch (err) {
    console.log(`  ✗ annotateSettings threw: ${err.message}`);
    console.log(
      '      A runtime error here blanks the entire Settings tab, because the caller',
    );
    console.log('      swallows it into "Failed to load configuration".');
    process.exit(1);
  }

  const serverUrl = result.elements['cfg-server-url'];
  check(
    'a launch-claimed field shows the RUNNING value',
    serverUrl.value === 'http://tunnel.lfr-demo.local',
    `got ${JSON.stringify(serverUrl.value)}. A client started from flags has no config file, so ` +
      `showing the saved value leaves an empty box while the tunnel serves traffic (#1211).`,
  );
  check(
    'a launch-claimed field is not editable',
    serverUrl.readOnly === true,
    'a restart reuses the same argv, so editing it could never take effect (#2088)',
  );
  check(
    'a launch-claimed field says what claimed it',
    serverUrl.parentElement.children.length === 1,
    'locked with no explanation is worse than leaving it editable',
  );

  const subdomain = result.elements['cfg-subdomain'];
  check(
    'an unclaimed field keeps the saved value',
    subdomain.value === '',
    `got ${JSON.stringify(subdomain.value)}. The box is what a save writes; showing the running ` +
      `value is what made edits appear to revert (#2088).`,
  );
  check(
    'an unclaimed field whose running value differs gets a note',
    subdomain.parentElement.children.length === 1,
    'otherwise nothing tells the user which value is actually in force',
  );
  check(
    'claimed fields are excluded from the restart set',
    !result.restartNeededFields.includes('server_url') &&
      result.restartNeededFields.includes('subdomain'),
    `got ${JSON.stringify(result.restartNeededFields)}. Offering to restart for a field a flag ` +
      `owns would restart into the same value.`,
  );
}

// Nothing claimed, saved and running agree: no notes, everything editable.
{
  const result = runAnnotate(
    page,
    { server_url: 'https://a.example', subdomain: 'sub' },
    { server_url: 'https://a.example', subdomain: 'sub' },
    {},
    true,
  );
  const serverUrl = result.elements['cfg-server-url'];
  check(
    'nothing is locked when no flag claimed it',
    serverUrl.readOnly === false,
    'a field the user can change must not be read-only',
  );
  check(
    'no note when the saved and running values agree',
    serverUrl.parentElement.children.length === 0,
    'a note that always appears is one nobody reads',
  );
}

console.log(
  failures === 0
    ? `✅ ${checks} behavioural check(s) passed against the real page.`
    : `\n❌ ${failures} of ${checks} behavioural check(s) failed.`,
);
process.exit(failures === 0 ? 0 : 1);
