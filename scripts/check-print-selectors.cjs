#!/usr/bin/env node
/**
 * check-print-selectors.cjs -- a print stylesheet may not target markup that does not exist (#1916).
 *
 * The defect this guards: `@media print` in ui/src/index.css reset `.app-container` to release
 * the viewport confinement so a report could flow across pages. Nothing has ever rendered that
 * class. The real shell carries `h-screen overflow-hidden`, so every V2 export was clipped to a
 * single page -- and the stylesheet looked correct, because a rule that matches nothing is
 * indistinguishable from a rule that works.
 *
 * Ordinary CSS checks do not catch this. check-css-modifiers.cjs asks "does every class USED in
 * the markup have a rule?" -- the opposite direction. A selector with no markup is invisible to
 * it, and invisible is exactly the failure mode.
 *
 * So this compares rather than documents: every class/id selector inside @media print must
 * appear somewhere in that arm's markup. Fails closed -- if either side parses to nothing the
 * run exits 1 rather than reporting that two empty sets agree (#1779).
 *
 * "Appears" means on TOKEN BOUNDARIES, via findMarker(), not String.includes() (#2208).
 * selectorsIn() stores the bare name without its leading `.`, so a containment test counted
 * `.summary` live against markup holding only `summary-row` or `summaryTotal` -- the #1916
 * failure this gate exists to prevent, surviving in extended-name form. Same defect as #2201 one
 * file over, found by sweeping scripts/ for the shape rather than for the symbol, and matched by
 * the same shared helper rather than a second copy of it.
 *
 * What that still does not buy, stated where tests/hooks/test-gate-scope-boundaries.sh can
 * assert it rather than only here: the markup side is a concatenated blob of source, so the word
 * "summary" in a comment or a sentence is still a match. The boundary rule catches the rename,
 * which is how these rot; telling markup from prose would need a parser.
 */

const fs = require('fs');
const path = require('path');
const { findMarker } = require('./lib/token-match.cjs');

const REPO = path.join(__dirname, '..');
const rel = (p) => path.relative(REPO, p);

// Selectors that legitimately match nothing in the arm's own source.
// Each needs a reason: an unexplained entry here is how a real dead selector gets waved through.
const EXEMPT = {
  // Applied by Chart.js / the browser, not written in our markup.
  canvas: 'element selector, not a class',
  // Generated at runtime by renderTable() rather than written in dashboard.html.
  'print-hide': 'applied dynamically by dashboard.js renderTable()',
};

const ARMS = [
  {
    name: 'V2',
    css: path.join(REPO, 'ui/src/index.css'),
    markup: [path.join(REPO, 'ui/src')],
    exts: ['.tsx', '.ts', '.html'],
  },
  {
    name: 'V1',
    css: path.join(REPO, 'pkg/server/static/dashboard.css'),
    markup: [
      path.join(REPO, 'pkg/server/dashboard.html'),
      path.join(REPO, 'pkg/server/static/dashboard.js'),
    ],
    exts: ['.html', '.js'],
  },
];

let failed = false;
const fail = (m) => {
  console.error(`  ${m}`);
  failed = true;
};

/** Extract the body of the @media print block, brace-balanced. */
function printBlock(src) {
  const start = src.indexOf('@media print');
  if (start < 0) return null;
  const open = src.indexOf('{', start);
  if (open < 0) return null;
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}') {
      depth--;
      if (depth === 0) return src.slice(open + 1, i);
    }
  }
  return null;
}

/** Class and id selectors used in a CSS fragment, ignoring @page and declarations. */
function selectorsIn(block) {
  const out = new Set();
  // Strip comments FIRST: a comment explaining a dead selector names it, and would otherwise
  // be read as a live one -- the gate would then pass precisely because someone documented the
  // bug. Then strip declaration bodies so property values (e.g. url(#x)) cannot look like
  // selectors either.
  const selectorText = block
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\{[^{}]*\}/g, '{}');
  for (const m of selectorText.matchAll(/([.#])([A-Za-z_][\w-]*)/g)) {
    out.add(m[2]);
  }
  return out;
}

function collectMarkup(paths, exts) {
  let blob = '';
  const walk = (p) => {
    const st = fs.statSync(p);
    if (st.isDirectory()) {
      for (const e of fs.readdirSync(p)) walk(path.join(p, e));
    } else if (exts.includes(path.extname(p))) {
      blob += fs.readFileSync(p, 'utf8');
    }
  };
  for (const p of paths) {
    if (fs.existsSync(p)) walk(p);
  }
  return blob;
}

for (const arm of ARMS) {
  if (!fs.existsSync(arm.css)) {
    fail(`${arm.name}: ${rel(arm.css)} not found.`);
    continue;
  }
  const block = printBlock(fs.readFileSync(arm.css, 'utf8'));
  if (!block) {
    fail(`${arm.name}: no @media print block in ${rel(arm.css)}.`);
    continue;
  }
  const selectors = [...selectorsIn(block)].sort();
  if (selectors.length === 0) {
    fail(
      `${arm.name}: parsed no selectors out of the print block -- refusing to pass vacuously.`,
    );
    continue;
  }
  const markup = collectMarkup(arm.markup, arm.exts);
  if (markup.length === 0) {
    fail(`${arm.name}: read no markup, so nothing could be verified.`);
    continue;
  }

  const dead = selectors.filter(
    (s) => !EXEMPT[s] && findMarker(markup, s) === -1,
  );
  // "The class is gone" and "the class was renamed to something longer" ask the reader to do
  // different things, so they are reported apart rather than lumped together (#2208, after
  // #2201's locateMarker).
  const looseOnly = dead.filter((s) => markup.includes(s));
  if (dead.length) {
    fail(
      `${arm.name} print styles target markup that does not exist: ${dead.join(', ')}`,
    );
    if (looseOnly.length) {
      fail(
        `  ${looseOnly.join(', ')}: present only as part of a longer name. A rename that ` +
          'EXTENDS a class (summary -> summary-row) leaves the old name a substring of its ' +
          'replacement, so a containment test would still vouch for it (#2201). Point the ' +
          'print rule at the name that replaced it.',
      );
    }
    fail(
      '  A rule matching nothing looks identical to a rule that works. This is #1916.',
    );
  } else {
    console.log(
      `  ${arm.name}: OK -- ${selectors.length} print selector(s) all match markup`,
    );
  }
}

if (failed) {
  console.error('');
  console.error('PRINT SELECTOR CHECK FAILED');
  console.error(
    '  Either the markup lost the class, or the stylesheet targets one that never',
  );
  console.error(
    '  existed. Fix whichever is wrong -- or add it to EXEMPT with a reason.',
  );
  process.exit(1);
}

console.log('OK -- every print selector in both arms matches real markup');
