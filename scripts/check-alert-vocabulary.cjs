#!/usr/bin/env node
/**
 * check-alert-vocabulary.cjs -- every admin alert must be switchable off (#1882).
 *
 * Six alert_notify_* keys existed and the settings endpoint exposed three, so three alerts could
 * not be turned off at all except by writing the admin_settings row by hand. An owner who found
 * watchdog or vanity-hook alerts noisy had no way to stop them, and nothing said the setting
 * existed.
 *
 * The list lived in three places that had to agree -- the sendAdminAlert call sites, the settings
 * endpoint, and the portal forms -- so adding an alert meant editing all three, and missing one
 * was silent. #1875 added alert_notify_watchdog_restart following the broken pattern rather than
 * fixing it, which is the proof the shape repeats.
 *
 * So this compares rather than documents, the same reasoning as check-status-vocabulary.cjs:
 * every key raised by sendAdminAlert must be declared in AlertSettings, and every declared key
 * must carry a label both portals can render.
 *
 * Fails closed. If either side parses to nothing the run exits 1 rather than reporting that two
 * empty sets agree -- a gate that cannot fail is worse than none (#1779).
 */

const fs = require('fs');
const path = require('path');

const REPO = path.join(__dirname, '..');
const rel = (p) => path.relative(REPO, p);

const TABLE = path.join(REPO, 'pkg/server/alert_settings.go');
const EN = path.join(REPO, 'pkg/server/i18n/Language.properties');

let failed = false;
const fail = (m) => {
  console.error(`  ${m}`);
  failed = true;
};

for (const f of [TABLE, EN]) {
  if (!fs.existsSync(f)) {
    console.error(`check-alert-vocabulary: ${rel(f)} not found.`);
    console.error(
      '  If it moved, move this check with it rather than deleting the check.',
    );
    process.exit(1);
  }
}

// -- What the gateway declares.
const tableSrc = fs.readFileSync(TABLE, 'utf8');
const declared = [
  ...tableSrc.matchAll(
    /Key:\s*"(alert_notify_[a-z_]+)",\s*LabelKey:\s*"([a-z_]+)"/g,
  ),
].map((m) => ({ key: m[1], labelKey: m[2] }));

// -- What the code actually raises. Read from the call sites rather than from a list, because a
//    list of call sites is the thing that goes stale.
const goFiles = fs
  .readdirSync(path.join(REPO, 'pkg/server'))
  .filter((f) => f.endsWith('.go') && !f.endsWith('_test.go'));
const raised = new Set();

// Some call sites pass a named constant rather than a literal -- the watchdog alert does
// (watchdog_spool.go). Resolving them matters: a gate that only saw literals would report a
// declared-but-never-raised alert and be confidently wrong about it.
const constants = new Map();
for (const f of goFiles) {
  const src = fs.readFileSync(path.join(REPO, 'pkg/server', f), 'utf8');
  for (const m of src.matchAll(/(\w+)\s*=\s*"(alert_notify_[a-z_]+)"/g)) {
    constants.set(m[1], m[2]);
  }
}

// Raised deliberately by a human pressing a button, so it has no toggle and needs none -- you
// cannot be spared a notification you just asked for. Listed rather than pattern-matched, so
// adding an exemption stays a visible decision.
const NO_TOGGLE_NEEDED = new Set(['alert_notify_test']);

