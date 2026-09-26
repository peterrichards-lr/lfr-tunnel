#!/usr/bin/env node
/**
 * Fails when a portal i18n key is used but has no entry in Language.properties, and when
 * the translated bundles have drifted from the English one.
 *
 * This is the gate #1701 asked for. Both portals resolve a string through a key with an
 * inline English fallback -- `t('local_time', 'Local Time')` in V2,
 * `data-i18n="th_local_time">Local Time` in V1 -- and the fallback is what makes a missing
 * key harmless *in English* and invisible everywhere else. 477 keys had drifted out of the
 * bundle before anything noticed, because the person adding the string saw it render
 * correctly. CSS classes (`check-css-modifiers.cjs`) and theme tokens
 * (`check-theme-tokens.mjs`) already have exactly this gate for exactly this reason.
 *
 * FIVE PARSING RULES, each learned from a wrong answer this produced:
 *
 * 1. The key is the first argument of `t()` and it must be a string LITERAL. A computed key
 *    cannot be checked and is not counted; there are none today, and the gate says so rather
 *    than pretending a dynamic key is covered.
 *
 * 2. The whole file is scanned in one pass, with line numbers derived from offsets. Prettier
 *    wraps a two-argument `t()` across four lines whenever the fallback is long, so a
 *    line-by-line scan misses roughly a third of the call sites in `ui/src` -- and a gate
 *    that silently skips a third of its input reports a clean pass over a broken bundle.
 *
 * 3. `function t(key, defaultVal)` in `dashboard.js` is a declaration, not a call. Requiring
 *    a quote immediately after `(` excludes it; matching an identifier would add `key` to
 *    the used set and fail the build over a parameter name.
 *
 * 4. `offline.html` and `maintenance.html` use `data-i18n` too, and they must NOT be checked
 *    against Language.properties. They are standalone error pages that never load
 *    `/api/i18n` -- each carries its own inline `translations` object keyed by locale
 *    (`offline.html:340-397`). Checking them reports 19 keys as missing that are, in fact,
 *    already translated into all ten locales.
 *
 * 5. A fallback written as a template literal containing `${...}` is a defect, not a key.
 *    The properties value is a static string, so the moment such a key gets an entry the
 *    interpolated value silently disappears from the rendered text -- a translated portal
 *    would have shown "Your session ends in about minute." Use a `{0}` placeholder and
 *    `.replace('{0}', ...)` at the call site, the way `copy_link_to` already does.
 *
 * Get any of these wrong and the gate fails builds over strings that are perfectly fine,
 * which is worse than not having a gate at all.
 */
'use strict';
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const vm = require('vm');

const ROOT = path.join(__dirname, '..');
const I18N_DIR = path.join(ROOT, 'pkg', 'server', 'i18n');
const BASE = path.join(I18N_DIR, 'Language.properties');

// The locale set the server actually loads: READ OUT of pkg/server/i18n.go rather than copied
// from it (#1779). "Kept in step with that list deliberately" is what the comment here used to
// say, and a hand-kept copy of another file's list is kept in step until the day it is not --
// at which point a locale the server loads has an unchecked bundle and the gate reports success
// over it. This is the same derive-don't-list rule test-shell-portability.sh follows.
//
// `en` is dropped because it is Language.properties itself, which is the baseline everything
// else is compared against rather than one of the bundles being compared.
const I18N_GO = path.join(ROOT, 'pkg', 'server', 'i18n.go');
function localesFromServer() {
  const src = fs.readFileSync(I18N_GO, 'utf8');
  const m = /locales\s*:?=\s*\[\]string\{([^}]*)\}/.exec(src);
  if (!m) return null;
  return [...m[1].matchAll(/"([a-z]{2}(?:-[A-Za-z]+)?)"/g)]
    .map((x) => x[1])
    .filter((l) => l !== 'en');
}
const LOCALES = localesFromServer();
if (!LOCALES || LOCALES.length === 0) {
  console.error(
    `check-i18n-keys: could not read the locale list out of ${rel(I18N_GO)}.\n` +
      'It is derived rather than duplicated here, so a shape change in initI18n must fail this\n' +
      'gate rather than silently reduce it to checking nothing. Update the pattern above.',
  );
  process.exit(1);
}

const errors = [];

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

function rel(p) {
  return path.relative(ROOT, p);
}

// --- 1. What is defined -----------------------------------------------------------------

// Mirrors parseProperties in pkg/server/i18n.go: trimmed lines, `#`/`!` comments, first
// `=` (or `:`) separates. Anything that parser accepts is defined, and anything it drops is
// not, so this must not be more permissive than the server.
function parseProperties(file) {
  const props = new Map();
  const dupes = [];
  const text = fs.readFileSync(file, 'utf8');
  text.split('\n').forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith('#') || line.startsWith('!')) return;
    let idx = line.indexOf('=');
    if (idx === -1) idx = line.indexOf(':');
    if (idx === -1) return;
    const key = line.slice(0, idx).trim();
    const val = line.slice(idx + 1).trim();
    if (props.has(key)) dupes.push({ key, line: i + 1 });
    props.set(key, val);
  });
  return { props, dupes };
}

if (!fs.existsSync(BASE)) {
  console.error(`check-i18n-keys: ${rel(BASE)} not found`);
  process.exit(1);
}
const base = parseProperties(BASE);
if (base.props.size === 0) {
  console.error(
    'check-i18n-keys: no keys parsed from Language.properties -- the check would pass over nothing',
  );
  process.exit(1);
}

// --- 2. What is used --------------------------------------------------------------------

