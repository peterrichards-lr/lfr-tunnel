#!/usr/bin/env node
/*
 * check-html-balance.mjs -- every HTML document in the repo must have balanced tags (#1791).
 *
 * The defect this exists for is not a parse error. A dropped or duplicated closing tag makes
 * no noise anywhere: the file still opens in a browser, prettier does not read it (*.html is
 * in .prettierignore), no linter looks at it, and the recovery rules in the HTML parser give
 * you a *different DOM tree* rather than an error. Every subsequent edit then inherits the
 * wrong tree, and the next person reads two files that are each individually correct.
 *
 * Three of these have been found by hand:
 *
 *   #1785  #card-server-config lost its closing tag in #606, which reparented the Save
 *          Settings row into a card that ships display:none -- System Settings had no
 *          reachable save control at all.
 *   #1791  #dashboard-shell was never closed. #689 removed a closing tag it read as "extra",
 *          orphaning #dashboard-screen; #1292 then added the shell and its own closing tag,
 *          which got consumed one level early. Every modal, #toast-container and both toast
 *          live regions ended up inside a container that is display:none until sign-in.
 *   #1791  pkg/client/dashboard.html carried TWO stray </div>, from #796 and its predecessor,
 *          which closed .header-panel early and pushed the Inspector's status row outside the
 *          panel's padding and below its border.
 *
 * All three are the same class, and none of the existing gates (check-css, check-contrast,
 * check-i18n, check-theme-tokens) can see it, because none of them models nesting.
 *
 * WHAT THIS DOES NOT LOOK AT -- asserted in tests/hooks/test-html-balance.sh rather than only
 * stated here, because a blind spot written in a comment is a blind spot nobody can notice
 * widening (github-workflow SKILL 5b rule 6):
 *
 *   - JSX/TSX under ui/. Unbalanced JSX is a compile error; `pnpm run build` in CI already
 *     fails on it, so a second checker would be redundant rather than additive.
 *   - Markup assembled at runtime from string templates (innerHTML in dashboard.js, Go
 *     fmt.Sprintf). Balance there is a property of the code path, not of any file on disk.
 *   - Attribute validity, unknown elements, duplicate ids. Different problems, different gate.
 */

import fs from 'node:fs';
import path from 'node:path';

/*
 * Elements with no end tag at all. A `</br>` is not a thing and `<img>` never opens a scope,
 * so these must never reach the stack.
 */
const VOID = new Set([
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
  'source',
  'track',
  'wbr',
  'param',
  'keygen',
]);

/*
 * Elements whose end tag HTML makes optional. `<li>foo<li>bar` and a `<p>` with no `</p>` are
 * both legal, so an unclosed one of these is not evidence of anything. They are still tracked
 * -- they have to be, or a `</div>` inside a `<td>` would match the wrong opener -- but they
 * are popped implicitly and never reported.
 */
const OPTIONAL_END = new Set([
  'li',
  'dt',
  'dd',
  'p',
  'rt',
  'rp',
  'optgroup',
  'option',
  'thead',
  'tbody',
  'tfoot',
  'tr',
  'td',
  'th',
  'caption',
  'colgroup',
  'head',
  'body',
  'html',
]);

/*
 * Raw-text elements. Their content is not markup: dashboard.html's inline <script> contains
 * `'</div>'` inside string literals, and a naive scan counts those as real closing tags. Per
 * the spec the content ends at the first `</name`, so that is what is skipped to.
 */
const RAW_TEXT = new Set(['script', 'style', 'textarea', 'title']);

const SKIP_DIRS = new Set([
  '.git',
  'node_modules',
  'dist',
  'ui-dist',
  '_site',
  'site',
  'bin',
  'playwright-report',
  'test-results',
  'coverage',
  '.pnpm-store',
]);

/*
 * The ratchet, NOT an exclusion list (github-workflow SKILL 5b rule 5).
 *
 * An entry is `'<path>': ['<kind>:<element>' , ...]`. A problem listed here is tolerated; a
 * problem NOT listed fails; and an entry that no longer matches anything ALSO fails, naming
 * itself. That last half is the whole point -- a list that tolerates its own staleness is a
 * suppression wearing a ratchet's clothes, and it can only ever grow.
 *
 * It is empty because the enumeration that came with #1791 found exactly two unbalanced
 * documents and both were fixed. Keep it that way if you can.
 */
const KNOWN_UNBALANCED = Object.freeze({});

/*
 * Anti-vacuity floor (#1779). A checker that examined nothing exits 0 and is indistinguishable
 * from one that found nothing wrong -- which is how three gates in this repo were narrower
 * than anyone believed. Overridable so the floor itself can be exercised by a test rather than
 * only asserted to exist.
 */
const MIN_FILES = Number(process.env.LFT_HTML_MIN_FILES ?? 20);

function walk(dir, out) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const e of entries) {
    if (e.name.startsWith('.') && e.name !== '.github') continue;
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (SKIP_DIRS.has(e.name)) continue;
      walk(full, out);
    } else if (/\.html?$/i.test(e.name)) {
      out.push(full);
    }
  }
  return out;
}

/**
 * Returns { problems, elements } for one document.
 *
 * `elements` is reported so a green run can be told apart from a run that parsed nothing --
 * a file whose content failed to tokenise would otherwise look perfectly balanced.
 */
