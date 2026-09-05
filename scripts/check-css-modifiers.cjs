#!/usr/bin/env node
/**
 * Fails when a class used in either portal's source has no rule anywhere in its CSS.
 *
 * TWO PASSES, one per portal. Portal V2 (ui/src) came first; Portal V1 (pkg/server) was
 * added in #1744, after `.alert-warning` sat in V1 for months styling nothing. The gate
 * that exists precisely to catch that was pointed only at V2 -- a blind spot in exactly
 * the arm that had the bug. The two passes share the selector parser and nothing else,
 * because the source languages and the stylesheet-resolution rules differ.
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

// Shared reporter for both passes: same shape of failure, same shape of message.
function report(label, undefinedClasses, definedSet, advice) {
  const total = [...undefinedClasses.values()].reduce(
    (s, w) => s + w.length,
    0,
  );
  console.error(
    `check-css-modifiers [${label}]: ${undefinedClasses.size} class(es) used but never defined, ${total} occurrence(s)\n`,
  );
  for (const [cls, where] of [...undefinedClasses.entries()].sort(
    (a, b) => b[1].length - a[1].length,
  )) {
    console.error(`  .${cls}  (${where.length})`);
    for (const w of where.slice(0, 4)) console.error(`      ${w}`);
    if (where.length > 4) console.error(`      …and ${where.length - 4} more`);
    // Shorten the prefix one dash-segment at a time until something matches. `.alert-warning`
    // has no defined name under `alert-warning`, but three siblings under `alert` -- and
    // those siblings are the whole hint a reader needs to write the missing rule.
    const parts = cls.replace(/\u2026$/, '').split(/(?=-)/);
    let near = [];
    for (let i = parts.length; i > 0 && near.length === 0; i--) {
      const base = parts.slice(0, i).join('');
      if (!base) continue;
      near = [...definedSet]
        .filter((d) => d !== cls && d.startsWith(base))
        .sort()
        .slice(0, 6);
    }
    if (near.length)
      console.error(`      near: ${near.map((s) => '.' + s).join(', ')}`);
    console.error('');
  }
  for (const line of advice) console.error(line);
  console.error('');
}

let failed = false;

if (used.size === 0) {
  console.log(
    `check-css-modifiers [V2]: OK -- every class used in ui/src has a rule (${defined.size} defined)`,
  );
} else {
  failed = true;
  report('V2', used, defined, [
    'Add a rule to ui/src/index.css, point the markup at a class that exists, or delete it',
    'if something else already does the job. Tailwind is not wired up and its dependency was',
    'removed in #1383, so a Tailwind-shaped name will not style anything.',
  ]);
}

// The V1 pass runs at the bottom of this file, after its own declarations.

// ---------------------------------------------------------------------------
// Portal V1 (pkg/server) -- added in #1744.
// ---------------------------------------------------------------------------
//
// V1 is server-rendered HTML plus one large hand-written script, so nothing about the V2
// pass transfers except the selector parser. What replaces it:
//
// * DOCUMENT-SCOPED, NOT PROJECT-SCOPED. V1 is a dozen standalone pages, most carrying
//   their own <style> block. Pooling every rule into one set would let passcode.html's
//   inline `.btn` satisfy a `.btn` used in dashboard.html -- a page that never loads it.
//   Each document therefore gets the rules it actually links, and nothing else.
//
// * THE LINK GRAPH IS DERIVED, NOT LISTED. Stylesheets come from that document's
//   <link rel="stylesheet">, scripts from its <script src>, both resolved against
//   pkg/server/. A new page is covered the moment it exists; a page that stops linking
//   dashboard.css stops being checked against it. Nothing to keep in sync by hand.
//
// * CLASSES ARE APPLIED FROM FOUR PLACES, not one. `class="..."` in the HTML, the same
//   attribute inside template literals in the JS (which is where V1 renders most of its
//   tables), `className = '...'` assignments, and `classList.add/remove/toggle/replace`.
//   Reading only the HTML misses `.alert-warning` at dashboard.js:4071 -- the exact
//   occurrence #1744 was filed for.
//
// THREE THINGS THAT LOOK LIKE VIOLATIONS AND ARE NOT. Each is recognised in code rather
// than listed, so it keeps working as the source changes:
//
// 1. DYNAMICALLY COMPOSED NAMES. `class="edge-status-dot--${status}"` yields the token
//    `edge-status-dot--` once the interpolation is stripped, which is a prefix and not a
//    class. Reported only when NO defined class starts with that prefix -- which still
//    catches a genuinely dead family, while `toast-${type}` passes on `.toast-success`.
//
// 2. BEHAVIOUR HOOKS. A class both applied and read back through
//    querySelector/closest/matches/getElementsByClassName, with no rule anywhere, is a
//    handle for script, not styling -- `.edge-select-checkbox`, `.server-version-display`.
//    Demanding a rule for those would mean adding empty ones.
//
// 3. THIRD-PARTY STYLESHEETS. dashboard.html links driver.css from a CDN, whose rules are
//    not in this repo and cannot be read. Nothing in V1 currently applies a driver.js class
//    through markup, so the exemption list below holds none of them -- but that is where
//    such a class belongs, named with the stylesheet it comes from, not silently dropped.
//
// Everything else that is genuinely inert lives in V1_KNOWN_INERT with a reason. That list
// is a ratchet, not an escape hatch: an entry that no longer matches anything FAILS the
// check, so it can only shrink, and it cannot quietly outlive the problem it describes.

const V1_HTML_DIRS = [
  path.join(__dirname, '..', 'pkg', 'server'),
  path.join(__dirname, '..', 'pkg', 'server', 'static'),
];
const V1_WEB_ROOT = path.join(__dirname, '..', 'pkg', 'server');

// Classes V1 applies that have no rule and are not yet fixed. Each entry must say WHY.
// Tracked for burndown by #1752; read that issue before adding to this list instead of
// fixing the class.
const V1_KNOWN_INERT = new Map(
  Object.entries({
    'btn-secondary':
      'inert button variant; every use carries its own inline style. Hoisting those into a rule is a visual refactor of V1 buttons, not a bug fix (#1752)',
    'btn-outline': 'inert button variant; same as btn-secondary (#1752)',
    'form-group':
      'inert layout class; each use inlines its own margin-bottom (#1752)',
    'tab-btn':
      'inert; the install-instructions tabs inline every declaration and swap them in JS (#1752)',
    'tab-content':
      'inert; paired with tab-btn, visibility driven by inline display (#1752)',
    'custom-dropdown-trigger':
      'inert; both triggers inline the full flex layout (#1752)',
    'dashboard-table':
      'inert; the one table using it inlines width and border-collapse (#1752)',
    'timestamp-tooltip':
      'inert; the span inlines cursor and border-bottom (#1752)',
    'host-link': 'inert; the anchor inlines colour, font and weight (#1752)',
    'btn-copy': 'inert; the button inlines its full appearance (#1752)',
    'form-control':
      'inert; the pagination select falls back to the global element style (#1752)',
    'modal-header':
      'inert; the keyboard-shortcuts overlay header is unstyled. Needs a design decision, not a one-line rule (#1752)',
    'modal-title': 'inert; same overlay as modal-header (#1752)',
    'modal-close': 'inert; same overlay as modal-header (#1752)',
    'sidebar-collapsed':
      'dead state marker on #dashboard-screen: no rule reads it and no script queries it. The collapse itself works through .sidebar.collapsed, so removing it is safe but out of scope (#1752)',
  }),
);

const v1InertSeen = new Set();

const V1_SELECTOR_CALLS =
  /(?:querySelectorAll|querySelector|closest|matches|getElementsByClassName)\s*\(\s*(?:'([^']*)'|"([^"]*)"|`([^`]*)`)/g;

function v1ResolveHref(href, docPath) {
  if (/^(?:[a-z]+:)?\/\//i.test(href) || /^data:/i.test(href)) return null; // external
  const clean = href.split(/[?#]/)[0];
  if (!clean) return null;
  if (clean.startsWith('/')) return path.join(V1_WEB_ROOT, clean.slice(1));
  return path.join(path.dirname(docPath), clean);
}

// `${...}` collapses to a sentinel rather than a space, so a token that was built by
// interpolation stays distinguishable from one written out in full.
const DYN = '\u0000';
function v1Tokens(literal) {
  return literal.replace(/\$\{[^}]*\}/g, DYN).split(/\s+/);
}

function checkV1() {
  const rootRel = (p) => path.relative(path.join(__dirname, '..'), p);
  const docs = [];
  for (const dir of V1_HTML_DIRS) {
    if (!fs.existsSync(dir)) continue;
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.isFile() && e.name.endsWith('.html'))
        docs.push(path.join(dir, e.name));
    }
  }

  const undef = new Map();
  const nearby = new Set();
  const seen = new Set(); // every class name the pass actually examined
  let examinedDocs = 0;

  for (const doc of docs) {
    const docRel = rootRel(doc);
    const src = fs.readFileSync(doc, 'utf8');

    const defined = new Set();
    for (const m of src.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g))
      collect(m[1], defined);
    for (const m of src.matchAll(/<link\b[^>]*>/g)) {
      if (!/\brel\s*=\s*(['"])stylesheet\1/i.test(m[0])) continue;
      const href = /\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')/.exec(m[0]);
      if (!href) continue;
      const raw = href[1] ?? href[2];
      const resolved = v1ResolveHref(raw, doc);
      if (!resolved) continue; // third-party; see note 3 above
      if (fs.existsSync(resolved))
        collect(fs.readFileSync(resolved, 'utf8'), defined);
      else
        console.error(
          `check-css-modifiers [V1]: ${docRel} links ${raw}, which does not exist`,
        );
    }

    const scripts = [];
    for (const m of src.matchAll(
      /<script\b[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')/g,
    )) {
      const resolved = v1ResolveHref(m[1] ?? m[2], doc);
      if (resolved && fs.existsSync(resolved)) scripts.push(resolved);
    }

    // Note 2: classes this document's own scripts read back are behaviour hooks.
    const hooks = new Set();
    const scriptSrc = new Map();
    for (const s of scripts) {
      const js = fs.readFileSync(s, 'utf8');
      scriptSrc.set(s, js);
      for (const m of js.matchAll(V1_SELECTOR_CALLS)) {
        const sel = m[1] ?? m[2] ?? m[3];
        for (const c of sel.matchAll(/\.(-?[A-Za-z][-\w]*)/g)) hooks.add(c[1]);
      }
    }

    if (defined.size === 0 && !/(?<![-\w])class\s*=/.test(src)) continue;
    examinedDocs++;

    const add = (tok, where) => {
      if (!tok) return;
      if (tok.includes(DYN)) {
        // Note 1: a composed name. Its literal prefix must match something.
        const prefix = tok.slice(0, tok.indexOf(DYN));
        if (!prefix) return; // wholly dynamic -- there is no name here to check
        const key = prefix + '…';
        seen.add(key);
        if ([...defined].some((d) => d.startsWith(prefix))) return;
        if (V1_KNOWN_INERT.has(key)) {
          v1InertSeen.add(key);
          return;
        }
        for (const d of defined) nearby.add(d);
        if (!undef.has(key)) undef.set(key, []);
        undef.get(key).push(where);
        return;
      }
      if (!/^-?[A-Za-z][-\w]*$/.test(tok)) return;
      seen.add(tok);
      if (defined.has(tok)) return;
      if (hooks.has(tok)) return;
      if (V1_KNOWN_INERT.has(tok)) {
        v1InertSeen.add(tok);
        return;
      }
      for (const d of defined) nearby.add(d);
      if (!undef.has(tok)) undef.set(tok, []);
      undef.get(tok).push(where);
    };

    const scanClassAttrs = (text, rel) => {
      for (const m of text.matchAll(
        /(?<![-\w])class\s*=\s*(?:"([^"]*)"|'([^']*)')/g,
      )) {
        const line = text.slice(0, m.index).split('\n').length;
        for (const tok of v1Tokens(m[1] ?? m[2])) add(tok, `${rel}:${line}`);
      }
    };

    scanClassAttrs(src, docRel);

    for (const s of scripts) {
      const rel = rootRel(s);
      const js = scriptSrc.get(s);
      scanClassAttrs(js, rel);
      for (const m of js.matchAll(
        /\.className\s*\+?=\s*(?:'([^']*)'|"([^"]*)"|`([^`]*)`)/g,
      )) {
        const line = js.slice(0, m.index).split('\n').length;
        for (const tok of v1Tokens(m[1] ?? m[2] ?? m[3]))
          add(tok, `${rel}:${line}`);
      }
      for (const m of js.matchAll(
        /\.classList\.(?:add|remove|toggle|replace)\(([^)]*)\)/g,
      )) {
        const line = js.slice(0, m.index).split('\n').length;
        for (const lit of m[1].matchAll(/'([^']*)'|"([^"]*)"|`([^`]*)`/g))
          for (const tok of v1Tokens(lit[1] ?? lit[2] ?? lit[3]))
            add(tok, `${rel}:${line}`);
      }
    }
  }

  // A pass that examined nothing reads as coverage and is none -- the lesson #1402 wrote
  // down for the EDR guard, applied here. Assert documents were found, that the portal
  // itself is among them, and that a plausible number of classes actually went through.
  if (examinedDocs === 0 || !seen.has('sidebar') || seen.size < 50) {
    console.error(
      `check-css-modifiers [V1]: examined ${examinedDocs} document(s) and ${seen.size} class(es) -- the check would pass over nothing`,
    );
    return false;
  }

  const stale = [...V1_KNOWN_INERT.keys()].filter((c) => !v1InertSeen.has(c));
  let ok = true;

  if (undef.size > 0) {
    ok = false;
    report('V1', undef, nearby, [
      'Add a rule to pkg/server/static/dashboard.css next to the ones it belongs with, point',
      'the markup at a class that exists, or delete the class if the element is already fully',
      'styled without it. A trailing … means the name is built by interpolation and no',
      'defined class starts with that prefix.',
      '',
      'If it is genuinely inert and fixing it is separate work, add it to V1_KNOWN_INERT in',
      'this script WITH A REASON and link it from #1752 -- do not narrow the scan.',
    ]);
  }

  if (stale.length > 0) {
    ok = false;
    console.error(
      `check-css-modifiers [V1]: ${stale.length} stale V1_KNOWN_INERT entr(ies) -- these classes no longer appear undefined anywhere:\n`,
    );
    for (const c of stale) console.error(`  .${c}`);
    console.error(
      '\nRemove them from V1_KNOWN_INERT. The list is a ratchet: it may only shrink, so an',
    );
    console.error(
      'entry that outlives its problem would otherwise silently exempt a future regression.',
    );
    console.error('');
  }

  if (ok) {
    console.log(
      `check-css-modifiers [V1]: OK -- every class applied across ${examinedDocs} Portal V1 document(s) has a rule (${seen.size} examined, ${V1_KNOWN_INERT.size} known-inert exemptions)`,
    );
  }
  return ok;
}

if (!checkV1()) failed = true;

process.exit(failed ? 1 : 0);