// Rule 3: a quote must follow `(` directly, so the `function t(key, defaultVal)` declaration
// is not read as a call.
const T_CALL =
  /\bt\(\s*(["'`])((?:\\.|(?!\1)[^\\])*)\1(\s*,\s*(["'`])((?:\\.|(?!\4)[^\\])*)\4)?/g;
// Any `data-i18n-<attr>`, not the two that existed when this was written. The value of every
// one of them is a bundle key, so naming them individually meant a newly implemented mechanism
// was invisible here: `data-i18n-title` landed in #2248 and its nine keys were neither counted
// as used nor reported as missing -- the gate read a clean pass over them.
const ATTR = /data-i18n(?:-[a-z][a-z-]*)?\s*=\s*"([^"]+)"/g;
const GO_CALL = /GetTranslation\(\s*[^,)]+,\s*"([^"]+)"/g;

const used = new Map(); // key -> [ "file:line", ... ]
const interpolated = new Map(); // key -> [ "file:line", ... ] (rule 5)

function record(map, key, file, offset, src) {
  const line = src.slice(0, offset).split('\n').length;
  if (!map.has(key)) map.set(key, []);
  map.get(key).push(`${rel(file)}:${line}`);
}

function scan(file, patterns) {
  const src = fs.readFileSync(file, 'utf8');
  for (const { re, keyGroup, fallbackGroup, quoteGroup } of patterns) {
    re.lastIndex = 0;
    for (const m of src.matchAll(re)) {
      record(used, m[keyGroup], file, m.index, src);
      // Rule 5. Only a backtick fallback can interpolate; `${` in a normal quoted string is
      // literal text and must not be reported.
      if (
        fallbackGroup &&
        m[quoteGroup] === '`' &&
        m[fallbackGroup] &&
        m[fallbackGroup].includes('${')
      ) {
        record(interpolated, m[keyGroup], file, m.index, src);
      }
    }
  }
}

const UI_SRC = path.join(ROOT, 'ui', 'src');
if (!fs.existsSync(UI_SRC)) {
  console.error(`check-i18n-keys: ${rel(UI_SRC)} not found`);
  process.exit(1);
}
const tsFiles = walk(UI_SRC).filter((f) => /\.tsx?$/.test(f));
for (const f of tsFiles) {
  scan(f, [{ re: T_CALL, keyGroup: 2, fallbackGroup: 5, quoteGroup: 4 }]);
}

// Portal V1. Rule 4 is why this is an explicit list and not a directory walk.
const V1_MARKUP = [
  path.join(ROOT, 'pkg', 'server', 'dashboard.html'),
  path.join(ROOT, 'pkg', 'server', 'static', 'setup.html'),
];
for (const f of V1_MARKUP) {
  scan(f, [{ re: ATTR, keyGroup: 1 }]);
}

// Rule 6 (#1779). An explicit list is a scope boundary, and this one was stated only as rule 4's
// prose: two files are checked against Language.properties, and every OTHER document that uses
// data-i18n is out of scope because it carries its own inline `translations` object. That is
// the right call -- checking a standalone page here reports 19 keys as missing that are in fact
// translated into ten locales -- but nothing made the list answer for the tree.
//
// So the two lists must together account for EVERY tracked document using the attribute:
//
//   * a document using data-i18n that is in neither list is unaccounted for, and the run fails.
//     That is not hypothetical -- pkg/client/dashboard.html has used the attribute for 40 keys
//     since it was written, resolves them against its own bundle, and appeared in neither list
//     nor in any comment. It could have been either kind and nobody would have been asked.
//   * an entry in SELF_CONTAINED that no longer uses the attribute -- or no longer exists --
//     also fails, so this is a ratchet rather than an exclusion list. An exclusion that outlives
//     its subject silently exempts whatever lands at that path next.
//
// It also asserts, since #1854, that each self-contained page's own bundle is internally
// consistent -- the same absent/extra/placeholder comparison the locale files get, with the
// page's own `en` as the baseline. The paragraph below describes the state before that.
// Measured while writing this: the client inspector's bundle has four keys (client_replay_req,
// client_req, client_resp, client_replaying) only in `en`, so five locales fall back to English
// -- exactly the drift this gate exists for, one bundle out of its reach. Filed as #1854 rather
// than bolted on here, because "which bundle governs this page" is a different question from
// "do these keys resolve" and answering both in one pass is how the collector duplication in
// #1841 started.
const SELF_CONTAINED = [
  // Standalone error pages: never load /api/i18n, each carries its own `translations` object
  // keyed by locale (offline.html:340-397). This is rule 4 above, stated as data.
  path.join(ROOT, 'pkg', 'server', 'static', 'offline.html'),
  path.join(ROOT, 'pkg', 'server', 'static', 'maintenance.html'),
  // The client inspector. Served by the client binary on localhost, with no server to ask, so
  // its `clientTranslations` object is the only bundle it can have.
  path.join(ROOT, 'pkg', 'client', 'dashboard.html'),
];

// Documents only. dashboard.js is scanned too, but it is not a document and does not appear in
// the listing below, so including it here would be a set member that can never be matched.
const SCANNED_MARKUP = new Set(V1_MARKUP);
const declaredSelfContained = new Set(SELF_CONTAINED);
const htmlFiles = execFileSync('git', ['ls-files', '*.html', '*.htm'], {
  cwd: ROOT,
  encoding: 'utf8',
})
  .split('\n')
  .filter(Boolean)
  .map((f) => path.join(ROOT, f));

const usesAttr = (f) =>
  fs.existsSync(f) &&
  /data-i18n(?:-[a-z-]+)?\s*=/.test(fs.readFileSync(f, 'utf8'));

const unaccounted = htmlFiles.filter(
  (f) => usesAttr(f) && !SCANNED_MARKUP.has(f) && !declaredSelfContained.has(f),
);
const staleSelfContained = SELF_CONTAINED.filter((f) => !usesAttr(f));

