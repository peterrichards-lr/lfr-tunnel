#!/usr/bin/env node
/**
 * Fails when a class used in the Portal V2 source has no rule anywhere in its CSS.
 *
 * This is the gate #1383 asked for. That issue found ~325 class occurrences that styled
 * nothing -- Tailwind was a dependency the build never ran, so anything Tailwind-shaped was
 * decoration. The count is now zero and the dependency is gone; this keeps it that way.
 * Without a check comparing classes used against rules present, it regrew invisibly once
 * and would again.
 *
 * It started narrower, gating only BEM modifiers whose base class was defined, because the
 * full set could not pass while those ~325 remained. That restriction is now lifted.
 *
 * FOUR PARSING RULES, each learned from a wrong answer this produced:
 *
 * 1. Class names come from string LITERALS inside className, not from the whole
 *    `className={...}` expression. Reading the expression body counts `selectedUser.role`
 *    and `toast.type` as classes -- which is how #1383's headline figure of 352 was reached
 *    when the real number was 325.
 *
 * 2. A component may define classes in its own inline <style> block.
 *    ClientInstallationModal does exactly that for code-box, copy-btn and
 *    animation-fade-in. Reading only .css files reported all thirteen occurrences as inert
 *    when they had been styled all along.
 *
 * 3. A CSS class name may contain escapes: `.hover\:opacity-80:hover` is ONE class, not
 *    `.hover` followed by a pseudo-class. A naive parser reads the prefix and reports a
 *    rule it has just been given as still missing.
 *
 * 4b. `className="a b"` IS already the literal. Applying rule 1's literal-extraction to it
 *    finds no quotes inside and skips the attribute entirely -- which silently ignored most
 *    classNames in the codebase and reported a clean pass over a page full of undefined
 *    classes. Quoted and braced regions are handled separately.
 *
 * 4. A className expression can span lines. Scanning line by line truncates
 *    `className={({ isActive }) =>` at the newline, and a fragment with no string literal
 *    in it then reads as a class -- reporting `isActive`, a render-prop parameter, fifteen
 *    times. The whole file is scanned in one pass, with line numbers derived from offsets.
 *
 * Get any of these wrong and the gate fails builds over classes that are perfectly fine,
 * which is worse than not having a gate at all.
 */
'use strict';
const fs = require('fs');
const path = require('path');

const UI_SRC = path.join(__dirname, '..', 'ui', 'src');

// Rule 5: some rules V2 uses are not IN ui/src. The accessibility component rules are
// shared with Portal V1 (#1520) and live under static/shared/, the same way the theme
// tokens moved to static/themes/ in #1522. Reading only ui/src reported every one of
// those classes as inert, which would have pushed them back into index.css -- recreating
// the duplication the shared file exists to remove.
const SHARED_CSS = path.join(
  __dirname,
  '..',
  'pkg',
  'server',
  'static',
  'shared',
);

// Tokens that appear where a class would, but are not classes.
const NOT_A_CLASS = new Set(['buttonClassName']);

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

// Rule 3: match escapes as part of the name, then unescape before comparing.
const SELECTOR = /\.(-?(?:\\.|[A-Za-z0-9_-])+)/g;
function collect(css, into) {
  for (const m of css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(SELECTOR)) {
    into.add(m[1].replace(/\\(.)/g, '$1'));
  }
}

const defined = new Set();
for (const f of files.filter((f) => f.endsWith('.css'))) {
  collect(fs.readFileSync(f, 'utf8'), defined);
}
if (fs.existsSync(SHARED_CSS)) {
  for (const f of walk(SHARED_CSS).filter((f) => f.endsWith('.css'))) {
    collect(fs.readFileSync(f, 'utf8'), defined);
  }
}
// Rule 2: a component's own <style> block defines classes too.
for (const f of files.filter((f) => /\.tsx?$/.test(f))) {
  const src = fs.readFileSync(f, 'utf8');
  for (const m of src.matchAll(/<style[^>]*>\{?`([\s\S]*?)`\}?<\/style>/g)) {
    collect(m[1], defined);
  }
}