function scanDocument(src) {
  const lower = src.toLowerCase();
  const problems = [];
  const stack = [];
  let elements = 0;
  let i = 0;

  // Precompute line starts so a position -> line lookup is not a slice+split per tag.
  const lineStarts = [0];
  for (let k = 0; k < src.length; k++)
    if (src[k] === '\n') lineStarts.push(k + 1);
  const lineAt = (idx) => {
    let lo = 0;
    let hi = lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (lineStarts[mid] <= idx) lo = mid;
      else hi = mid - 1;
    }
    return lo + 1;
  };

  const TAG =
    /^<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>"'])*?)(\/?)>/;

  while (i < src.length) {
    const lt = src.indexOf('<', i);
    if (lt < 0) break;
    if (src.startsWith('<!--', lt)) {
      const end = src.indexOf('-->', lt + 4);
      i = end < 0 ? src.length : end + 3;
      continue;
    }
    if (src.startsWith('<!', lt) || src.startsWith('<?', lt)) {
      const end = src.indexOf('>', lt);
      i = end < 0 ? src.length : end + 1;
      continue;
    }
    const m = TAG.exec(src.slice(lt));
    if (!m) {
      // A bare `<` in text. Not a tag, not an error.
      i = lt + 1;
      continue;
    }
    const [full, slash, rawName, attrs, selfClose] = m;
    const name = rawName.toLowerCase();
    const end = lt + full.length;

    if (!slash) {
      elements++;
      if (VOID.has(name) || selfClose) {
        i = end;
        continue;
      }
      if (RAW_TEXT.has(name)) {
        const close = lower.indexOf(`</${name}`, end);
        if (close < 0) {
          problems.push({
            kind: 'unclosed',
            name,
            line: lineAt(lt),
            id: null,
            cls: null,
          });
          break;
        }
        const gt = src.indexOf('>', close);
        i = gt < 0 ? src.length : gt + 1;
        continue;
      }
      const idMatch = /\bid\s*=\s*"([^"]*)"/.exec(attrs);
      const classMatch = /\bclass\s*=\s*"([^"]*)"/.exec(attrs);
      stack.push({
        name,
        line: lineAt(lt),
        id: idMatch ? idMatch[1] : null,
        cls: classMatch ? classMatch[1] : null,
      });
      i = end;
      continue;
    }

    // Closing tag. Pop through anything whose end tag was optional, then match.
    while (
      stack.length &&
      stack[stack.length - 1].name !== name &&
      OPTIONAL_END.has(stack[stack.length - 1].name)
    ) {
      stack.pop();
    }
    if (stack.length && stack[stack.length - 1].name === name) {
      stack.pop();
    } else if (!OPTIONAL_END.has(name)) {
      problems.push({
        kind: 'stray-close',
        name,
        line: lineAt(lt),
        id: null,
        cls: null,
      });
    }
    i = end;
  }

  for (const el of stack) {
    if (OPTIONAL_END.has(el.name)) continue;
    problems.push({ ...el, kind: 'unclosed' });
  }
  return { problems, elements };
}

function describe(p) {
  const label = p.id ? `#${p.id}` : p.cls ? `.${p.cls.split(/\s+/)[0]}` : '';
  return `${p.kind}:${p.name}${label ? `(${label})` : ''}`;
}

function main() {
  const root = process.cwd();
  const files = walk(root, []).sort();

  const found = new Map(); // relative path -> signature[]
  let elements = 0;
  const lines = [];

  for (const file of files) {
    const rel = path.relative(root, file);
    const { problems, elements: n } = scanDocument(
      fs.readFileSync(file, 'utf8'),
    );
    elements += n;
    if (!problems.length) continue;
    found.set(
      rel,
      problems.map((p) => describe(p)),
    );
    for (const p of problems) {
      lines.push(`  ${rel}:${p.line}  ${describe(p)}`);
    }
  }

  let failed = false;

  // 1. Vacuity. Reported first: every verdict below is meaningless if nothing was read.
  if (files.length < MIN_FILES || elements === 0) {
    console.error(
      `check-html-balance: FAIL -- scanned ${files.length} document(s) / ${elements} element(s), ` +
        `below the floor of ${MIN_FILES}. This is a pass over nothing, not a clean tree.`,
    );
    failed = true;
  }

  // 2. Unratcheted problems.
  const unexpected = [];
  for (const [rel, sigs] of found) {
    const allowed = KNOWN_UNBALANCED[rel] ?? [];
    for (const sig of sigs) {
      if (!allowed.includes(sig)) unexpected.push(`${rel}  ${sig}`);
    }
  }
  if (unexpected.length) {
    console.error(
      `check-html-balance: FAIL -- unbalanced HTML in ${found.size} document(s):`,
    );
    for (const l of lines) console.error(l);
    console.error(
      '\n  A dropped or duplicated closing tag does not error -- the browser recovers into a\n' +
        '  DIFFERENT tree, which is how #1785 and #1791 survived. Fix the nesting; do not add\n' +
        '  the file to KNOWN_UNBALANCED unless the fix is genuinely a separate change.',
    );
    failed = true;
  }

  // 3. Stale ratchet entries. This is what makes KNOWN_UNBALANCED a ratchet: fixing a file
  //    without removing its entry turns the build red, so the list can only shrink.
  for (const [rel, sigs] of Object.entries(KNOWN_UNBALANCED)) {
    const actual = found.get(rel);
    if (!actual) {
      console.error(
        `check-html-balance: FAIL -- stale ratchet entry '${rel}': the document is balanced ` +
          '(or gone). Remove the entry.',
      );
      failed = true;
      continue;
    }
    for (const sig of sigs) {
      if (!actual.includes(sig)) {
        console.error(
          `check-html-balance: FAIL -- stale ratchet entry '${rel}' -> '${sig}': no longer ` +
            'occurs. Remove it.',
        );
        failed = true;
      }
    }
  }

  if (failed) process.exit(1);

  console.log(
    `check-html-balance: OK (${files.length} documents, ${elements} elements examined, ` +
      `${Object.keys(KNOWN_UNBALANCED).length} ratcheted)`,
  );
}

main();
