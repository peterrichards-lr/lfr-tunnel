'use strict';
/**
 * One collection pass over Portal V1, with two consumers (#1841).
 *
 * scripts/check-css-modifiers.cjs and scripts/check-theme-tokens.mjs ask different questions --
 * "is this class defined anywhere the page can reach" and "does this custom property resolve" --
 * of the same body of source. They need the same INPUT to ask them: every class and every
 * custom property Portal V1 actually uses, from markup, from inline `style` attributes, from
 * `class`/`className`/`classList` in script, and from the stylesheets and scripts each page
 * links.
 *
 * They each grew their own collector, and each missed a different half:
 *
 *   #1744  check-css-modifiers.cjs read Portal V2 only. Pointed at V1 it found 25 undefined
 *          classes, one of them `action-menu-content` -- a typo that left the vanity-domain
 *          menu permanently open in production.
 *   #1774  check-theme-tokens.mjs read stylesheets only. Sixteen inline styles referenced
 *          --text / --text-color, which no theme defines.
 *
 * Both were widened afterwards, separately, which left two collectors over one corpus and the
 * next blind spot free to land in one of them. Two more were sitting there when this file was
 * written, each visible to exactly one gate:
 *
 *   - documents in SUBDIRECTORIES of pkg/server. check-css-modifiers listed pkg/server/*.html
 *     and pkg/server/static/*.html, non-recursive, so all 34 localized templates were invisible
 *     to it while check-theme-tokens walked the tree and read every one.
 *   - stylesheets and scripts linked by a RELATIVE href. check-theme-tokens matched only
 *     `href="/static/…"`, so it never opened one; check-css-modifiers resolved them fine.
 *
 * Neither was an open defect -- nothing in the tree uses either shape today -- and that is the
 * point. A collector cannot test for input it never learned to find, so the gap only becomes
 * visible when someone writes the construct, and by then it is a production bug rather than a
 * red build. tests/hooks/test-v1-usage-parity.sh plants a class AND a property in each shape
 * and requires both gates to react.
 *
 * A third was found the same way in #1779, and unlike those two it was live: every script-only
 * shape -- `.className =`, `.classList.add()`, and the selector read-back that marks a class as
 * a behaviour hook rather than a styling one -- was reached only through a `<script src>`. A
 * page keeping its behaviour in an INLINE `<script>` had all three invisible. pkg/client's
 * dashboard is 1600 lines of exactly that, and pkg/server/static/setup.html toggles a class the
 * same way. Both halves matter: without the hook half, widening the corpus reports a handle the
 * page queries as an undefined class.
 *
 * WHAT COUNTS AS THE CORPUS is the caller's question, not this module's. It walks one web root
 * per call; the gates name their roots and concatenate. That list -- `pkg/server` and
 * `pkg/client` -- is asserted in tests/hooks/test-gate-scope-boundaries.sh from both directions,
 * so a document inside it is read and a document outside it is not, and changing either is a
 * decision rather than a side effect (#1779).
 *
 * WHAT IS SHARED AND WHAT IS NOT. This module collects USAGE and the corpus it comes from: the
 * documents, the assets each one links, and every class and property referenced across them.
 * It deliberately does NOT resolve anything. Which classes count as defined, which themes must
 * carry a property, what a print block is allowed to depend on -- those are each gate's own
 * question, they disagree about the answers, and lowering them to a common denominator would
 * be the failure this refactor exists to avoid. Definitions stay with the gate that owns them.
 *
 * COMMENT POLICY, which differs by language and is deliberate:
 *
 *   .html   C-style block comments AND `<!-- -->` are stripped. Commented-out markup is not
 *           live styling; prose describing a token would otherwise register as a reference.
 *   .css    block comments stripped, same reason.
 *   .js     block comments stripped, `//` NOT stripped. A `//` cannot be removed safely --
 *           a URL inside a string literal would take the rest of its line with it and hide any
 *           real reference sharing that line. Losing coverage is worse than a false positive;
 *           both halves of that trade-off were paid for in #1774.
 */
