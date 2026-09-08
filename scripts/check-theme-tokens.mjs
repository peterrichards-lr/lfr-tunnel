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
 *
 *   #1784  A page that does NOT link the shared themes was reported as held out and then
 *          resolved against nothing at all. setup.css carries its own 18-property :root and
 *          referenced three properties it does not define -- including, again, the @media
 *          print body rule that #1221 was filed for. Holding those pages out of the SHARED
 *          theme check was right; leaving them unchecked was not. Every page now has a
 *          definition source: the shared themes if it links them, otherwise the tokens it
 *          defines itself (see collectDocumentScope). The membership rule is unchanged --
 *          only what a non-member is resolved against.
 *
 *   #1802  Resolving is not the only question worth asking about a var(). Inside @media
 *          print, a token that resolves perfectly is still a defect if its value depends on
 *          the screen theme, because print-color-adjust: exact then tells the browser to
 *          reproduce the screen on paper. #1221 fixed the body rule in one file, #1784 fixed
 *          it in a second, and BOTH left the .glass rule four lines below it untouched --
 *          in all three stylesheets -- because the class was named as "the body rule" rather
 *          than as "a print colour that follows the screen theme". See collectPrintScope.
 *
 *   #1803  Coverage is derived from a <link>, and until now the shared scope followed only
 *          <script src>. So a stylesheet reached no other way was read by neither scope:
 *          static/offline.css (no page links it at all) and static/shared/a11y.css (linked
 *          by dashboard.html, which the shared scope read without following the link). Both
 *          could have referenced anything and stayed green. The shared scope now follows
 *          <link rel=stylesheet> as well, and every .css under pkg/server must end the run
 *          having been read by some scope -- see the coverage report below. That converts
 *          the blind spot #1784 could only pin into a build failure.
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