for (const f of goFiles) {
  const src = fs.readFileSync(path.join(REPO, 'pkg/server', f), 'utf8');
  for (const m of src.matchAll(
    /send(?:Admin)?Alert\(\s*"(alert_notify_[a-z_]+)"/g,
  )) {
    if (!NO_TOGGLE_NEEDED.has(m[1])) raised.add(m[1]);
  }
  for (const m of src.matchAll(/send(?:Admin)?Alert\(\s*(\w+)\s*,/g)) {
    const resolved = constants.get(m[1]);
    if (resolved && !NO_TOGGLE_NEEDED.has(resolved)) raised.add(resolved);
  }
}

// -- Anti-vacuity. Two empty sets agree perfectly.
if (declared.length === 0 || raised.size === 0) {
  console.error(
    'check-alert-vocabulary: one of the two lists parsed to nothing.',
  );
  console.error(`  declared=${declared.length} raised=${raised.size}`);
  console.error('  Refusing to report that empty sets agree.');
  process.exit(1);
}

console.log(
  `Checking the admin alert vocabulary (${declared.length} declared in ${rel(TABLE)})...`,
);

const declaredKeys = declared.map((d) => d.key);

for (const key of raised) {
  if (!declaredKeys.includes(key)) {
    fail(
      `${key} is raised by sendAdminAlert but not declared in AlertSettings.`,
    );
    fail(
      '  An undeclared alert cannot be switched off from System Settings, which is',
    );
    fail('  exactly the state #1882 found three keys in.');
  }
}

for (const key of declaredKeys) {
  if (!raised.has(key)) {
    fail(`${key} is declared in AlertSettings but nothing raises it.`);
    fail(
      '  A toggle for an alert that never fires is a control that does nothing.',
    );
  }
}

// -- Every declared alert needs a label, or the portals render a raw key.
const enSrc = fs.readFileSync(EN, 'utf8');
for (const d of declared) {
  if (!new RegExp(`^${d.labelKey}=`, 'm').test(enSrc)) {
    fail(
      `${d.key} declares label "${d.labelKey}", which ${rel(EN)} does not define.`,
    );
  }
}

// -- Both portal arms must render the table rather than a copy of it.
//
// There is deliberately nothing per-key to grep for here: the portals build their checkboxes
// from the alert_settings the endpoint returns, which is the property that stops the drift.
// So the gate asserts that wiring instead -- each arm fetches /api/admin/settings AND reads
// alert_settings off the response. An arm that reverts to naming keys inline, or drops the
// section, stops matching and fails, which is the #1866 parity rule applied to this surface.
const ARMS = [
  { name: 'V1', file: path.join(REPO, 'pkg/server/static/dashboard.js') },
  { name: 'V2', file: path.join(REPO, 'ui/src/pages/AdminSettings.tsx') },
];

for (const arm of ARMS) {
  if (!fs.existsSync(arm.file)) {
    fail(`${arm.name}: ${rel(arm.file)} not found.`);
    continue;
  }
  const src = fs.readFileSync(arm.file, 'utf8');
  if (!src.includes('/api/admin/settings')) {
    fail(`${arm.name} (${rel(arm.file)}) never calls /api/admin/settings.`);
    fail(
      '  The alert toggles cannot be shown by an arm that never asks for them.',
    );
  }
  if (!src.includes('alert_settings')) {
    fail(
      `${arm.name} (${rel(arm.file)}) does not read alert_settings off the response.`,
    );
    fail(
      '  Render from the server-declared table; a list held in the portal is the drift.',
    );
  }
  // A hardcoded key is the exact regression this replaced, so name it rather than waiting
  // for the counts to disagree somewhere else.
  const inline = [...src.matchAll(/['"`](alert_notify_[a-z_]+)['"`]/g)].map(
    (m) => m[1],
  );
  if (inline.length) {
    fail(
      `${arm.name} (${rel(arm.file)}) names alert keys inline: ${[...new Set(inline)].join(', ')}.`,
    );
    fail(
      '  Build the controls from alert_settings so a new alert needs no portal edit.',
    );
  }
}

if (failed) {
  console.error('');
  console.error('ALERT VOCABULARY CHECK FAILED');
  console.error(`  declared: ${declaredKeys.join(', ')}`);
  console.error(`  raised:   ${[...raised].sort().join(', ')}`);
  console.error('');
  console.error(
    '  Add the alert to AlertSettings in pkg/server/alert_settings.go, give it a',
  );
  console.error(
    '  label in every locale bundle, and both portals will render it.',
  );
  process.exit(1);
}

console.log(
  `OK -- ${declaredKeys.length} alert(s) declared, raised, labelled and rendered in both arms: ${declaredKeys.join(', ')}`,
);