if (unaccounted.length > 0) {
  errors.push(
    `${unaccounted.length} document(s) use data-i18n but are in neither scan list:\n` +
      unaccounted.map((f) => `  ${rel(f)}`).join('\n') +
      '\n\n  Decide which it is and say so in code. If the page resolves its keys through' +
      '\n  /api/i18n, add it to V1_MARKUP so they are checked against Language.properties. If it' +
      '\n  carries its own inline bundle, add it to SELF_CONTAINED with the reason. Leaving it in' +
      '\n  neither means nothing has ever checked its keys and nothing ever asked why.',
  );
}
if (staleSelfContained.length > 0) {
  errors.push(
    `${staleSelfContained.length} stale SELF_CONTAINED entr(ies) -- no data-i18n attribute there any more:\n` +
      staleSelfContained.map((f) => `  ${rel(f)}`).join('\n') +
      '\n\n  Remove them. The list is a ratchet: an exemption that outlives its subject silently' +
      '\n  exempts whatever is written at that path next.',
  );
}
const V1_SCRIPT = path.join(ROOT, 'pkg', 'server', 'static', 'dashboard.js');
scan(V1_SCRIPT, [
  { re: T_CALL, keyGroup: 2, fallbackGroup: 5, quoteGroup: 4 },
  { re: ATTR, keyGroup: 1 },
]);

// The server resolves a handful of keys itself, for email subjects and the legal pages. A
// miss there is worse than in the portal: GetTranslation's last fallback is the key itself,
// so an absent entry ships an email whose subject line reads `invite_subject`.
const goFiles = execFileSync(
  'git',
  ['ls-files', 'pkg/**/*.go', 'cmd/**/*.go'],
  { cwd: ROOT, encoding: 'utf8' },
)
  .split('\n')
  .filter((f) => f && !f.endsWith('_test.go'));
for (const f of goFiles) {
  scan(path.join(ROOT, f), [{ re: GO_CALL, keyGroup: 1 }]);
}

if (used.size === 0) {
  console.error(
    'check-i18n-keys: no i18n keys found in the sources -- the check would pass over nothing',
  );
  process.exit(1);
}

// --- 3. The checks ----------------------------------------------------------------------

const missing = [...used.keys()].filter((k) => !base.props.has(k)).sort();
if (missing.length) {
  const total = missing.reduce((s, k) => s + used.get(k).length, 0);
  errors.push(
    `${missing.length} key(s) used but not defined in ${rel(BASE)}, ${total} occurrence(s):\n` +
      missing
        .map((k) => {
          const where = used.get(k);
          const shown = where.slice(0, 3).map((w) => `      ${w}`);
          if (where.length > 3)
            shown.push(`      …and ${where.length - 3} more`);
          return `  ${k}  (${where.length})\n${shown.join('\n')}`;
        })
        .join('\n') +
      `\n\n  Add each key to ${rel(BASE)} using the English fallback already in the source,` +
      `\n  then translate it in Language_<locale>.properties for every locale.`,
  );
}

if (interpolated.size) {
  errors.push(
    `${interpolated.size} key(s) whose fallback interpolates a value with \${...}:\n` +
      [...interpolated.entries()]
        .sort()
        .map(([k, w]) => `  ${k}\n      ${w.join('\n      ')}`)
        .join('\n') +
      `\n\n  A properties value is a static string, so once such a key has an entry the` +
      `\n  interpolated value silently disappears from the translated text. Put a {0}` +
      `\n  placeholder in the fallback and .replace('{0}', …) at the call site instead.`,
  );
}

if (base.dupes.length) {
  errors.push(
    `${base.dupes.length} duplicate key(s) in ${rel(BASE)}:\n` +
      base.dupes.map((d) => `  ${d.key}  (line ${d.line})`).join('\n') +
      `\n\n  The later entry wins silently, so a duplicate is a translation nobody will ever see.`,
  );
}

// Placeholders are positional and the code substitutes them by literal text, so a locale
// that drops or renames one produces a string with a hole in it at runtime.
const PLACEHOLDER = /\{[0-9]+\}/g;
function placeholders(v) {
  return [...new Set(v.match(PLACEHOLDER) || [])].sort();
}

for (const locale of LOCALES) {
  const file = path.join(I18N_DIR, `Language_${locale}.properties`);
  if (!fs.existsSync(file)) {
    errors.push(
      `${rel(file)} is missing, but pkg/server/i18n.go loads locale "${locale}"`,
    );
    continue;
  }
  const { props, dupes } = parseProperties(file);
  const absent = [...base.props.keys()].filter((k) => !props.has(k));
  const extra = [...props.keys()].filter((k) => !base.props.has(k));
  const holes = [];
  for (const [k, v] of props) {
    if (!base.props.has(k)) continue;
    const want = placeholders(base.props.get(k)).join(' ');
    const got = placeholders(v).join(' ');
    if (want !== got)
      holes.push(
        `  ${k}: English has [${want || 'none'}], ${locale} has [${got || 'none'}]`,
      );
  }
  if (dupes.length)
    errors.push(
      `${dupes.length} duplicate key(s) in ${rel(file)}:\n` +
        dupes.map((d) => `  ${d.key}  (line ${d.line})`).join('\n'),
    );
  if (absent.length)
    errors.push(
      `${absent.length} key(s) in ${rel(BASE)} have no ${locale} translation:\n` +
        absent
          .slice(0, 20)
          .map((k) => `  ${k}`)
          .join('\n') +
        (absent.length > 20 ? `\n  …and ${absent.length - 20} more` : ''),
    );
  if (extra.length)
    errors.push(
      `${extra.length} key(s) in ${rel(file)} are not in ${rel(BASE)}:\n` +
        extra
          .slice(0, 20)
          .map((k) => `  ${k}`)
          .join('\n') +
        (extra.length > 20 ? `\n  …and ${extra.length - 20} more` : '') +
        `\n\n  English is the source of truth; a key only a translation has is dead weight` +
        `\n  that no locale except that one can ever resolve.`,
    );
  if (holes.length)
    errors.push(
      `${holes.length} placeholder mismatch(es) in ${rel(file)}:\n` +
        holes.join('\n'),
    );
}