if (defined.size === 0) {
  console.error(
    'check-css-modifiers: no CSS rules found -- the check would pass over nothing',
  );
  process.exit(1);
}

// Balanced-brace scan, so nested {} inside a className expression does not end it early.
// Returns the offset of each region so a line number can be derived (rule 4).
function classNameRegions(src) {
  const out = [];
  const re = /className\s*=\s*/g;
  let m;
  while ((m = re.exec(src))) {
    const i = m.index + m[0].length;
    if (src[i] === '"' || src[i] === "'") {
      const j = src.indexOf(src[i], i + 1);
      if (j > 0) out.push({ text: src.slice(i + 1, j), at: i, quoted: true });
    } else if (src[i] === '{') {
      let depth = 0;
      let j = i;
      for (; j < src.length; j++) {
        if (src[j] === '{') depth++;
        else if (src[j] === '}') {
          depth--;
          if (!depth) break;
        }
      }
      out.push({ text: src.slice(i + 1, j), at: i, quoted: false });
    }
  }
  return out;
}

const used = new Map();
for (const f of files.filter((f) => /\.tsx?$/.test(f))) {
  const rel = path.relative(path.join(__dirname, '..'), f);
  const src = fs.readFileSync(f, 'utf8');
  for (const region of classNameRegions(src)) {
    // Rule 1: only string literals inside the region are class sources.
    //
    // className="a b" IS the literal -- there are no quotes left inside it, so applying the
    // literal-extraction to it finds nothing and skips the whole attribute. Getting this
    // wrong meant the gate silently ignored every plain quoted className, which is most of
    // them, and reported a clean pass over a page carrying undefined classes.
    let literals;
    if (region.quoted) {
      literals = [region.text];
    } else {
      literals = [
        ...region.text.matchAll(/"([^"]*)"|'([^']*)'|`([^`]*)`/g),
      ].map((m) => m[1] ?? m[2] ?? m[3] ?? '');
      // No literal in an expression region means it holds a helper call or a variable and
      // nothing else. There are no class names in it to find.
      if (literals.length === 0) continue;
    }

    const line = src.slice(0, region.at).split('\n').length;
    for (const literal of literals) {
      // ${...} holds an expression, not classes.
      for (const tok of literal.replace(/\$\{[^}]*\}/g, ' ').split(/\s+/)) {
        if (!tok || NOT_A_CLASS.has(tok)) continue;
        if (!/^-?[A-Za-z][-\w/.:%[\]]*$/.test(tok)) continue;
        if (tok.includes('.')) continue; // property access, not a class
        if (defined.has(tok)) continue;
        if (!used.has(tok)) used.set(tok, []);
        used.get(tok).push(`${rel}:${line}`);
      }
    }
  }
}

if (used.size === 0) {
  console.log(
    `check-css-modifiers: OK -- every class used in ui/src has a rule (${defined.size} defined)`,
  );
  process.exit(0);
}

const total = [...used.values()].reduce((s, w) => s + w.length, 0);
console.error(
  `check-css-modifiers: ${used.size} class(es) used but never defined, ${total} occurrence(s)\n`,
);
for (const [cls, where] of [...used.entries()].sort(
  (a, b) => b[1].length - a[1].length,
)) {
  console.error(`  .${cls}  (${where.length})`);
  for (const w of where.slice(0, 4)) console.error(`      ${w}`);
  if (where.length > 4) console.error(`      …and ${where.length - 4} more`);
  const base = cls.split('--')[0];
  const near = [...defined]
    .filter((d) => d !== cls && d.startsWith(base))
    .sort()
    .slice(0, 6);
  if (near.length)
    console.error(`      near: ${near.map((s) => '.' + s).join(', ')}`);
  console.error('');
}
console.error(
  'Add a rule to ui/src/index.css, point the markup at a class that exists, or delete it',
);
console.error(
  'if something else already does the job. Tailwind is not wired up and its dependency was',
);
console.error(
  'removed in #1383, so a Tailwind-shaped name will not style anything.',
);
process.exit(1);
