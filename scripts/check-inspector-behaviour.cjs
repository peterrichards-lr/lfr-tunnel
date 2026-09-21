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

// A field the user is editing must survive the state poll.
//
// The poll runs every 1500ms and used to rewrite every Access Control input whose element was not
// document.activeElement. Tab from Passcode Protection to Confirm passcode and the passcode field
// is no longer focused, so the next tick wiped it -- with '', because the engine's access control
// is a local-config value and a hardcoded default no gateway populates (#2130). The owner could
// not save a passcode at all (#2131).
//
// Asserted by RUNNING setUnlessEdited rather than reading it, because the bug was never in the
// words: the old code said `if (document.activeElement !== el)`, which is perfectly sensible and
// protects exactly one field.
{
  const src = extractFunction(page, 'function setUnlessEdited(');
  // Same mechanism annotateSettings is exercised with: build it as a real function and call it.
  const doc = { activeElement: null };
  const setUnlessEdited = new Function(
    'document',
    src + '\nreturn setUnlessEdited;',
  )(doc);

  const field = (edited) => ({
    value: 'what-the-user-typed',
    dataset: edited ? { userEdited: '1' } : {},
  });

  const dirty = field(true);
  setUnlessEdited(dirty, '');
  check(
    'a poll tick cannot wipe a field the user has edited',
    dirty.value === 'what-the-user-typed',
    `the poll overwrote it with ${JSON.stringify(dirty.value)}; 1500ms after leaving the field, ` +
      'everything typed was gone',
  );

  const untouched = field(false);
  const wrote = setUnlessEdited(untouched, 'from-the-gateway');
  check(
    'an untouched field still follows the gateway',
    untouched.value === 'from-the-gateway' && wrote === true,
    'refusing to ever repopulate would freeze the panel at whatever was last typed',
  );

  const focused = field(false);
  doc.activeElement = focused;
  setUnlessEdited(focused, 'clobber');
  check(
    'the focused field is still protected',
    focused.value === 'what-the-user-typed',
    'the original guard was not wrong, only insufficient -- it must not be lost',
  );
  doc.activeElement = null;
}

// Every Access Control input must be watched, not just the one that was reported.
//
// The owner reported the passcode field. Confirm passcode survived only because it was added
// later and was never wired into the poll; whitelist and mode had the identical bug. A fix
// covering only the reported symptom would leave most of the defect in place.
{
  // Scoped to the function BODY. The first version of this asked whether the id appeared
  // anywhere on the page and whether a regex matched across it -- both stayed true with
  // ac-whitelist deleted from the watch list, so the check passed against the defect. Caught by
  // running the control; a guard that cannot go red is not a guard.
  const watchBody = extractFunction(page, 'function markAccessControlEdited(');
  const clearBody = extractFunction(page, 'function clearAccessControlEdited(');
  for (const id of [
    'ac-passcode',
    'ac-passcode-confirm',
    'ac-whitelist',
    'ac-mode',
  ]) {
    check(
      `${id} is watched for edits`,
      watchBody.includes(`'${id}'`),
      'a field nothing watches is a field the poll is free to wipe 1500ms later',
    );
    check(
      `${id} is released again after a save`,
      clearBody.includes(`'${id}'`),
      'a field marked edited and never cleared freezes at whatever was last typed',
    );
  }
  // Located by SLICING the branch rather than by a proximity regex: a character window is a
  // guess about formatting, and the first version of this check failed on a comment.
  // Anchored inside saveAccessControl: the page has several `if (res.ok) {`, and the first one
  // belongs to a different handler entirely.
  const saveAt = page.indexOf('async function saveAccessControl(');
  const okAt = page.indexOf('if (res.ok) {', saveAt);
  const elseAt = page.indexOf('} else {', okAt);
  const successBranch =
    saveAt >= 0 && okAt > saveAt && elseAt > okAt
      ? page.slice(okAt, elseAt)
      : '';
  // The definition line matches the bare name too, so calls are counted by excluding it.
  const calls = (
    page.match(/(?<!function )clearAccessControlEdited\(\)/g) || []
  ).length;
  check(
    'the edited flags are cleared only after a successful save',
    successBranch.includes('clearAccessControlEdited()') && calls === 1,
    'clearing them on a REJECTED save discards what the user typed, exactly when they are most ' +
      'likely to have typed something long',
  );
}

// The Settings groups must be SIBLINGS, and the template that draws them must balance.
//
// #2112 split "Applies when the client restarts" from "Applies immediately" because it was not
// intuitive which settings needed one. They shipped NESTED -- the live group was a child of the
// restart group -- so the markup said the opposite of the intent: restart-required settings, and
// inside them, a few that apply at once (#2119). The template also left two divs unclosed, which
// a browser repairs in silence.
//
// No text guard can catch this. Every string one would grep for is present and correct; only the
// shape of the tree is wrong. So this parses.
{
  const template = (() => {
    const start = page.indexOf('async function renderSettingsView()');
    const marker = page.indexOf('main.innerHTML = `', start);
    const from = marker + 'main.innerHTML = `'.length;
    return page.slice(from, page.indexOf('`;', from));
  })();

  const VOID = new Set(['input', 'img', 'br', 'hr', 'meta', 'link']);
  const stack = [];
  const depths = [];
  const tagRe = /<(\/?)([a-zA-Z0-9]+)([^>]*)>/g;
  let m;
  while ((m = tagRe.exec(template)) !== null) {
    const [, closing, tag, attrs] = m;
    const name = tag.toLowerCase();
    if (VOID.has(name) || attrs.endsWith('/')) continue;
    if (closing) {
      for (let i = stack.length - 1; i >= 0; i -= 1) {
        if (stack[i].tag === name) {
          stack.length = i;
          break;
        }
      }
      continue;
    }
    // A settings GROUP is the bordered full-width box carrying one of the two headings.
    const isGroup = name === 'div' && attrs.includes('grid-column: 1 / -1');
    stack.push({ tag: name, group: isGroup });
    if (isGroup) depths.push(stack.filter((e) => e.group).length);
  }

  check(
    'both Settings groups exist',
    depths.length === 2,
    `found ${depths.length} group box(es); the split in #2112 needs exactly two`,
  );
  check(
    'the Settings groups are siblings, not nested',
    depths.every((d) => d === 1),
    'a group inside another group tells the reader that the inner settings are a SUBSET of the ' +
      'outer ones -- the opposite of what splitting them was for',
  );
  check(
    'the settings template closes every tag it opens',
    stack.length === 0,
    `left open: ${stack.map((e) => e.tag).join(', ')} -- the browser repairs this silently, so ` +
      'the only symptom is a layout nobody can explain',
  );
}

console.log(
  failures === 0
    ? `✅ ${checks} behavioural check(s) passed against the real page.`
    : `\n❌ ${failures} of ${checks} behavioural check(s) failed.`,
);
process.exit(failures === 0 ? 0 : 1);