// A custom property DECLARATION, as opposed to a reference. Anchored on the character that can
// legally precede one -- a block open, a preceding declaration's semicolon, or the start of a
// line -- rather than on line start alone, because a page's own <style> block is often written
// as `:root{--a:1;--b:2}` on one line and a line-anchored pattern would see one of the two.
// `var(--x)` cannot match: a reference is followed by `)` or `,`, never by a colon.
const VAR_DECL = /(?:^|[{;])\s*(--[a-z0-9-]+)\s*:/gm;

function addDecls(set, text) {
  for (const m of text.matchAll(VAR_DECL)) set.add(m[1]);
}

// The same declaration, kept with its VALUE (#1802). Every check above this line asks only
// whether a property is set; the print scope asks what it is set TO, because "does this
// colour depend on the screen theme" is a question about values and cannot be answered from
// names. Split from VAR_DECL rather than replacing it: the resolution checks want a flat set
// of names and would be slower and no more correct reading values they never look at.
const DECL_VALUE = /(?:^|[{;])\s*(--[a-z0-9-]+)\s*:\s*([^;{}]*)/g;

// Whitespace is stripped, not collapsed, before two values are compared. Prettier wraps a
// long declaration across lines, so one value can appear as two different strings in the same
// file -- setup.css writes --login-gradient on one line in its :root and across five in its
// prefers-color-scheme copy -- and a collapse-to-single-space normalisation reports those two
// as a theme-varying pair. Stripping is safe here because the result is only ever compared for
// equality, never parsed; the readable form is kept separately for the message.
const normalise = (v) => v.replace(/\s+/g, '');

// name -> normalised value -> { display, sources }. A property with more than one entry in its
// inner map has a value that depends on which theme (or which media query) is in force.
function addValues(map, text, source) {
  for (const m of text.matchAll(DECL_VALUE)) {
    const display = m[2].trim().replace(/\s+/g, ' ');
    const norm = normalise(display);
    if (!norm) continue;
    if (!map.has(m[1])) map.set(m[1], new Map());
    const byValue = map.get(m[1]);
    if (!byValue.has(norm)) byValue.set(norm, { display, sources: new Set() });
    byValue.get(norm).sources.add(source);
  }
}

// A first-party asset a page pulls in by its served path. Resolved against the static dir the
// server serves it from, so a CDN href has no local path and is dropped by existsSync.
const SCRIPT_SRC = /<script[^>]+src\s*=\s*["'](\/static\/[^"']+\.js)["']/g;
const STYLE_HREF = /<link[^>]+href\s*=\s*["'](\/static\/[^"']+\.css)["']/g;

function* linkedAssets(html, pattern) {
  for (const m of html.matchAll(pattern)) {
    const path = join(SERVER_DIR, 'static', m[1].slice('/static/'.length));
    if (!existsSync(path)) continue;
    yield { path, rel: relative(process.cwd(), path) };
  }
}

// ---------------------------------------------------------------------------
// Coverage (#1803). Which stylesheets this run actually opened, recorded at the point each
// one is read rather than derived from the same <link>-following logic being checked -- a
// coverage report computed from the scan's own rules can only ever agree with itself.
const readSheets = new Set();

// ---------------------------------------------------------------------------
// The print scope (#1802).
//
// The body of every @media print block, brace-MATCHED rather than regex-terminated. The
// obvious /@media print\s*\{[^}]*\}/ stops at the first inner closing brace, which is the end
// of the @page rule -- so it would read one of the twelve rules in dashboard.css's block and
// report the other eleven as absent. That is the shape of failure this whole file exists to
// catch, so it is worth not committing here.
//
// The prelude is matched loosely, so `@media screen, print` and `@media only print` are found
// as well as the bare form. `@media not print` would be a false positive; there is none in the
// tree, and a false positive here is loud rather than silent.
function printBlocks(css) {
  const bodies = [];
  const open = /@media[^{}]*\bprint\b[^{}]*\{/g;
  for (let m; (m = open.exec(css));) {
    let depth = 1;
    let i = open.lastIndex;
    for (; i < css.length && depth > 0; i++) {
      if (css[i] === '{') depth++;
      else if (css[i] === '}') depth--;
    }
    bodies.push(css.slice(open.lastIndex, i - 1));
    open.lastIndex = i;
  }
  return bodies;
}

// The label a print finding is resolved against when the file belongs to the shared scope.
const SHARED_SCOPE = '(the shared themes)';

const printScans = [];
let printBlocksSeen = 0;
let printDeclsSeen = 0;

// Called wherever a file is read for references, with the scope that file resolves against --
// SHARED_SCOPE, or the page path for a self-contained document. Same text, same trip through
// the file: a separate walk would be a second membership rule to keep in step with the first.
function recordPrint(fileRel, text, scope) {
  for (const body of printBlocks(text)) {
    printBlocksSeen++;
    printDeclsSeen += [...body.matchAll(/[a-z-]+\s*:\s*[^;{}]/g)].length;
    const tokens = new Set();
    for (const m of body.matchAll(VAR_REF)) tokens.add(m[1]);
    printScans.push({ file: fileRel, scope, tokens });
  }
}

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
      const css = read(full);
      addRefs(refs, css, basename(full));
      recordPrint(relative(process.cwd(), full), css, SHARED_SCOPE);
    }
  };
  walk(UI_SRC);

  // V1 is one file rather than a tree, so it is read directly rather than walked.
  const v1 = read(V1_CSS);
  addRefs(refs, v1, basename(V1_CSS));
  recordPrint(relative(process.cwd(), V1_CSS), v1, SHARED_SCOPE);
  readSheets.add(relative(process.cwd(), V1_CSS));

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
// Scripts and stylesheets are found through the page that loads them rather than by globbing
// *.js and *.css, so an asset belonging to a self-contained page is not silently resolved
// against the shared themes. The cost is that an asset NO page links is read by neither scope;
// that gap is pinned by a case in tests/hooks/test-theme-tokens.sh rather than described here,
// because a comment does not fail when the gap closes or widens.
function collectMarkupReferences(refs) {
  const scanned = [];
  const documents = [];

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
        documents.push(collectDocumentScope(rel, html));
        continue;
      }

      const files = [rel];
      addRefs(refs, html, basename(full));
      recordPrint(rel, html, SHARED_SCOPE);

      // Stylesheets as well as scripts, as of #1803. Following only <script src> left
      // static/shared/a11y.css read by nothing at all: dashboard.html links it, the shared
      // scope read dashboard.html, and the link was never followed. It happened to be
      // self-consistent; nothing checked that it was.
      for (const sheet of linkedAssets(html, STYLE_HREF)) {
        if (readSheets.has(sheet.rel)) continue;
        const css = read(sheet.path);
        addRefs(refs, css, basename(sheet.path));
        recordPrint(sheet.rel, css, SHARED_SCOPE);
        readSheets.add(sheet.rel);
        files.push(sheet.rel);
      }
      for (const script of linkedAssets(html, SCRIPT_SRC)) {
        const js = read(script.path);
        addRefs(refs, js, basename(script.path));
        recordPrint(script.rel, js, SHARED_SCOPE);
        files.push(script.rel);
      }
      scanned.push(files);
    }
  };
  walk(SERVER_DIR);

  return { scanned, documents };
}