// -- Self-contained bundles (#1854).
//
// The three pages above never load /api/i18n, so Language_<locale>.properties says nothing about
// them and the comparison above cannot reach them. Nothing compared a self-contained page's own
// locales against its own English, and one had already drifted: the client inspector's bundle had
// four keys (client_replay_req, client_req, client_resp, client_replaying) in `en` only, so five
// locales fell back to English for them. Twenty missing translations, invisible -- because t()
// falls back silently, which is #1701's defect exactly, in the bundles that gate could not see.
//
// Parsed by brace-matching and evaluating, not by regex: these pages carry markup inside string
// literals, and both key styles appear (quoted in the client inspector, bare in the error pages).
// A regex that half-works here is worse than none -- it would report a clean comparison over the
// part it managed to parse.

// braceMatch returns the index of the `}` closing the `{` at start, skipping over string
// literals so that a brace inside translated markup does not end the object early.
function braceMatch(src, start) {
  let depth = 0;
  let quote = null;
  let escaped = false;
  for (let i = start; i < src.length; i++) {
    const c = src[i];
    if (quote) {
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      quote = c;
      continue;
    }
    if (c === '{') depth++;
    else if (c === '}') {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

// Accepts `translations` and `clientTranslations` alike. Named by suffix rather than by an exact
// list so that a page naming its bundle something else is still found -- and if it is not found,
// the floor below fails the run rather than skipping the page.
const BUNDLE_DECL =
  /(?:const|let|var)\s+((?:[A-Za-z_$][\w$]*)?[Tt]ranslations)\s*=\s*\{/;

let selfContainedKeys = 0;
for (const file of SELF_CONTAINED) {
  if (!fs.existsSync(file)) continue; // already reported by the staleSelfContained ratchet
  const src = fs.readFileSync(file, 'utf8');

  const decl = src.match(BUNDLE_DECL);
  if (!decl) {
    errors.push(
      `${rel(file)} is declared self-contained but no inline translations object was found.\n` +
        `  A self-contained page's bundle IS its i18n; if it moved or was renamed, this check\n` +
        `  stops looking at the page entirely, which is worse than the drift it exists to catch.`,
    );
    continue;
  }

  const open = src.indexOf('{', decl.index);
  const close = braceMatch(src, open);
  if (close === -1) {
    errors.push(`${rel(file)}: could not find the end of ${decl[1]}.`);
    continue;
  }

  let bundle;
  try {
    // Empty context and a timeout: this is repo source, but a gate that can be made to run
    // arbitrary code by editing a page is not a gate.
    bundle = vm.runInNewContext(
      `(${src.slice(open, close + 1)})`,
      Object.create(null),
      { timeout: 2000 },
    );
  } catch (e) {
    errors.push(`${rel(file)}: ${decl[1]} could not be parsed: ${e.message}`);
    continue;
  }

  const locales = Object.keys(bundle || {});
  const en = bundle && bundle.en;

  // Anti-vacuity floor. Two empty sets agree perfectly, and a page that parses to nothing would
  // otherwise report a clean comparison forever.
  if (!en || typeof en !== 'object' || Object.keys(en).length === 0) {
    errors.push(
      `${rel(file)}: ${decl[1]} has no usable \`en\` bundle (locales found: ` +
        `${locales.join(', ') || 'none'}).\n` +
        `  Refusing to report that its locales agree with an English bundle that is not there.`,
    );
    continue;
  }
  if (locales.length < 2) {
    errors.push(
      `${rel(file)}: ${decl[1]} declares only \`en\`, so nothing was compared.\n` +
        `  If the page is genuinely English-only, remove it from SELF_CONTAINED -- an entry\n` +
        `  here that compares nothing reads as a page that has been checked.`,
    );
    continue;
  }

  const enKeys = Object.keys(en);
  selfContainedKeys += enKeys.length;

  for (const locale of locales) {
    if (locale === 'en') continue;
    const loc = bundle[locale] || {};
    const locKeys = Object.keys(loc);
    const absent = enKeys.filter((k) => !(k in loc));
    const extra = locKeys.filter((k) => !(k in en));
    const holes = [];
    for (const k of locKeys) {
      if (!(k in en)) continue;
      const want = placeholders(String(en[k])).join(' ');
      const got = placeholders(String(loc[k])).join(' ');
      if (want !== got)
        holes.push(
          `  ${k}: en has [${want || 'none'}], ${locale} has [${got || 'none'}]`,
        );
    }
    if (absent.length)
      errors.push(
        `${absent.length} key(s) missing from \`${locale}\` in ${rel(file)}:\n` +
          absent
            .slice(0, 20)
            .map((k) => `  ${k}`)
            .join('\n') +
          (absent.length > 20 ? `\n  …and ${absent.length - 20} more` : '') +
          `\n\n  This page carries its own bundle, so a missing key here falls back to English\n` +
          `  silently -- there is no server bundle behind it to catch the difference.`,
      );
    if (extra.length)
      errors.push(
        `${extra.length} key(s) in \`${locale}\` but not \`en\` in ${rel(file)}:\n` +
          extra
            .slice(0, 20)
            .map((k) => `  ${k}`)
            .join('\n'),
      );
    if (holes.length)
      errors.push(
        `${holes.length} placeholder mismatch(es) in ${rel(file)} (${locale}):\n` +
          holes.join('\n'),
      );
  }
}

// A string that never became a key has nothing for the checks above to report missing -- they
// verify that a key which IS used resolves. That is the blind spot #2158 found on the V2 side and
// #2244 on V1's: five table rows wrote their empty and failure states as bare English literals,
// so they stayed English in all nine translated bundles and no gate could see it.
//
// The shape is specific enough to assert directly. Every one of V1's empty/error table rows is a
// full-width muted cell, written as `text-align:center;opacity:0.6`, and every one of them is
// prose a user reads -- so each must resolve through t(). Asserted as the CLASS rather than as the
// five instances, or the sixth gets written next month and nothing notices.
const MUTED_ROW = /text-align:center;opacity:0\.6;?"?>([\s\S]{0,240}?)<\/td>/g;
const v1Rows = [...fs.readFileSync(V1_SCRIPT, 'utf8').matchAll(MUTED_ROW)];

// Anti-vacuity: a regex that stops matching reports a clean pass over nothing, which is the
// failure every gate in this repo keeps re-learning. Five exist as this is written.
if (v1Rows.length < 4) {
  errors.push(
    `only ${v1Rows.length} muted table row(s) were found in ${rel(V1_SCRIPT)}; the derivation is ` +
      `broken, so a green result here would mean nothing was checked`,
  );
}

const hardcodedRows = v1Rows
  .filter((m) => !m[1].includes('t('))
  .map((m) => `  ${m[1].trim().slice(0, 90)}`);

if (hardcodedRows.length) {
  errors.push(
    `${hardcodedRows.length} empty/error table row(s) in ${rel(V1_SCRIPT)} write prose directly ` +
      `instead of resolving it through t():\n${hardcodedRows.join('\n')}\n\n` +
      `Those rows stay English in all nine translated bundles, and no other check here can see ` +
      `them -- a string that never became a key has nothing to report missing (#2244).`,
  );
}

// --- 4. Prose already in the bundle must resolve through its key (#2247) ------------------
//
// Everything above verifies that a key which IS used resolves, and the muted-row assertion
// widens that by one shape. Neither can see the opposite defect: an element that renders prose
// which is ALREADY a value in Language.properties, already translated into all ten locales, and
// simply does not point at the key. 139 of those existed when this was written -- the Reserve a
// Subdomain labels reported from production, the sidebar's Logout button, and every column
// header on four V2 admin screens among them.
//
// The rule carries no translation judgement, which is what makes it gateable: if the exact text
// an element renders is already the value of a key, that element must resolve through that key.
// Text with no matching value is deliberately OUT of scope -- it needs a new key and ten
// translations, which is #2248. This gate must never be widened into asking for a translation.
//
// Scope, stated as data rather than prose (§5b rule 6):
//   * V1_MARKUP only. The SELF_CONTAINED pages carry their own bundles and are compared against
//     their own English above; measuring them against Language.properties is exactly the wrong
//     answer rule 4 documents.
//   * V1 attribute rules apply only where the page's own translator implements them, DERIVED by
//     reading the page and the scripts it loads rather than listed here. So `title="..."` is not
//     checked today because nothing applies `data-i18n-title` -- and the day something does,
//     this starts checking it with no edit here.
//   * V2 covers JSX text, JSX string attributes, and object-literal display labels.

const ENTITIES = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  nbsp: ' ',
  times: '×',
  rarr: '→',
  larr: '←',
  middot: '·',
  mdash: '—',
  ndash: '–',
  hellip: '…',
};

function decodeEntities(str) {
  return str.replace(/&(#[xX]?[0-9a-fA-F]+|[a-zA-Z]+);/g, (m, body) => {
    if (body[0] === '#') {
      const hex = body[1] === 'x' || body[1] === 'X';
      const code = parseInt(hex ? body.slice(2) : body.slice(1), hex ? 16 : 10);
      return Number.isFinite(code) ? String.fromCodePoint(code) : m;
    }
    return Object.prototype.hasOwnProperty.call(ENTITIES, body)
      ? ENTITIES[body]
      : m;
  });
}

function normText(str) {
  return decodeEntities(String(str)).replace(/\s+/g, ' ').trim();
}

// Matching is case-INSENSITIVE, and the exact-case key wins when one exists. Both halves were
// measured against real defects: V1's generator option read "Liferay SE style" against a bundle
// value of "Liferay SE Style", so a case-sensitive rule would have missed it -- while the PAT
// badges need the case to choose, because `active`/`status_active` and `revoked`/`status_revoked`
// differ from each other only by capitalisation and mean different things.
const byValue = new Map();
for (const [k, v] of base.props) {
  const n = normText(v).toLowerCase();
  // Two letters minimum: a bundle value of "—" or "0" would otherwise match every glyph on
  // the page and report nonsense.
  if (!n || !/[A-Za-z]{2}/.test(n)) continue;
  if (!byValue.has(n)) byValue.set(n, []);
  byValue.get(n).push(k);
}

function keysForText(text) {
  const n = normText(text);
  if (!n || !/[A-Za-z]{2}/.test(n)) return null;
  const ks = byValue.get(n.toLowerCase());
  if (!ks) return null;
  const exact = ks.filter((k) => normText(base.props.get(k)) === n);
  return (exact.length ? exact : ks).slice(0, 4);
}

// The offenders found this pass, and the counters that prove the derivation still derives.
const unwired = [];
// The opposite mistake, and the one the fix for the above walks straight into: data-i18n on an
// element that CONTAINS other elements. applyTranslations assigns `el.innerText`
// (static/dashboard.js:19-23), so the first language switch replaces the children with a flat
// string. It looks correct until then, because the initial render happens before any translation
// pass and the English fallback is already in the markup -- which is why
// tests/e2e/ui/tests/portal_heading_anchors.spec.ts:187 exists at all: a copy-link button was
// being wiped out of an <h2> exactly this way. Asserted here as the class rather than left to
// one spec covering one heading.
const containerI18n = [];
const counters = {
  v1Elements: 0,
  v1I18nElements: 0,
  v1Wired: 0,
  v2TextRuns: 0,
  v2Wired: 0,
};

function report(file, line, what, text, keys) {
  unwired.push({ file: rel(file), line, what, text: normText(text), keys });
}

// -- V1 markup --------------------------------------------------------------------------
//
// Parsed with a tag stack rather than a regex over `<tag>text</tag>`, because the question
// "does an ancestor already carry data-i18n" cannot be asked of a flat match. It is not
// academic: setup.html's consent label is one data-i18n element whose value contains two
// <a> children, and applyTranslations rewrites the whole label -- a regex reads those anchors
// as two untranslated links and demands keys they must not have.

const VOID_ELEMENTS = new Set([
  'area',
  'base',
  'br',
  'col',
  'embed',
  'hr',
  'img',
  'input',
  'link',
  'meta',
  'param',
  'source',
  'track',
  'wbr',
]);

function blankOut(src, re) {
  return src.replace(re, (m) => m.replace(/[^\n]/g, ' '));
}

// Scripts, styles and comments are blanked whole -- tags included, so the stack stays balanced.
// A <script> body is JavaScript, and reading it as markup is how setup.html's inline
// applyTranslations (which contains the literal text "Privacy Policy" inside a .replace call)
// turns into two phantom offenders.
function strippedMarkup(src) {
  let out = blankOut(src, /<!--[\s\S]*?-->/g);
  out = blankOut(out, /<script\b[\s\S]*?<\/script\s*>/gi);
  out = blankOut(out, /<style\b[\s\S]*?<\/style\s*>/gi);
  return out;
}

function attrValue(attrs, name) {
  const m = new RegExp(`\\b${name}\\s*=\\s*"([^"]*)"`).exec(attrs);
  return m ? m[1] : null;
}

// Which data-i18n-<attr> mechanisms this page actually has, read out of the page and the local
// scripts it loads. Derived, not listed: a page cannot be asked to point an attribute at a key
// when nothing would ever apply it, and adding the mechanism must switch the check on by itself.
function attrMechanisms(file, src) {
  let text = src;
  for (const m of src.matchAll(
    /<script[^>]*\bsrc\s*=\s*"\/static\/([^"?#]+)"/g,
  )) {
    const js = path.join(ROOT, 'pkg', 'server', 'static', m[1]);
    if (fs.existsSync(js)) text += '\n' + fs.readFileSync(js, 'utf8');
  }
  const attrs = new Set();
  for (const m of text.matchAll(/data-i18n-([a-z][a-z-]*)\s*[\]="']/g)) {
    attrs.add(m[1]);
  }
  return attrs;
}

const V1_TAG = /<(\/?)([a-zA-Z][\w-]*)((?:"[^"]*"|'[^']*'|[^>"'])*)(\/?)>/g;

for (const file of V1_MARKUP) {
  const raw = fs.readFileSync(file, 'utf8');
  const src = strippedMarkup(raw);
  const mechanisms = attrMechanisms(file, raw);
  const lineAt = (off) => src.slice(0, off).split('\n').length;

  const stack = [];
  let last = 0;
  V1_TAG.lastIndex = 0;
  for (const m of src.matchAll(V1_TAG)) {
    const [whole, closing, nameRaw, attrs, selfClose] = m;
    const name = nameRaw.toLowerCase();
    if (stack.length) stack[stack.length - 1].text += src.slice(last, m.index);
    last = m.index + whole.length;

    if (!closing) {
      const i18n = /\bdata-i18n\s*=/.test(attrs);
      if (stack.length) stack[stack.length - 1].children++;
      counters.v1Elements++;

      // Iterate the DERIVED set, not a list. This loop used to read
      // `['placeholder', 'aria-label']` filtered by `mechanisms`, which made the comment above
      // ("adding the mechanism must switch the check on by itself") false: adding
      // data-i18n-title to dashboard.js switched nothing on, because `title` was not in the
      // hardcoded pair. A list intersected with a derivation is a list (#2248).
      for (const attr of mechanisms) {
        const val = attrValue(attrs, attr);
        if (val === null) continue;
        if (new RegExp(`\\bdata-i18n-${attr}\\s*=`).test(attrs)) {
          counters.v1Wired++;
          continue;
        }
        const keys = keysForText(val);
        if (keys) report(file, lineAt(m.index), `${attr}=`, val, keys);
      }

      if (i18n) counters.v1I18nElements++;
      if (VOID_ELEMENTS.has(name) || selfClose) continue;
      stack.push({
        name,
        i18n,
        key: attrValue(attrs, 'data-i18n'),
        ancestorI18n:
          i18n || (stack.length ? stack[stack.length - 1].ancestorI18n : false),
        start: m.index,
        text: '',
        children: 0,
      });
      continue;
    }

    // Closing tag. Unwind tolerantly -- make check-html already asserts these documents are
    // balanced, so an unwind here means that gate is the one to look at, not this one.
    let idx = -1;
    for (let i = stack.length - 1; i >= 0; i--) {
      if (stack[i].name === name) {
        idx = i;
        break;
      }
    }
    if (idx === -1) continue;
    const closed = stack.splice(idx).shift();
    if (closed.i18n && closed.children > 0) {
      containerI18n.push({
        file: rel(file),
        line: lineAt(closed.start),
        name: closed.name,
        key: closed.key || '(unreadable)',
        children: closed.children,
      });
    }
    const text = normText(closed.text);
    if (!text || closed.children > 0) continue;
    if (closed.i18n) {
      const key = closed.key;
      if (
        key &&
        base.props.has(key) &&
        normText(base.props.get(key)) === text
      ) {
        counters.v1Wired++;
      }
      continue;
    }
    if (closed.ancestorI18n) continue;
    const keys = keysForText(text);
    if (keys)
      report(file, lineAt(closed.start), `<${closed.name}>`, text, keys);
  }
}

// -- V2 ---------------------------------------------------------------------------------
//
// String literals and comments are blanked before JSX text is read, so a t() fallback -- which
// is a string literal -- cannot be mistaken for unwired prose, and a commented-out block cannot
// fail the build. Known limit: this lexer does not track regex literals, so a regex containing
// a quote can blank more than it should. That direction is safe (it can only hide an offender,
// never invent one) and the floors below fail loudly if it ever hides most of a file.

function blankLiterals(src) {
  let out = '';
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (c === '/' && src[i + 1] === '/') {
      let j = src.indexOf('\n', i);
      if (j === -1) j = src.length;
      out += ' '.repeat(j - i);
      i = j;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      let j = src.indexOf('*/', i + 2);
      j = j === -1 ? src.length : j + 2;
      out += src.slice(i, j).replace(/[^\n]/g, ' ');
      i = j;
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1;
      while (j < src.length) {
        if (src[j] === '\\') {
          j += 2;
          continue;
        }
        if (src[j] === c) {
          j++;
          break;
        }
        j++;
      }
      out += src.slice(i, j).replace(/[^\n]/g, ' ');
      i = j;
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

function braceSpans(blank) {
  const stack = [];
  const spans = [];
  for (let i = 0; i < blank.length; i++) {
    if (blank[i] === '{') stack.push(i);
    else if (blank[i] === '}' && stack.length) spans.push([stack.pop(), i]);
  }
  return spans;
}

function enclosingBraces(spans, pos) {
  let best = null;
  for (const [a, b] of spans) {
    if (a < pos && pos < b && (!best || a > best[0])) best = [a, b];
  }
  return best;
}

const JSX_TEXT = /[>}]([^<>{}]+)[<{]/g;
const JSX_ATTR = /\b(placeholder|aria-label|title)\s*=\s*(['"])([^'"\n]*)\2/g;
const OBJ_LABEL = /\b(label|title|placeholder)\s*:\s*(['"])([^'"\n]*)\2/g;
// A display literal is accounted for when the same object carries the key that resolves it --
// the shape ShortcutsOverlay needs, because its table is a module-level const where t() cannot
// be called and the key has to travel with the row.
const SIBLING_KEY = /\b[A-Za-z]*[Kk]ey\s*:\s*['"]([A-Za-z0-9_.]+)['"]/g;

for (const file of tsFiles) {
  const raw = fs.readFileSync(file, 'utf8');
  const blank = blankLiterals(raw);
  const spans = braceSpans(blank);
  const lineAt = (off) => raw.slice(0, off).split('\n').length;

  for (const m of blank.matchAll(JSX_TEXT)) {
    counters.v2TextRuns++;
    const keys = keysForText(m[1]);
    if (keys) report(file, lineAt(m.index), 'JSX text', m[1], keys);
  }

  for (const m of raw.matchAll(JSX_ATTR)) {
    const keys = keysForText(m[3]);
    if (keys) report(file, lineAt(m.index), `${m[1]}=`, m[3], keys);
  }

  for (const m of raw.matchAll(OBJ_LABEL)) {
    const keys = keysForText(m[3]);
    if (!keys) continue;
    const span = enclosingBraces(spans, m.index);
    let accounted = false;
    if (span) {
      const obj = raw.slice(span[0], span[1] + 1);
      SIBLING_KEY.lastIndex = 0;
      for (const k of obj.matchAll(SIBLING_KEY)) {
        if (
          base.props.has(k[1]) &&
          normText(base.props.get(k[1])).toLowerCase() ===
            normText(m[3]).toLowerCase()
        ) {
          accounted = true;
          break;
        }
      }
    }
    if (accounted) counters.v2Wired++;
    else report(file, lineAt(m.index), `${m[1]}:`, m[3], keys);
  }

  for (const m of raw.matchAll(T_CALL)) {
    if (
      m[5] &&
      base.props.has(m[2]) &&
      normText(base.props.get(m[2])) === normText(m[5])
    ) {
      counters.v2Wired++;
    }
  }
}

// -- data-i18n on a container ------------------------------------------------------------
//
// Same ratchet shape. An entry that is no longer a container -- or no longer carries the key --
// fails the run, so the list can only shrink.
const KNOWN_CONTAINER_I18N = [
  {
    file: 'pkg/server/static/setup.html',
    key: 'policy_consent_label',
    why:
      "setup.html's own applyTranslations special-cases this one key by name (setup.html:183-190) " +
      'and rebuilds the two policy anchors with innerHTML, so the children survive. It is the ' +
      'only key on the page that does, which is exactly why it is named there rather than ' +
      'inferred.',
  },
];

const containerMatched = new Set();
const containerOffenders = [];
for (const c of containerI18n) {
  const hit = KNOWN_CONTAINER_I18N.findIndex(
    (k) => k.file === c.file && k.key === c.key,
  );
  if (hit === -1) containerOffenders.push(c);
  else containerMatched.add(hit);
}
const staleContainer = KNOWN_CONTAINER_I18N.filter(
  (_, i) => !containerMatched.has(i),
);

if (containerOffenders.length) {
  errors.push(
    `${containerOffenders.length} element(s) carry data-i18n AND contain child elements:\n` +
      containerOffenders
        .map(
          (c) =>
            `  ${c.file}:${c.line}  <${c.name} data-i18n="${c.key}"> has ${c.children} child element(s)`,
        )
        .join('\n') +
      `\n\n  applyTranslations assigns el.innerText, so the first language switch replaces those` +
      `\n  children with a flat string -- an icon, a badge or a copy-link button simply vanishes.` +
      `\n  It renders correctly until then, because the initial paint happens before any` +
      `\n  translation pass, so neither review nor a single-language E2E run can see it.` +
      `\n  Move data-i18n onto an inner text-only element (add a <span> if there is none), the way` +
      `\n  the heading-anchor fix did -- do not reach for innerHTML.`,
  );
}
if (staleContainer.length) {
  errors.push(
    `${staleContainer.length} stale KNOWN_CONTAINER_I18N entr(ies) in ${rel(__filename)}:\n` +
      staleContainer.map((k) => `  ${k.file}: ${k.key}`).join('\n') +
      `\n\n  Remove them -- the element no longer carries that key, or no longer has children.`,
  );
}

// -- The ratchet ------------------------------------------------------------------------
//
// Not an exclusion list. An entry that no longer matches an offender fails the run, so this can
// only shrink -- the V1_KNOWN_INERT shape (§5b rule 5). Each entry says why the obvious mapping
// is WRONG, because a wrong key is worse than no key: it renders confidently in the wrong words
// in nine languages.
const KNOWN_UNWIRED = [
  {
    file: 'pkg/server/dashboard.html',
    text: 'Status: Inactive 🟢',
    why:
      'dashboard.js:5473-5488 writes this span from maint_iron_active or maint_iron_inactive as ' +
      'the Iron Curtain is toggled. data-i18n="maint_iron_inactive" would make the next language ' +
      'switch overwrite a live "Active" with "Inactive" -- the gateway reported as open while it ' +
      'is sealed. It needs the JS to re-apply, not an attribute.',
  },
];

const matched = new Set();
const remaining = [];
for (const u of unwired) {
  const hit = KNOWN_UNWIRED.findIndex(
    (k) => k.file === u.file && normText(k.text) === u.text,
  );
  if (hit === -1) remaining.push(u);
  else matched.add(hit);
}
const staleKnown = KNOWN_UNWIRED.filter((_, i) => !matched.has(i));

// -- Anti-vacuity floors ----------------------------------------------------------------
//
// A derivation that stops matching reports a clean pass over nothing, which is the failure this
// repo keeps re-learning. Every input to the rule above gets a floor, because each of them has
// its own way of silently going to zero: a regex that stops matching, a lexer that blanks a file,
// a properties parser that returns an empty map.
const FLOORS = [
  ['bundle values usable as prose', byValue.size, 500],
  ['V1 markup elements parsed', counters.v1Elements, 800],
  ['V1 elements carrying data-i18n', counters.v1I18nElements, 200],
  ['V2 JSX text runs read', counters.v2TextRuns, 2000],
  [
    'strings already resolving through the key whose value they equal',
    counters.v1Wired + counters.v2Wired,
    400,
  ],
];
for (const [what, got, floor] of FLOORS) {
  if (got < floor) {
    errors.push(
      `check-i18n-keys: only ${got} ${what} (floor ${floor}) -- the #2247 derivation is broken, ` +
        `so a green result here would mean nothing was checked. Fix the derivation rather than ` +
        `lowering the floor; the floors are set well under the real counts on purpose.`,
    );
  }
}

if (staleKnown.length) {
  errors.push(
    `${staleKnown.length} stale KNOWN_UNWIRED entr(ies) in ${rel(__filename)} -- no such ` +
      `unwired prose was found any more:\n` +
      staleKnown.map((k) => `  ${k.file}: ${k.text}`).join('\n') +
      `\n\n  Remove them. The list is a ratchet, not an exclusion: an entry that outlives its ` +
      `\n  subject silently exempts whatever is written at that spot next.`,
  );
}

if (remaining.length) {
  errors.push(
    `${remaining.length} element(s) render prose that is ALREADY a value in ${rel(BASE)} but do ` +
      `not resolve through its key:\n` +
      remaining
        .slice(0, 40)
        .map(
          (u) =>
            `  ${u.file}:${u.line}  ${u.what}  "${u.text.slice(0, 60)}"` +
            `\n      -> ${u.keys.join(' | ')}`,
        )
        .join('\n') +
      (remaining.length > 40 ? `\n  …and ${remaining.length - 40} more` : '') +
      `\n\n  The translation already exists in all ten locales; only the pointer is missing.` +
      `\n  V1: add data-i18n="<key>" (or data-i18n-<attr> for an attribute -- the mechanisms` +
      `\n      applyTranslations implements today are placeholder, aria-label and title).` +
      `\n  V2: wrap it as t('<key>', '<the same English>').` +
      `\n  Check the key means what the element means before pointing at it -- where several are` +
      `\n  listed, they are candidates, not a verdict. If none of them fits, the element needs a` +
      `\n  new key and belongs to #2248: add it to KNOWN_UNWIRED with the reason instead.`,
  );
}

if (errors.length) {
  console.error('check-i18n-keys: FAILED\n');
  console.error(errors.join('\n\n'));
  process.exit(1);
}

console.log(
  `check-i18n-keys: OK -- ${used.size} key(s) used across Portal V1, Portal V2 and the server ` +
    `all resolve, ${LOCALES.length} locale bundle(s) match the ${base.props.size} English keys, ` +
    `and ${SELF_CONTAINED.length} self-contained page(s) agree with their own English ` +
    `(${selfContainedKeys} key(s))`,
);
// Printed rather than kept internal: these are the counts the floors above guard, and a reader
// who cannot see them has no way to tell a real pass from a derivation that quietly stopped
// matching.
console.log(
  `check-i18n-keys: #2247 scan -- ${counters.v1Elements} V1 element(s) and ` +
    `${counters.v2TextRuns} V2 text run(s) read against ${byValue.size} bundle value(s); ` +
    `${counters.v1Wired + counters.v2Wired} string(s) resolve through the key they equal, ` +
    `${counters.v1I18nElements} V1 element(s) carry data-i18n and only ` +
    `${containerI18n.length} of them wrap markup, ` +
    `${KNOWN_UNWIRED.length + KNOWN_CONTAINER_I18N.length} ratcheted`,
);
