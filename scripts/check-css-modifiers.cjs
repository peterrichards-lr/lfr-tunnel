#!/usr/bin/env node
/**
 * Fails when a BEM modifier class used in the Portal V2 source has no rule in the
 * built-from-source CSS.
 *
 * Narrow on purpose. #1383 found ~150 class names in ui/src that style nothing, most
 * of them Tailwind utilities left inert because Tailwind is a dependency that never
 * runs. Driving that whole set to zero is staged work; gating on it today would just
 * fail every build.
 *
 * A BEM modifier is different, and worth gating separately: `foo--bar` where `.foo`
 * IS defined is unambiguously a typo or an unfinished rule, never a deliberate
 * Tailwind class. That is exactly how these three shipped --
 *
 *   .status-dot--warning     a connecting tunnel looked identical to a healthy one
 *   .alert-banner--success   "saved" and "failed" rendered the same
 *   .modal-card--sm/md/lg    six modals asked for a size, all got the base width
 *
 * -- and each looked deliberate in review because the base class was right there.
 *
 * The broader used-vs-defined gate in #1383's acceptance criteria supersedes this
 * once the inert count reaches zero.
 */
'use strict';
const fs = require('fs');
const path = require('path');

const UI_SRC = path.join(__dirname, '..', 'ui', 'src');

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

if (!fs.existsSync(UI_SRC)) {
  console.error(`check-css-modifiers: ${UI_SRC} not found`);
  process.exit(1);
}

const files = walk(UI_SRC);

// Every class selector defined anywhere in the V2 stylesheets, comments stripped so a
// commented-out rule does not count as defined.
const defined = new Set();
for (const f of files.filter((f) => f.endsWith('.css'))) {
  const css = fs.readFileSync(f, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
  for (const m of css.matchAll(/\.(-?[A-Za-z_][-\w]*)/g)) defined.add(m[1]);
}

// Every BEM modifier token appearing in the source, with where it came from.
//
// Scanning every line rather than only lines containing `className` is deliberate. The
// first version of this check filtered on that and missed .alert-banner--success
// outright, because it sits in a multi-line ternary whose `className` is three lines
// above the string. Class names reach the DOM through template literals, ternaries,
// helper functions and variables, so no single-line anchor finds them all.
//
// What keeps that from over-matching is the `defined.has(base)` test below: a token only
// counts if its base is a class this stylesheet already owns, which no ordinary
// identifier is.
const used = new Map();
const MODIFIER = /\b([A-Za-z][-\w]*?)--([A-Za-z][-\w]*)\b/g;
for (const f of files.filter((f) => /\.tsx?$/.test(f))) {
  const src = fs.readFileSync(f, 'utf8');
  const lines = src.split('\n');
  lines.forEach((line, i) => {
    for (const m of line.matchAll(MODIFIER)) {
      const [full, base] = m;
      if (!defined.has(base)) continue; // not a BEM family this sheet owns
      if (defined.has(full)) continue;
      const rel = path.relative(path.join(__dirname, '..'), f);
      if (!used.has(full)) used.set(full, []);
      used.get(full).push(`${rel}:${i + 1}`);
    }
  });
}

if (used.size === 0) {
  console.log(
    'check-css-modifiers: OK -- every BEM modifier used in ui/src has a rule',
  );
  process.exit(0);
}

console.error(
  'check-css-modifiers: BEM modifier classes used but never defined\n',
);
for (const [cls, where] of [...used.entries()].sort()) {
  console.error(`  .${cls}`);
  for (const w of where) console.error(`      ${w}`);
  const base = cls.split('--')[0];
  const siblings = [...defined].filter((d) => d.startsWith(base + '--')).sort();
  if (siblings.length)
    console.error(
      `      defined siblings: ${siblings.map((s) => '.' + s).join(', ')}`,
    );
  console.error('');
}
console.error(
  `${used.size} undefined modifier(s). Add the rule to ui/src/index.css, or`,
);
console.error(
  'correct the class name to one of the defined siblings listed above.',
);
process.exit(1);