// A page that does not link the shared themes still has a definition source: the tokens it
// defines itself. Resolving it against THOSE, rather than against nothing, is the whole of
// #1784. The membership rule that sorts pages into the two scopes is untouched -- checking a
// self-contained page against the shared themes would report every one of its own properties
// as undefined, which is why it was held out in the first place and why it must stay held out.
//
// The model is the one scripts/check-css-modifiers.cjs already uses for class names (#1744):
// each document gets the definitions it actually links, and nothing else. It needs no exclusion
// list, and it covers the standalone error pages and the localized legal templates in the same
// pass as setup.css.
//
// Definitions come from the page's own <style> blocks and from the stylesheets it links.
// References come from those, from the whole document (see the note above collectMarkupReferences
// for why the whole document and not just style attributes), and from the scripts it loads --
// a script is reached through the page that loads it, so a script belonging to a self-contained
// page is resolved against that page's tokens rather than against the shared themes.
function collectDocumentScope(rel, html) {
  const decls = new Set();
  const values = new Map();
  const refs = new Map();
  const files = [rel];

  for (const m of html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/gi)) {
    addDecls(decls, m[1]);
    addValues(values, m[1], rel);
  }
  addRefs(refs, html, rel);
  recordPrint(rel, html, rel);

  for (const sheet of linkedAssets(html, STYLE_HREF)) {
    const css = read(sheet.path);
    addDecls(decls, css);
    addValues(values, css, sheet.rel);
    addRefs(refs, css, sheet.rel);
    recordPrint(sheet.rel, css, rel);
    readSheets.add(sheet.rel);
    files.push(sheet.rel);
  }
  for (const script of linkedAssets(html, SCRIPT_SRC)) {
    const js = read(script.path);
    addRefs(refs, js, script.rel);
    recordPrint(script.rel, js, rel);
    files.push(script.rel);
  }

  return { page: rel, files, decls, values, refs };
}

// Properties defined per theme file, split by whether they sit on a bare :root (inherited
// by every theme) or inside a theme-specific selector.
function collectDefinitions() {
  const base = new Set();
  const perTheme = new Map();
  const values = new Map();
  for (const file of readdirSync(THEMES)) {
    if (!file.endsWith('.css') || file === 'index.css') continue;
    const full = join(THEMES, file);
    const css = read(full);
    readSheets.add(relative(process.cwd(), full));
    addValues(values, css, file);
    recordPrint(relative(process.cwd(), full), css, SHARED_SCOPE);
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
  return { base, perTheme, values };
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

// Definitions first, so the theme files are in readSheets before any page's <link rel=
// stylesheet> is followed (#1803). A themed page links the themes as well as its own
// stylesheet, and the themes are the definition source rather than a consumer of it -- they
// are deliberately outside the reference scan. Skipping an already-read sheet is what keeps
// them out, so they have to be marked read before the link-following starts.
const { base, perTheme, values: themeValues } = collectDefinitions();
const refs = collectReferences();
const { scanned, documents } = collectMarkupReferences(refs);

// Every first-party stylesheet under pkg/server, whether or not anything reached it. The
// difference between this and readSheets is the whole of #1803.
function allServerStylesheets() {
  const found = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) =>
      a.name.localeCompare(b.name),
    )) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        // Generated, not authored: content-hashed bundles that are never committed (#1196).
        if (entry.name !== 'ui-dist') walk(full);
        continue;
      }
      if (entry.name.endsWith('.css'))
        found.push(relative(process.cwd(), full));
    }
  };
  walk(SERVER_DIR);
  return found;
}

// A document is only worth printing individually if it has something to resolve. The rest are
// counted, so "nothing to check" stays visible without burying the pages that do have tokens.
const resolved = documents.filter((d) => d.refs.size > 0);
const tokenless = documents.filter((d) => d.refs.size === 0);

