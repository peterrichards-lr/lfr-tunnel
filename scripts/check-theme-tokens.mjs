#!/usr/bin/env node
/**
 * Verifies that every CSS custom property the stylesheets reference is actually defined,
 * in every theme.
 *
 * This exists because the same defect kept recurring in different disguises:
 *
 *   #1217  --success and --danger were defined only in the base :root, so the light theme
 *          rendered colours tuned for a dark background as text and failed WCAG contrast.
 *   #1221  Five properties were referenced but defined in no theme at all. Two had no
 *          fallback, including the @media print block, which meant printing on the dark
 *          theme produced a near-black page.
 *
 * Both are invisible in review and invisible at build time: a var() that resolves to
 * nothing makes the declaration invalid and the browser silently drops it. The only way to
 * notice is to look at every reference against every theme, which is what this does.
 *
 * A referenced property is acceptable if it is defined in the base :root (inherited by all
 * themes) or defined in each theme block. A fallback -- var(--x, something) -- is accepted
 * as intentional, but reported, since it usually means a name is wrong rather than that a
 * default was wanted.
 *
 *   #1774  The scan read .css files only. Portal V1 keeps much of its styling in inline
 *          `style` attributes, in `element.style.*` assignments, and in template literals
 *          inside dashboard.js -- none of which is a stylesheet, so 20 references to four
 *          properties no theme defines sat behind the gate's blind spot. Markup and script
 *          are now scanned too (see collectMarkupReferences).
 */
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { join, basename, relative } from 'node:path';

const UI_SRC = join(process.cwd(), 'ui', 'src');

// The theme files are shared by both portals and live with the static assets the Go server
// embeds, because that is the one place BOTH can read them from: Portal V2 @imports them into
// its bundle, Portal V1 links them (#1522).
const THEMES = join(process.cwd(), 'pkg', 'server', 'static', 'themes');

// Portal V1's stylesheet is scanned alongside V2's source, so one gate covers both. Until the
// themes were shared, V1 defined its own tokens and nothing checked them.
const V1_CSS = join(process.cwd(), 'pkg', 'server', 'static', 'dashboard.css');

// Every page the Go server embeds. Walked rather than listed so a new page is covered the day
// it is added -- the same reason test-shell-portability.sh derives its file set instead of
// keeping one by hand.
const SERVER_DIR = join(process.cwd(), 'pkg', 'server');

// A page is governed by the shared themes only if it links them. That link is the whole
// membership test, and deriving it beats an exclusion list: pages that carry their own token
// set in their own <style> block -- passcode.html, the standalone error pages, the localized
// legal/email templates -- must NOT be resolved against these themes, because their tokens are
// legitimately absent from them. Checking a self-contained page here would report every one of
// its own properties as undefined.
const THEME_LINK = '/static/themes/';

// Comments are stripped before scanning: prose describing a token -- including the
// comments explaining these very bugs -- would otherwise register as a reference.
//
// C-style block comments ONLY, which matters now that JS is scanned as well as CSS: a `//`
// comment cannot be stripped safely, because a URL inside a string literal would take the rest
// of its line with it and silently hide any real reference sharing that line. Losing coverage
// is worse than the false positive, so prose in a .js file that needs to spell a var()
// reference must sit in a block comment. Both halves of that trade-off were paid for in #1774:
// the note explaining the toast fix was written with `//` and failed this gate.
const stripComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '');
const read = (p) => stripComments(readFileSync(p, 'utf8'));

// HTML comments hide commented-out markup, which must not count as a live reference. Applied
// on top of stripComments, since a page can carry both kinds.
const readMarkup = (p) => read(p).replace(/<!--[\s\S]*?-->/g, '');

const VAR_REF = /var\(\s*(--[a-z0-9-]+)\s*(,)?/g;

function addRefs(refs, text, label) {
  for (const m of text.matchAll(VAR_REF)) {
    const existing = refs.get(m[1]) || { hasFallback: false, files: new Set() };
    if (m[2]) existing.hasFallback = true;
    existing.files.add(label);
    refs.set(m[1], existing);
  }
}

// Every var(--x) reference outside the theme files themselves.
function collectReferences() {
  const refs = new Map(); // name -> { hasFallback, files }
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== 'themes') walk(full);
        continue;
      }
      if (!entry.name.endsWith('.css')) continue;
      addRefs(refs, read(full), basename(full));
    }
  };
  walk(UI_SRC);

  // V1 is one file rather than a tree, so it is read directly rather than walked.
  addRefs(refs, read(V1_CSS), basename(V1_CSS));

  return refs;
}

