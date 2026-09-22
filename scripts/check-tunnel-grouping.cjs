#!/usr/bin/env node
/**
 * Both portal arms must group a client's tunnels the same way.
 *
 * A client that maps several ports gets one lease per port -- Registry.Register gives the first
 * the bare subdomain and every one after it "<subdomain>-<localPort>". So a three-port client,
 * the normal shape for a Liferay setup with client extensions, occupied three rows repeating
 * everything that describes the CLIENT rather than the port (#2129).
 *
 * V1 and V2 render the SAME payload -- handleTelemetryPayload assigns the socket's frame to
 * currentUser, and V2's AdminTelemetry reads telemetryData.tunnels -- so a grouping that differs
 * between them is two answers to one question, which is what the A/B rule forbids.
 *
 * This asserts the CLASS: both arms group on the same key, neither groups on the session token,
 * and neither repeats per-client fields onto a child row.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..');
const V1 = fs.readFileSync(
  path.join(ROOT, 'pkg/server/static/dashboard.js'),
  'utf8',
);
const V2 = fs.readFileSync(
  path.join(ROOT, 'ui/src/pages/AdminTelemetry.tsx'),
  'utf8',
);
// V2's Dashboard renders the same clients in its own panel and was missed by #2129 -- an arm
// difference, since V1's Active Tunnels groups and this is its counterpart (#2143).
const V2DASH = fs.readFileSync(
  path.join(ROOT, 'ui/src/components/TunnelsPanel.tsx'),
  'utf8',
);
const V1CSS = fs.readFileSync(
  path.join(ROOT, 'pkg/server/static/dashboard.css'),
  'utf8',
);
const V2CSS = fs.readFileSync(path.join(ROOT, 'ui/src/index.css'), 'utf8');

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

// The grouping key. Both arms must use all three parts: the prefix alone would merge two users
// who happen to share it, since the same prefix can exist on different domains.
for (const [arm, src] of [
  ['V1', V1],
  ['V2', V2],
]) {
  for (const part of ['user_id', 'subdomain_prefix', 'node_id']) {
    check(
      `${arm} groups on ${part}`,
      new RegExp(`${part}[^\\n]*\\|`).test(src) || src.includes(`\${t.${part}`),
      'the prefix alone merges two users who share it; the node keeps an edge and central apart',
    );
  }
}

// The session token is a credential: /api/deregister authenticates on nothing else (#2137).
// It would have been the obvious grouping key, and must never be one.
for (const [arm, src] of [
  ['V1', V1],
  ['V2', V2],
]) {
  check(
    `${arm} does not group on the session token`,
    !/session_token/.test(src),
    'grouping on it would require sending it to the browser, handing every viewer the means ' +
      'to drop any tunnel they can see',
  );
}

// A single-port client -- most of them -- must not gain a parent row and an expander that
// reveals nothing.
check(
  'V1 renders a single-port client as a plain row',
  // The CALL, with the paren. A bare substring also matches a renamed
  // tunnelGroupIsExpandableRenamed, so renaming the helper left this green -- found by
  // running the control.
  /tunnelGroupIsExpandable\(/.test(V1),
  'without this the common case gets a group of one, which repeats itself',
);
check(
  'V2 renders a single-port client as a plain row',
  V2.includes('members.length > 1'),
  'without this the common case gets a group of one, which repeats itself',
);

// Status describes the SESSION: the client dials every target port and reports "down" if any
// fails. Repeating it onto children reprints it onto rows it is not true of.
check(
  'V1 forces an unhealthy group open',
  /tunnelGroupIsOpen[\s\S]{0,400}!==\s*'up'/.test(V1),
  'collapsing a broken group hides the row someone opened the table to find',
);
check(
  'V2 forces an unhealthy group open',
  /unhealthy[\s\S]{0,200}!==\s*'up'/.test(V2) &&
    /isOpen[\s\S]{0,120}unhealthy/.test(V2),
  'collapsing a broken group hides the row someone opened the table to find',
);

// An empty prefix must never be a grouping value. Treating it as one collapsed every lease
// sharing a node into a single bogus group -- including unrelated clients on different IPs.
// Caught by telemetry_parity.spec.ts, whose fixture carries full_host and node_id only.
for (const [arm, src] of [
  ['V1', V1],
  ['V2', V2],
]) {
  check(
    `${arm} refuses to group a lease with no subdomain prefix`,
    /ungrouped:/.test(src),
    'with no prefix there is nothing to group BY, so each such lease must stand alone rather ' +
      'than joining everything else that happens to share a node',
  );
}

// The Dashboard panel groups too, and paginates GROUPS -- grouping only within a page would let
// one client's rows straddle a page boundary.
check(
  "V2's Dashboard panel groups a client's tunnels",
  /groupTunnels\(/.test(V2DASH) && /members\.length > 1/.test(V2DASH),
  'V1 Active Tunnels groups and this is its counterpart; one arm grouping and the other not is ' +
    'the difference the A/B rule forbids',
);
check(
  "V2's Dashboard panel paginates groups, not rows",
  /useDataTable<TunnelGroup>/.test(V2DASH),
  "grouping only the current page lets a client's rows split across a page boundary",
);
check(
  "V2's Dashboard panel refuses to group a lease with no prefix",
  /ungrouped:/.test(V2DASH),
  'an empty prefix is not a value to group ON',
);

// The expand/collapse control must sit INLINE, as a prefix on the name's line. V1's first
// attempt reused .action-menu-btn, which carries display:flex -- block-level, so the control
// took the whole row and pushed the subdomain beneath it (#2143).
for (const [arm, css] of [
  ['V1', V1CSS],
  ['V2', V2CSS],
]) {
  const at = css.indexOf('.tunnel-group-toggle {');
  const rule = at >= 0 ? css.slice(at, at + 400) : '';
  check(
    `${arm}'s expander is styled inline`,
    at >= 0 && /display:\s*inline;/.test(rule),
    'a flex or block control takes the whole row and drops the name onto the next line',
  );
}
for (const [arm, src] of [
  ['V1', V1],
  ['V2 Dashboard', V2DASH],
]) {
  check(
    `${arm} uses the inline expander class`,
    src.includes('tunnel-group-toggle'),
    'reusing .action-menu-btn, built for the round menu, is what put it on its own line',
  );
}

// Rows must be ordered, not merely rendered. The leases arrive from Go map iteration, which the
// runtime randomises on purpose, so every telemetry frame reshuffled the table with no sort key
// anyone could name (#2143). Asserted because a test that checks rows are PRESENT passes just as
// happily against a random order.
for (const [arm, src] of [
  ['V1', V1],
  ['V2', V2],
]) {
  check(
    `${arm} orders groups alphabetically`,
    /groups\.sort\(/.test(src) && /localeCompare/.test(src),
    'stable is not the same as meaningful; the owner asked for alphabetical',
  );
  check(
    `${arm} orders a group's members alphabetically`,
    /members\.sort\(/.test(src),
    'the child rows reshuffle for the same reason the parents did',
  );
}

// renderTable filters on Object.values(item), so a group must carry its member hosts or a
// search for a child host would match nothing.
check(
  'V1 keeps child hosts searchable',
  // BOTH halves: the field on the group and the accumulation of member hosts into it. Testing
  // for the name alone stayed true with the initialiser deleted, because the accumulation line
  // still mentioned it.
  /searchText:\s*''/.test(V1) && /\.searchText\s*\+=/.test(V1),
  "renderTable filters on Object.values(item); without this, searching 'dxplive-8222' finds " +
    'nothing, because that string lives only inside the members array',
);

console.log(
  failures === 0
    ? `✅ ${checks} grouping check(s) passed across both portal arms.`
    : `\n❌ ${failures} of ${checks} grouping check(s) failed.`,
);
process.exit(failures === 0 ? 0 : 1);