const fs = require('fs');
const path = require('path');

// `${...}` collapses to this rather than to a space, so a name that was BUILT by interpolation
// stays distinguishable from one written out in full. `class="edge-status-dot--${status}"`
// yields a prefix, not a class, and a consumer has to be able to tell.
const DYN = '\u0000';

const VAR_REF = /var\(\s*(--[a-z0-9-]+)\s*(,)?/g;

// Anchored on the character that can legally precede an attribute name, so `data-class=` and
// `className=` are not mistaken for it.
const CLASS_ATTR = /(?<![-\w])class\s*=\s*(?:"([^"]*)"|'([^']*)')/g;

const CLASS_NAME_ASSIGN =
  /\.className\s*\+?=\s*(?:'([^']*)'|"([^"]*)"|`([^`]*)`)/g;
const CLASS_LIST_CALL =
  /\.classList\.(?:add|remove|toggle|replace)\(([^)]*)\)/g;
const STRING_LITERAL = /'([^']*)'|"([^"]*)"|`([^`]*)`/g;

// A class both applied and read back through one of these is a handle for script, not styling.
const SELECTOR_CALLS =
  /(?:querySelectorAll|querySelector|closest|matches|getElementsByClassName)\s*\(\s*(?:'([^']*)'|"([^"]*)"|`([^`]*)`)/g;