// Portal V1's styling is not all in its stylesheet, so scanning stylesheets alone exempts most
// of it (#1774). The whole document is scanned, not just `style="…"` attributes, because the
// references hide at three different depths and an attribute-only regex sees one of them:
//
//   <div style="color: var(--text-main)">              an inline style attribute
//   item.style.color = 'var(--text-main)'              a property assignment in dashboard.js
//   `<strong style="color: var(--text-main)">`         markup inside a JS template literal
//   `<button onmouseover="this.style.color='…'">`      a handler inside that markup
//
// A var() token cannot appear in one of these files for any reason other than styling, so a
// whole-file scan costs no false positives and needs no list of the shapes to look for -- the
// next shape someone invents is covered without this script being touched.
//
// Scripts are found through the page that loads them rather than by globbing *.js, so a script
// belonging to a self-contained page is not silently resolved against the shared themes.
function collectMarkupReferences(refs) {
  const scanned = [];
  const skipped = [];

  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) =>
      a.name.localeCompare(b.name),
    )) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(full);
        continue;
      }
      if (!entry.name.endsWith('.html')) continue;

      const html = readMarkup(full);
      const rel = relative(process.cwd(), full);
      if (!html.includes(THEME_LINK)) {
        skipped.push(rel);
        continue;
      }

      const files = [rel];
      addRefs(refs, html, basename(full));

      // <script src="/static/x.js"> — resolved against the static dir the server serves it
      // from, so only first-party scripts are read (a CDN src has no local path and is
      // skipped by existsSync).
      for (const m of html.matchAll(
        /<script[^>]+src\s*=\s*["'](\/static\/[^"']+\.js)["']/g,
      )) {
        const script = join(
          SERVER_DIR,
          'static',
          m[1].slice('/static/'.length),
        );
        if (!existsSync(script)) continue;
        addRefs(refs, read(script), basename(script));
        files.push(relative(process.cwd(), script));
      }
      scanned.push(files);
    }
  };
  walk(SERVER_DIR);

  return { scanned, skipped };
}

// Properties defined per theme file, split by whether they sit on a bare :root (inherited
// by every theme) or inside a theme-specific selector.
function collectDefinitions() {
  const base = new Set();
  const perTheme = new Map();
  for (const file of readdirSync(THEMES)) {
    if (!file.endsWith('.css') || file === 'index.css') continue;
    const css = read(join(THEMES, file));
    const defined = new Set();
    for (const m of css.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)) defined.add(m[1]);
    perTheme.set(file, defined);

    // Anything declared under a bare `:root` is inherited by every theme. The selector is
    // often a list -- `:root, :root[data-theme="dark"] { ... }` -- so match the block and
    // then check whether any selector in it is an unqualified :root.
    for (const block of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      const selectors = block[1].split(',').map((sel) => sel.trim());
      if (!selectors.includes(':root')) continue;
      for (const m of block[2].matchAll(/(--[a-z0-9-]+)\s*:/g)) base.add(m[1]);
    }
  }
  return { base, perTheme };
}

