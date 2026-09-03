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

const ROOT = path.join(__dirname, '..');
const I18N_DIR = path.join(ROOT, 'pkg', 'server', 'i18n');
const BASE = path.join(I18N_DIR, 'Language.properties');

// The locale set the server actually loads (pkg/server/i18n.go initI18n). Kept in step with
// that list deliberately: a bundle nobody loads is not worth failing a build over, and a
// locale the server loads but nobody translated is exactly what this check is for.
const LOCALES = ['es', 'fr', 'de', 'pt', 'ko', 'ja', 'zh', 'ro', 'ar'];

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
const ATTR = /data-i18n(?:-placeholder|-aria-label)?\s*=\s*"([^"]+)"/g;
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

if (errors.length) {
  console.error('check-i18n-keys: FAILED\n');
  console.error(errors.join('\n\n'));
  process.exit(1);
}

console.log(
  `check-i18n-keys: OK -- ${used.size} key(s) used across Portal V1, Portal V2 and the server ` +
    `all resolve, and ${LOCALES.length} locale bundle(s) match the ${base.props.size} English keys`,
);