const STYLE_BLOCK = /<style[^>]*>([\s\S]*?)<\/style>/gi;
const LINK_TAG = /<link\b[^>]*>/g;
const SCRIPT_SRC = /<script\b[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')/g;

// An INLINE <script>, i.e. one with no src. Its body is script, not markup, and the two
// differ in what can be read out of them: `class="…"` inside it is already covered by the
// whole-document scan, but `.className =`, `.classList.add()` and a selector read-back are
// script-only shapes that a markup scan cannot see (#1779).
//
// The negative lookahead is on the whole tag rather than on one attribute position, because
// `src` may appear after `type`, `defer` or `nonce`.
const INLINE_SCRIPT = /<script\b(?![^>]*\bsrc\s*=)[^>]*>([\s\S]*?)<\/script>/gi;

const stripBlockComments = (text) => text.replace(/\/\*[\s\S]*?\*\//g, '');
const stripHtmlComments = (text) => text.replace(/<!--[\s\S]*?-->/g, '');

function readAsset(abs) {
  const text = fs.readFileSync(abs, 'utf8');
  if (abs.endsWith('.html')) return stripHtmlComments(stripBlockComments(text));
  return stripBlockComments(text);
}

function lineAt(text, index) {
  return text.slice(0, index).split('\n').length;
}

function splitClassLiteral(literal) {
  return literal.replace(/\$\{[^}]*\}/g, DYN).split(/\s+/);
}

/**
 * Resolve an href/src written in a page to a first-party file on disk.
 *
 * Returns null for anything the repository does not contain: a scheme-relative or absolute URL,
 * a data: URI, an empty href, a path that escapes the web root, or a file that is not there.
 * Both `/static/x.css` (served path, resolved against the web root) and `x.css` (relative to the
 * document) are handled -- each gate previously understood one of those and not the other.
 */
function resolveAsset(href, docPath, webRoot) {
  if (/^(?:[a-z][a-z0-9+.-]*:)?\/\//i.test(href)) return null;
  if (/^data:/i.test(href)) return null;
  const clean = href.split(/[?#]/)[0];
  if (!clean) return null;
  const abs = clean.startsWith('/')
    ? path.join(webRoot, clean.slice(1))
    : path.resolve(path.dirname(docPath), clean);
  const rel = path.relative(webRoot, abs);
  if (rel.startsWith('..') || path.isAbsolute(rel)) return null;
  return abs;
}

// Sorted, and directories recursed in place, so the corpus is in one deterministic order for
// both consumers regardless of what the filesystem hands back.
function walkHtml(dir, out = []) {
  if (!fs.existsSync(dir)) return out;
  const entries = fs
    .readdirSync(dir, { withFileTypes: true })
    .sort((a, b) => a.name.localeCompare(b.name));
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // Generated, not authored: content-hashed bundles that are never committed (#1196).
      if (entry.name !== 'ui-dist') walkHtml(full, out);
      continue;
    }
    if (entry.name.endsWith('.html')) out.push(full);
  }
  return out;
}

function collectClassUses(text, file, into) {
  for (const m of text.matchAll(CLASS_ATTR)) {
    const line = lineAt(text, m.index);
    for (const tok of splitClassLiteral(m[1] ?? m[2]))
      if (tok) into.push({ name: tok, file, line, source: 'class-attribute' });
  }
}

// The two shapes only script has. Split out from collectScriptClassUses so an INLINE <script>
// can be scanned for them WITHOUT re-collecting its `class="…"` template literals, which the
// whole-document scan has already read (#1779). Double-counting those would not change any
// gate's verdict, but it would double every occurrence count in the report.
//
// `line` is relative to whatever text is passed in; callers scanning an inline block pass an
// offset so the number is the document's.
function collectScriptOnlyClassUses(js, file, into, lineOffset = 0) {
  for (const m of js.matchAll(CLASS_NAME_ASSIGN)) {
    const line = lineAt(js, m.index) + lineOffset;
    for (const tok of splitClassLiteral(m[1] ?? m[2] ?? m[3]))
      if (tok) into.push({ name: tok, file, line, source: 'className' });
  }
  for (const m of js.matchAll(CLASS_LIST_CALL)) {
    const line = lineAt(js, m.index) + lineOffset;
    for (const lit of m[1].matchAll(STRING_LITERAL)) {
      for (const tok of splitClassLiteral(lit[1] ?? lit[2] ?? lit[3]))
        if (tok) into.push({ name: tok, file, line, source: 'classList' });
    }
  }
}

// A class both applied and read back through a selector is a handle for script, not styling.
// Extracted as a function so an inline <script> feeds the same set an external one does.
function collectClassHooks(js, into) {
  for (const m of js.matchAll(SELECTOR_CALLS)) {
    const sel = m[1] ?? m[2] ?? m[3];
    for (const c of sel.matchAll(/\.(-?[A-Za-z][-\w]*)/g)) into.add(c[1]);
  }
}

function collectScriptClassUses(js, file, into) {
  // Portal V1 renders most of its tables from template literals, so `class="…"` inside the
  // script is markup and is collected by the same rule as the page's own.
  collectClassUses(js, file, into);
  collectScriptOnlyClassUses(js, file, into);
}

function collectTokenRefs(text, file, source, into) {
  for (const m of text.matchAll(VAR_REF)) {
    into.push({
      name: m[1],
      hasFallback: Boolean(m[2]),
      file,
      line: lineAt(text, m.index),
      source,
    });
  }
}

/**
 * Walk Portal V1 once and return what it uses.
 *
 * @param {object} options
 * @param {string} options.webRoot  the directory the server serves V1 from (pkg/server)
 * @param {string} options.repoRoot the path file references are reported relative to
 * @returns {{documents: Array, webRoot: string}}
 *
 * Each document carries:
 *   page          repo-relative path of the .html
 *   markup        its text, comments stripped
 *   styleBlocks   the bodies of its inline <style> elements
 *   stylesheets   [{abs, rel, text}] for every first-party <link rel=stylesheet> that exists
 *   scripts       [{abs, rel, text}] for every first-party <script src> that exists
 *   missingAssets [{href, kind}] for a link or src that resolves inside the tree but is absent
 *   classUses     [{name, file, line, source}] -- name may contain DYN; `source` is
 *                 class-attribute, className or classList, and the last two come from the
 *                 document's linked scripts AND its inline <script> blocks
 *   classHooks    Set of class names this document's scripts -- linked or inline -- read back
 *                 through a selector
 *   tokenRefs     [{name, hasFallback, file, line, source}]
 */
function collectV1Usage({ webRoot, repoRoot }) {
  const rel = (p) => path.relative(repoRoot, p);
  const documents = [];

  for (const doc of walkHtml(webRoot)) {
    const markup = readAsset(doc);
    const docRel = rel(doc);

    const styleBlocks = [];
    for (const m of markup.matchAll(STYLE_BLOCK)) styleBlocks.push(m[1]);

    const stylesheets = [];
    const scripts = [];
    const missingAssets = [];
    const seenAssets = new Set();

    for (const tag of markup.matchAll(LINK_TAG)) {
      if (!/\brel\s*=\s*(['"])stylesheet\1/i.test(tag[0])) continue;
      const href = /\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')/.exec(tag[0]);
      if (!href) continue;
      const raw = href[1] ?? href[2];
      const abs = resolveAsset(raw, doc, webRoot);
      if (!abs) continue; // third-party, or outside the tree: not ours to read
      if (seenAssets.has(abs)) continue;
      seenAssets.add(abs);
      if (!fs.existsSync(abs)) {
        missingAssets.push({ href: raw, kind: 'stylesheet' });
        continue;
      }
      stylesheets.push({ abs, rel: rel(abs), text: readAsset(abs) });
    }

    for (const m of markup.matchAll(SCRIPT_SRC)) {
      const raw = m[1] ?? m[2];
      const abs = resolveAsset(raw, doc, webRoot);
      if (!abs) continue;
      if (seenAssets.has(abs)) continue;
      seenAssets.add(abs);
      if (!fs.existsSync(abs)) {
        missingAssets.push({ href: raw, kind: 'script' });
        continue;
      }
      scripts.push({ abs, rel: rel(abs), text: readAsset(abs) });
    }

    const classUses = [];
    const classHooks = new Set();
    const tokenRefs = [];

    collectClassUses(markup, docRel, classUses);
    collectTokenRefs(markup, docRel, 'markup', tokenRefs);

    for (const sheet of stylesheets)
      collectTokenRefs(sheet.text, sheet.rel, 'stylesheet', tokenRefs);

    for (const script of scripts) {
      collectScriptClassUses(script.text, script.rel, classUses);
      collectTokenRefs(script.text, script.rel, 'script', tokenRefs);
      collectClassHooks(script.text, classHooks);
    }

    // The inline <script> blocks (#1779). Until this, every script-only shape was reached
    // through a <script src>, so a page that keeps its behaviour inline had `className = …`,
    // `classList.add(…)` and its selector read-backs invisible to both gates. That is not a
    // hypothetical shape here: pkg/client/dashboard.html is one 1600-line page with all of its
    // script inline, and pkg/server/static/setup.html manipulates classList the same way.
    //
    // The cost of the gap was in BOTH directions, which is why it is worth closing rather than
    // declaring deliberate: an applied class went unchecked, AND a class read back through
    // querySelector was not recognised as a behaviour hook, so widening the corpus without this
    // would have reported `.access-control-panel` and `.public-urls-panel` as undefined when
    // they are handles the page's own script reads.
    //
    // markup, not the raw file: comments are already stripped, and the offset arithmetic below
    // is against the same text every other line number here is derived from.
    for (const m of markup.matchAll(INLINE_SCRIPT)) {
      const body = m[1];
      const offset = lineAt(markup, m.index + m[0].indexOf(body)) - 1;
      collectScriptOnlyClassUses(body, docRel, classUses, offset);
      collectClassHooks(body, classHooks);
    }

    documents.push({
      page: docRel,
      abs: doc,
      markup,
      styleBlocks,
      stylesheets,
      scripts,
      missingAssets,
      classUses,
      classHooks,
      tokenRefs,
    });
  }

  return { documents, webRoot };
}

module.exports = { collectV1Usage, DYN, VAR_REF, resolveAsset };