// Colour values declared on the base :root, which every theme inherits unless it
// overrides them. A colour chosen for one theme's background is wrong on another's, so an
// un-overridden one is a bug waiting to be reported -- that is exactly what #1217 was:
// --success and --danger were dark-tuned, inherited by the light theme, and used as text.
//
// Checked separately from resolution because the failure is different: these *do* resolve,
// they just resolve to the wrong colour, which no amount of var() checking would notice.
function collectBaseColours() {
  const css = read(join(THEMES, 'dark.css'));
  const colours = new Map();
  for (const block of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const selectors = block[1].split(',').map((sel) => sel.trim());
    if (!selectors.includes(':root')) continue;
    for (const m of block[2].matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)) {
      if (/^(#|rgba?\(|hsla?\()/.test(m[2].trim()))
        colours.set(m[1], m[2].trim());
    }
  }
  return colours;
}

const refs = collectReferences();
const { scanned, skipped } = collectMarkupReferences(refs);
const { base, perTheme } = collectDefinitions();

// Printed on every run, pass or fail. A gate that quietly narrows its own scope reads exactly
// like one that is passing, which is how #1774 stayed hidden: saying out loud which pages were
// checked and which were not is the only thing that makes the coverage reviewable.
console.log('Pages linking the shared themes (markup and scripts scanned):');
for (const files of scanned) console.log(`  ${files.join('  +  ')}`);
const byDir = new Map();
for (const f of skipped) {
  const dir = f.split('/').slice(0, -1).join('/');
  if (!byDir.has(dir)) byDir.set(dir, []);
  byDir.get(dir).push(f.split('/').pop());
}
console.log(
  '\nPages that do not link the shared themes, so their tokens are not resolved against them\n' +
    '(each carries its own token set in its own <style> block):',
);
for (const [dir, names] of byDir) {
  const shown = names.length > 6 ? `${names.length} files` : names.join(', ');
  console.log(`  ${dir}/: ${shown}`);
}
console.log('');

// Anti-vacuity. The membership rule is derived from a <link> in the markup, so deleting that
// link -- or moving the themes to another path -- silently empties the markup scan while every
// stylesheet still resolves and the gate still exits 0. A scan that covers nothing reads exactly
// like a scan that found nothing, which is the failure #1402 recorded for the EDR guard and the
// one #1774 was filed for. Nothing weaker than "at least one page was actually read" catches it.
if (scanned.length === 0) {
  console.error(
    `\u274c No page under pkg/server links ${THEME_LINK}, so the markup and script scan\n` +
      'covered nothing. Either the themes moved and this check needs its path updated, or a\n' +
      'page lost its <link> and is no longer themed. Both are bugs; a silent pass is worse\n' +
      'than either.',
  );
  process.exit(1);
}

const undefinedTokens = [];
const fallbackOnly = [];

for (const [name, info] of [...refs].sort()) {
  if (base.has(name)) continue;
  const missing = [...perTheme]
    .filter(([, defs]) => !defs.has(name))
    .map(([f]) => f);
  if (missing.length === perTheme.size) {
    undefinedTokens.push({ name, info, missing });
  } else if (missing.length > 0) {
    undefinedTokens.push({ name, info, missing });
  } else if (info.hasFallback) {
    fallbackOnly.push(name);
  }
}

if (fallbackOnly.length > 0) {
  console.log(
    'Referenced with a fallback (accepted, but check the name is right):',
  );
  for (const n of fallbackOnly) console.log(`  ${n}`);
  console.log('');
}

if (undefinedTokens.length > 0) {
  console.error('❌ CSS custom properties that will not resolve:\n');
  for (const { name, info, missing } of undefinedTokens) {
    const where =
      missing.length === perTheme.size
        ? 'no theme defines it'
        : `missing from ${missing.join(', ')}`;
    const fb = info.hasFallback
      ? ' — it has a fallback, so it renders, but never follows the theme'
      : ' — the declaration is invalid and the browser drops it silently';
    console.error(`  ${name}: ${where}${fb}`);
    console.error(`      referenced in: ${[...info.files].join(', ')}`);
  }
  console.error(
    '\nDefine it in every theme, or in the base :root if it is theme-independent.',
  );
  process.exit(1);
}

// Second check: a colour inherited by every theme from the base :root.
const baseColours = collectBaseColours();
const themeFiles = [...perTheme.keys()].filter((f) => f !== 'dark.css');
const inherited = [...baseColours]
  .filter(([name]) => refs.has(name))
  .filter(([name]) => themeFiles.every((f) => !perTheme.get(f).has(name)));

if (inherited.length > 0) {
  console.error('❌ Colours inherited by every theme from the base :root:\n');
  for (const [name, value] of inherited) {
    console.error(
      `  ${name}: ${value} — no theme overrides it, so every theme renders this exact colour.`,
    );
  }
  console.error(
    '\nA colour picked for one theme is usually wrong against another background. This is\n' +
      'how #1217 happened: --success and --danger were tuned for dark, inherited by light,\n' +
      'and used as text at 2.4:1 contrast. Give each theme its own value. If the colour is\n' +
      'genuinely theme-independent, set it explicitly in each theme so that is a decision\n' +
      'someone made rather than something nobody noticed.',
  );
  process.exit(1);
}

console.log(
  `✅ All ${refs.size} referenced CSS custom properties resolve in every theme.`,
);
console.log(
  `✅ No referenced colour is inherited un-overridden from the base :root.`,
);
