#!/usr/bin/env node
/**
 * check-status-vocabulary.cjs -- the portal's user statuses must be exactly the server's (#1851).
 *
 * The vocabulary lived in six places and was checked in none: ~160 bare literals in pkg/server,
 * two unexported constants, the statusOptions array in AdminUsers.tsx, two badge ternaries with
 * OPPOSITE defaults, portal V1's filters in dashboard.js, and ten Language*.properties bundles.
 *
 * Adding one cost an edit in every place that happened to know, and misses were silent.
 *
 * It covered only V2 at first, and #1866 is what that cost: V1 kept its own badge ternary -- three
 * copies of it, no two alike -- so the same rejected user rendered red in one arm of the A/B test
 * and amber in the other. Binding V2 to the server while leaving V1 unbound just moved the drift.
 *
 * #1847 is the proof: #1830 added "rejected", the portal did not learn it, and a rejected user
 * could not be filtered for or un-rejected -- the control that reverses a rejection. Nothing
 * failed; a human asked.
 *
 * So this compares rather than documents, which is the same reason check-i18n-keys.cjs exists:
 * an inline fallback made a missing key invisible, structurally the same failure as a status the
 * portal does not know.
 *
 * Fails closed. If either side parses to nothing the check exits 1 rather than reporting that two
 * empty sets agree -- a gate that cannot fail is worse than none, and this repo has found five of
 * those (#1779).
 */

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');
const rel = (p) => path.relative(REPO, p);

const GO_SOURCE = path.join(REPO, 'pkg/db/user_status.go');
const UI_SOURCE = path.join(REPO, 'ui/src/pages/AdminUsers.tsx');
const V1_SOURCE = path.join(REPO, 'pkg/server/static/dashboard.js');

let failed = false;
const fail = (msg) => {
  console.error(`  ${msg}`);
  failed = true;
};

for (const f of [GO_SOURCE, UI_SOURCE, V1_SOURCE]) {
  if (!fs.existsSync(f)) {
    console.error(`check-status-vocabulary: ${rel(f)} not found.`);
    console.error(
      '  If it moved, move this check with it rather than deleting the check.',
    );
    process.exit(1);
  }
}

// -- The server's set, read from the UserStatuses slice rather than from the const block: the
//    slice is what declares membership, and a constant that is never added to it is precisely the
//    kind of half-added status this exists to catch.
const goSrc = fs.readFileSync(GO_SOURCE, 'utf8');
const sliceMatch = goSrc.match(/var UserStatuses = \[\]string\{([^}]*)\}/);
if (!sliceMatch) {
  console.error(
    `check-status-vocabulary: could not find UserStatuses in ${rel(GO_SOURCE)}.`,
  );
  process.exit(1);
}

const constValues = new Map();
for (const m of goSrc.matchAll(/\bUserStatus([A-Za-z]+)\s*=\s*"([a-z_]+)"/g)) {
  constValues.set(`UserStatus${m[1]}`, m[2]);
}

const serverStatuses = sliceMatch[1]
  .split(',')
  .map((s) => s.trim())
  .filter(Boolean)
  .map((name) => constValues.get(name) ?? name);

// -- The portal's two enumerations: the dropdown/filter list, and the badge mapping.
const uiSrc = fs.readFileSync(UI_SOURCE, 'utf8');

const optionsMatch = uiSrc.match(
  /const statusOptions = useMemo\(\s*\(\)\s*=>\s*\[([\s\S]*?)\],/,
);
if (!optionsMatch) {
  console.error(
    `check-status-vocabulary: could not find statusOptions in ${rel(UI_SOURCE)}.`,
  );
  process.exit(1);
}
const uiStatuses = [...optionsMatch[1].matchAll(/value:\s*'([a-z_]+)'/g)].map(
  (m) => m[1],
);

const badgeMatch = uiSrc.match(
  /const STATUS_BADGE: Record<string, string> = \{([\s\S]*?)\};/,
);
if (!badgeMatch) {
  console.error(
    `check-status-vocabulary: could not find STATUS_BADGE in ${rel(UI_SOURCE)}.`,
  );
  process.exit(1);
}
const badgeStatuses = [...badgeMatch[1].matchAll(/^\s*([a-z_]+):/gm)].map(
  (m) => m[1],
);

// -- Portal V1's badge mapping. Same shape, plain JS, no type annotation.
const v1Src = fs.readFileSync(V1_SOURCE, 'utf8');
const v1BadgeMatch = v1Src.match(/const STATUS_BADGE = \{([\s\S]*?)\};/);
if (!v1BadgeMatch) {
  console.error(
    `check-status-vocabulary: could not find STATUS_BADGE in ${rel(V1_SOURCE)}.`,
  );
  console.error(
    '  If V1 went back to deciding the colour inline, that is the defect #1866 fixed.',
  );
  process.exit(1);
}
const v1BadgeStatuses = [...v1BadgeMatch[1].matchAll(/^\s*([a-z_]+):/gm)].map(
  (m) => m[1],
);

// -- Anti-vacuity. Two empty sets agree perfectly.
if (
  serverStatuses.length === 0 ||
  uiStatuses.length === 0 ||
  badgeStatuses.length === 0 ||
  v1BadgeStatuses.length === 0
) {
  console.error(
    'check-status-vocabulary: one of the four lists parsed to nothing.',
  );
  console.error(
    `  server=${serverStatuses.length} statusOptions=${uiStatuses.length} STATUS_BADGE=${badgeStatuses.length} V1 STATUS_BADGE=${v1BadgeStatuses.length}`,
  );
  console.error('  Refusing to report that empty sets agree.');
  process.exit(1);
}

console.log(
  `Checking the user status vocabulary (${serverStatuses.length} statuses in ${rel(GO_SOURCE)})...`,
);

const compare = (label, actual) => {
  const missing = serverStatuses.filter((s) => !actual.includes(s));
  const extra = actual.filter((s) => !serverStatuses.includes(s));
  if (missing.length) {
    fail(`${label} is missing: ${missing.join(', ')}`);
    fail(
      `  a status the portal does not know cannot be filtered for, or changed to (#1847).`,
    );
  }
  if (extra.length) {
    fail(
      `${label} has statuses the server does not define: ${extra.join(', ')}`,
    );
  }
};

compare('statusOptions', uiStatuses);
compare('STATUS_BADGE (V2)', badgeStatuses);
compare('STATUS_BADGE (V1)', v1BadgeStatuses);

if (failed) {
  console.error('');
  console.error(`STATUS VOCABULARY CHECK FAILED`);
  console.error(`  server (${rel(GO_SOURCE)}): ${serverStatuses.join(', ')}`);
  console.error(`  statusOptions:              ${uiStatuses.join(', ')}`);
  console.error(`  STATUS_BADGE (V2):          ${badgeStatuses.join(', ')}`);
  console.error(`  STATUS_BADGE (V1):          ${v1BadgeStatuses.join(', ')}`);
  console.error('');
  console.error(
    '  Add the status to pkg/db/user_status.go first, then to all three portal lists.',
  );
  process.exit(1);
}

console.log(
  `OK -- statusOptions and both portals' STATUS_BADGE match: ${serverStatuses.join(', ')}`,
);