// Printed on every run, pass or fail. A gate that quietly narrows its own scope reads exactly
// like one that is passing, which is how #1774 stayed hidden: saying out loud which pages were
// checked and which were not is the only thing that makes the coverage reviewable.
console.log(
  'Pages linking the shared themes (markup, scripts and linked stylesheets scanned):',
);
for (const files of scanned) console.log(`  ${files.join('  +  ')}`);

console.log(
  '\nPages that do not link the shared themes, so each is resolved against the tokens it\n' +
    'defines itself -- its own <style> blocks plus the stylesheets it links (#1784):',
);
for (const d of resolved) {
  console.log(
    `  ${d.files.join('  +  ')}  [${d.decls.size} defined, ${d.refs.size} referenced]`,
  );
}
if (tokenless.length > 0) {
  console.log(
    `  ${tokenless.length} further pages reference no custom property at all.`,
  );
}
console.log('');

// Anti-vacuity, reference side. Every scope in this file finds its work with VAR_REF, so if
// that pattern stops matching, all three go quiet together and the run ends on the success
// message having resolved nothing. It is the one guard the two above cannot give: they prove a
// scope was ROUTED files, not that anything was read out of them. It also covers the print
// scope's reference half, which has no in-script guard of its own once the tree is clean --
// the correct end state there is zero references, so "the print scan found a reference" cannot
// be asserted here and is asserted in tests/hooks/test-theme-tokens.sh instead.
if (refs.size === 0) {
  console.error(
    '❌ Not one var() reference was found in any stylesheet, page or script, which cannot\n' +
      'be true of this tree. VAR_REF has stopped matching, and every check below it is now\n' +
      'resolving an empty set and passing.',
  );
  process.exit(1);
}

// Anti-vacuity, both scopes. Membership is derived from a <link> in the markup, so deleting that
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

// The document scope has its own way of emptying itself, and it is quieter than the one above:
// if the declaration pattern stops matching, every page reports its own tokens as undefined and
// the gate goes loudly red -- but if the REFERENCE side stops matching, or no page is routed
// here at all, the scope resolves nothing and still exits 0. Require at least one page that both
// defines and references a property, which is the only state that proves both halves ran.
if (!documents.some((d) => d.decls.size > 0 && d.refs.size > 0)) {
  console.error(
    '\u274c No self-contained page was resolved against its own tokens, so the document scan\n' +
      'covered nothing. Either every page now links the shared themes -- in which case delete\n' +
      'this scope rather than leaving it reporting success over zero files -- or the pattern\n' +
      "that finds a page's own <style> block or its linked stylesheet has stopped matching.",
  );
  process.exit(1);
}

// Anti-vacuity, print scope. Its findings are expected to be ZERO on a healthy tree, so the
// scope reports exactly the same thing whether it is working or has stopped extracting blocks
// altogether. Both halves of the extraction are asserted: that a block was found at all, and
// that the brace-matching produced a body with declarations in it rather than an empty slice.
if (printBlocksSeen === 0 || printDeclsSeen === 0) {
  console.error(
    `❌ The @media print scan found ${printBlocksSeen} blocks containing ` +
      `${printDeclsSeen} declarations, so it covered nothing.\n` +
      'Either every print block has been deleted -- in which case delete this scope rather\n' +
      'than leaving it reporting success over zero files -- or printBlocks() has stopped\n' +
      'matching the prelude, or its brace-matching is returning empty bodies. A print rule is\n' +
      'invisible to every other test in this repo and to review, so a scan of nothing here is\n' +
      'indistinguishable from a clean one.',
  );
  process.exit(1);
}

// Coverage (#1803). A stylesheet nothing links is read by neither scope, so it could reference
// any undefined property at all and every check in this file would stay green. That was true of
// static/offline.css (linked by no page) and static/shared/a11y.css (linked by dashboard.html,
// whose link was not followed) until this run. Asserted rather than described: prose in a
// script does not fail when the gap widens, and #1784 could only pin this one in a test.
// Findings are accumulated and reported together rather than exiting at the first one. Each of
// the four checks below answers a different question, so stopping at the first would report one
// instance of one class and hide the rest -- and enumerating the whole class in a single run is
// the point of widening a gate at all (#1744).
let failed = false;

