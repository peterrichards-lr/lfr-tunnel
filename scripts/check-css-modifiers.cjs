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

// Portal V1's corpus -- the documents, the assets each links, and every class applied across
// them -- comes from the pass check-theme-tokens.mjs also consumes (#1841). Before that, each
// gate walked V1 with its own collector and each missed a different half: this one did not
// read subdirectories of pkg/server, that one did not follow a relative href. What counts as
// DEFINED stays here, because the two gates genuinely disagree about it.
const { collectV1Usage, DYN } = require('./lib/collect-v1-usage.cjs');

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
    'check-css-modifiers [V2]: no CSS rules found -- the check would pass over nothing',
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
// All four of those now come from scripts/lib/collect-v1-usage.cjs, which check-theme-tokens.mjs
// consumes as well (#1841). They were duplicated in the two gates, and the copies had already
// drifted: this one listed pkg/server/*.html and pkg/server/static/*.html rather than walking,
// so the 34 localized templates under pkg/server/templates were invisible to it. A new markup
// shape is now either visible to both gates or to neither, and "neither" is loud --
// tests/hooks/test-v1-usage-parity.sh plants a class and a custom property in the same place
// and requires both to react.
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

const REPO_ROOT = path.join(__dirname, '..');

// The web roots this gate walks, and the whole of its scope. Named as a list rather than as a
// single path (#1779), because the answer to "what does this gate not look at" was `pkg/client`
// and nothing said so.
//
// pkg/client/dashboard.html is the client inspector: a 1600-line server-rendered-style page,
// styled by one inline <style> block, written in the same idiom as Portal V1 and by the same
// hand. Every reason this gate exists applies to it verbatim -- and pointing the gate at it
// found `.input-field` (3), `.btn` and `.btn-secondary` applied in the Logs view with no rule
// anywhere on the page, which is `.alert-warning` (#1744) in a different file.
//
// Each root is walked independently and served-path hrefs (`/static/…`) resolve against the
// root the document was found under, which is what the server does at runtime.
const V1_WEB_ROOTS = [
  path.join(REPO_ROOT, 'pkg', 'server'),
  path.join(REPO_ROOT, 'pkg', 'client'),
];

// Classes V1 applies that have no rule and are not yet fixed. Each entry must say WHY.
// Tracked for burndown by #1752; read that issue before adding to this list instead of
// fixing the class.
//
// #1752 emptied it. Everything it held was either an inline style waiting to be hoisted into
// the class that already named it, a class V2 had rules for and V1 had only markup for, or a
// marker nothing read. The map stays because it is the documented place for a genuinely
// unfixable class -- a third-party stylesheet's name, say (note 3 above) -- and because
// removing it would take the ratchet with it. Empty is the state to keep it in: an entry here
// is an exemption, and every one added is a class the gate stops protecting.
//
// The four below are what widening the scan to pkg/client turned up (#1779), all in the client
// inspector's own page. They are deferred rather than fixed here because each one needs a rule
// written and the visual result looked at, and this change is a scope-boundary change whose
// whole property is "the gate now reads a file it did not". Burning them down is #1853. The
// enumeration is the deliverable; a fix buried in it would make the before/after unreadable.
const V1_KNOWN_INERT = new Map(
  Object.entries({
    'input-field':
      'pkg/client/dashboard.html: the three Logs-view filter inputs and selects carry it; the ' +
      'page defines no rule, so they render unstyled. Needs a rule, not a rename (#1853).',
    'traffic-header':
      'pkg/client/dashboard.html:254: fully styled by its own inline style attribute, so the ' +
      'class is a name with nothing behind it. Hoist the inline style into it or drop it (#1853).',
    btn:
      'pkg/client/dashboard.html:1304: the Logs Refresh button. `.btn`/`.btn-secondary` are ' +
      'Portal V1 names that pkg/server/static/dashboard.css defines and this page never links ' +
      '(#1853).',
    'btn-secondary':
      'pkg/client/dashboard.html:1304: the other half of the same button. See .btn (#1853).',
  }),
);

const v1InertSeen = new Set();

function checkV1() {
  const documents = [];
  for (const webRoot of V1_WEB_ROOTS) {
    documents.push(
      ...collectV1Usage({ webRoot, repoRoot: REPO_ROOT }).documents,
    );
  }

  const undef = new Map();
  const nearby = new Set();
  const seen = new Set(); // every class name the pass actually examined
  let examinedDocs = 0;
  const brokenLinks = [];
  let resolvedAssets = 0;

  for (const doc of documents) {
    const docRel = doc.page;

    // What counts as DEFINED is this gate's own question and stays here: the document's own
    // <style> blocks plus the stylesheets it links, and nothing pooled from any other page.
    const defined = new Set();
    for (const block of doc.styleBlocks) collect(block, defined);
    for (const sheet of doc.stylesheets) collect(sheet.text, defined);
    // A first-party asset the page links that is not there (#1849). This used to print the
    // stylesheet half to stderr WITHOUT setting the failure flag, and `continue` past the
    // script half entirely -- so `<script src="/static/dashbaord.js">` produced a 404 at
    // runtime, every behaviour that file provides silently absent, and a green build.
    //
    // Both kinds now fail. What counts as first-party is resolveAsset's question and is
    // already answered: a CDN href, a scheme-relative URL and a data: URI never reach here.
    for (const missing of doc.missingAssets) {
      brokenLinks.push({ page: docRel, ...missing });
    }
    resolvedAssets += doc.stylesheets.length + doc.scripts.length;

    // Note 2: classes this document's own scripts read back are behaviour hooks.
    const hooks = doc.classHooks;

    if (defined.size === 0 && !/(?<![-\w])class\s*=/.test(doc.markup)) continue;
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

    // The four places V1 applies a class -- `class="…"` in the page, the same attribute
    // inside a template literal in the script, `className = "…"`, and classList.* -- are
    // collected once, by the shared pass. Each use arrives with the file and line it came
    // from, which is what the report prints.
    for (const use of doc.classUses) add(use.name, `${use.file}:${use.line}`);
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

  // The link check's own anti-vacuity floor (#1849). "No page links a missing asset" and "the
  // resolver returned null for every href it was given" print the same thing and exit the same
  // way, so require that at least one first-party asset was actually resolved AND read.
  if (resolvedAssets === 0) {
    console.error(
      'check-css-modifiers [V1]: not one first-party stylesheet or script was resolved from any\n' +
        'page, so the broken-link check below covered nothing. Either every asset is now loaded\n' +
        'from a CDN, or resolveAsset has stopped matching and a missing file would go unreported.',
    );
    return false;
  }

  const stale = [...V1_KNOWN_INERT.keys()].filter((c) => !v1InertSeen.has(c));
  let ok = true;

  if (brokenLinks.length > 0) {
    ok = false;
    console.error(
      `check-css-modifiers [V1]: ${brokenLinks.length} link(s) to a first-party asset that does not exist:\n`,
    );
    for (const b of brokenLinks)
      console.error(`  ${b.page} links ${b.kind} ${b.href}`);
    console.error(
      '\nThe browser 404s and carries on: the page renders, and every rule or behaviour that\n' +
        'file provides is silently absent. Correct the path, or remove the tag. Only first-party\n' +
        'paths are checked -- a CDN href, a scheme-relative URL and a data: URI never reach here.\n',
    );
  }

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