const unread = allServerStylesheets().filter((f) => !readSheets.has(f));
if (unread.length > 0) {
  failed = true;
  console.error('❌ Stylesheets under pkg/server that no scope read:\n');
  for (const f of unread) console.error(`  ${f}`);
  console.error(
    '\nCoverage here is derived from a <link>: a stylesheet is read because some page pulls it\n' +
      'in, which is what keeps a self-contained page from being resolved against themes it does\n' +
      'not load. The cost is that an unlinked file is checked by nothing. Either link it from\n' +
      'the page that needs it, or delete it -- an asset no page can reach is not serving anyone,\n' +
      'and leaving it here means the next property added to it is unchecked.\n',
  );
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

// The same resolution failure, one document at a time. A page's own token set is flat -- there
// is no theme file to be complete across -- so the question here is only whether the property is
// defined at all. Deliberately narrower than the shared check above, and asserted as narrower in
// tests/hooks/test-theme-tokens.sh so widening it stays a decision rather than an accident.
const docFindings = [];
for (const d of documents) {
  const missing = [...d.refs]
    .filter(([name]) => !d.decls.has(name))
    .sort(([a], [b]) => a.localeCompare(b));
  if (missing.length > 0) docFindings.push({ d, missing });
}

// The print scope (#1802). A different question from every check above: not "does this token
// resolve" but "does its value depend on the screen theme". Both print blocks in this repo sit
// under print-color-adjust: exact, which is an instruction to reproduce the screen colour on
// paper -- so a token with one value in dark.css and another in light.css means the printout
// follows whichever theme the reader happened to be using. Paper is not themed.
//
// A token defined identically everywhere is fine and is not reported: --spacing-2xs and the
// other geometry tokens are single-valued, and a print block is welcome to use them.
const printFindings = [];
for (const scan of printScans) {
  const source =
    scan.scope === SHARED_SCOPE
      ? themeValues
      : documents.find((d) => d.page === scan.scope)?.values;
  if (!source) continue;
  for (const token of [...scan.tokens].sort()) {
    const byValue = source.get(token);
    // Undefined here rather than single-valued: the resolution checks above own that case,
    // and reporting it twice would make one fix look like two.
    if (!byValue || byValue.size < 2) continue;
    printFindings.push({ file: scan.file, scope: scan.scope, token, byValue });
  }
}

if (printFindings.length > 0) {
  failed = true;
  console.error(
    '❌ Colours inside @media print that follow the screen theme:\n',
  );
  for (const { file, scope, token, byValue } of printFindings) {
    console.error(`  ${file}  ${token}  (resolved against ${scope})`);
    for (const { display, sources } of byValue.values())
      console.error(`      ${display}  — ${[...sources].join(', ')}`);
  }
  console.error(
    '\nThese blocks set print-color-adjust: exact, so the browser reproduces whatever this\n' +
      'resolves to. On a dark theme that is a near-black page, which is #1221. Use a literal\n' +
      'colour chosen for paper -- #ffffff and #000000 -- as ui/src/index.css already does for\n' +
      'the body rule. Note that pointing the declaration at a token that does not exist is NOT\n' +
      'a fix even though it prints correctly: the declaration is merely invalid, and the next\n' +
      'person to define that name turns white paper black again.',
  );
}

if (undefinedTokens.length > 0) {
  failed = true;
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
}

if (docFindings.length > 0) {
  failed = true;
  console.error(
    '❌ CSS custom properties a self-contained page references but does not define:\n',
  );
  for (const { d, missing } of docFindings) {
    console.error(`  ${d.page}`);
    for (const [name, info] of missing) {
      const fb = info.hasFallback
        ? ' — it has a fallback, so it renders, but never follows the page'
        : ' — the declaration is invalid and the browser drops it silently';
      console.error(`    ${name}${fb}`);
      console.error(`        referenced in: ${[...info.files].join(', ')}`);
    }
  }
  console.error(
    '\nThis page carries its own token set, so define it there, or correct the name to one\n' +
      'the page already defines. An unresolved var() is not a no-op: the whole declaration\n' +
      'becomes invalid at computed-value time and takes its UNSET value, which is `inherit`\n' +
      'for color and the initial value -- transparent, none, medium -- for everything else.',
  );
}

if (failed) process.exit(1);

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
console.log(
  `✅ All ${readSheets.size} stylesheets under pkg/server were read by some scope.`,
);
console.log(
  `✅ No colour in the ${printBlocksSeen} @media print blocks follows the screen theme.`,
);
