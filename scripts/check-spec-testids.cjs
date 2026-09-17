#!/usr/bin/env node
/**
 * check-spec-testids.cjs -- a spec may not key an assertion on a data-testid nothing renders
 * (#1974).
 *
 * The defect this guards: tests/e2e/ui/tests/geo_distribution_parity.spec.ts asserts the geo
 * credit line is ABSENT while the feature is off --
 *
 *     await expect(panel.locator('[data-testid="geo-attribution"]')).toHaveCount(0);
 *
 * -- and an absence assertion keyed on a testid passes just as well when the testid exists
 * nowhere at all. Rename or drop `data-testid="geo-attribution"` in ui/src and the assertion
 * goes permanently, silently green, including on the day the credit renders when it should not.
 *
 * The behavioural control cannot be run from inside the suite: rendering the credit needs a
 * geo-IP database, and the E2E stack deliberately ships none (no vendor licence permits baking
 * their file into the image). #1779's rule is that this is exactly the condition that makes a
 * gate worth having -- an assertion nobody can show going red needs its premise checked
 * somewhere else.
 *
 * So this is the sibling of scripts/check-print-selectors.cjs, one direction over: that one
 * asks "does every print selector match real markup"; this one asks "does every testid a spec
 * names exist in real markup". Both compare rather than document, and both fail closed -- if
 * either side parses to nothing the run exits 1 rather than reporting that two empty sets
 * agree (#1779).
 *
 * SCOPE, deliberately narrow, asserted in tests/hooks/test-spec-testids.sh rather than only
 * stated here (github-workflow SKILL 5b rule 6):
 *
 *   - It checks EXISTENCE, not arm-correctness. The two portals' markup is one pool, so a
 *     testid rendered only by V1 satisfies a V2 spec's reference. Deciding which arm a spec
 *     runs against needs the spec's own routing, which this gate does not model -- and the
 *     defect in #1974 is a testid that exists NOWHERE.
 *   - Dynamic references (`getByTestId(`row-${id}`)`) are reported and skipped. There is no
 *     literal to compare.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const REPO = path.join(__dirname, '..');
const rel = (p) => path.relative(REPO, p);

const SPEC_DIR = path.join(REPO, 'tests/e2e/ui/tests');
const SPEC_EXTS = ['.ts', '.tsx'];

// Where a testid may legitimately be defined. One pool on purpose -- see SCOPE above.
const MARKUP = [
  {
    name: 'V2',
    root: path.join(REPO, 'ui/src'),
    exts: ['.tsx', '.ts', '.html'],
  },
  { name: 'V1', root: path.join(REPO, 'pkg/server'), exts: ['.html', '.js'] },
];

// Testids a spec may name that no source file can contain -- injected by a library, or built at
// runtime from data. Each needs a reason: an unexplained entry here is how a real dead testid
// gets waved through, which is the defect this gate exists for.
const EXEMPT = {};

// Anti-vacuity floor. Overridable so tests/hooks/test-spec-testids.sh can exercise it in place
// rather than having to contrive an empty tree (#1779).
const MIN_REFS = Number(process.env.LFT_TESTID_MIN_REFS || 1);

let failed = false;
const fail = (m) => {
  console.error(`  ${m}`);
  failed = true;
};

/**
 * Strip comments before matching either side.
 *
 * On the MARKUP side this is the load-bearing half, and it is the lesson
 * check-print-selectors.cjs learned: a comment explaining that a testid was removed NAMES it,
 * so an unstripped scan would pass precisely because someone documented the bug.
 *
 * On the SPEC side it prevents the opposite error -- a commented-out assertion is not a live
 * reference, and failing the build over one would be a false red.
 *
 * Line comments are only stripped where `//` is not preceded by `:`, `/` or a quote, so that a
 * URL in an attribute (`href="https://..."`) does not swallow the rest of its line -- which
 * would delete a real `data-testid` sitting after it and invent a failure.
 *
 * Block comments are BLANKED rather than deleted, newlines kept. Deleting them renumbers every
 * line after the first multi-line comment, and the first run of this gate duly reported the
 * geo-attribution reference at line 145 of a spec that carries it at 175 -- a failure message
 * pointing at the wrong line is a failure message someone will disbelieve.
 */
function stripComments(src) {
  const blank = (m) => m.replace(/[^\n]/g, ' ');
  return src
    .replace(/<!--[\s\S]*?-->/g, blank)
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .replace(/(^|[^:\w"'`\\/])\/\/[^\n]*/g, '$1');
}

function walk(root, exts, out) {
  if (!fs.existsSync(root)) return out;
  const st = fs.statSync(root);
  if (st.isDirectory()) {
    for (const e of fs.readdirSync(root).sort()) {
      if (e === 'node_modules' || e === 'ui-dist') continue;
      walk(path.join(root, e), exts, out);
    }
  } else if (exts.includes(path.extname(root))) {
    out.push(root);
  }
  return out;
}

/** Every testid the markup actually renders. */
function definedTestids() {
  const defined = new Map(); // testid -> first file that defines it
  let filesRead = 0;
  for (const arm of MARKUP) {
    for (const file of walk(arm.root, arm.exts, [])) {
      filesRead++;
      const src = stripComments(fs.readFileSync(file, 'utf8'));
      // data-testid="x", data-testid='x', and the JSX literal form data-testid={"x"}.
      for (const m of src.matchAll(
        /data-testid\s*=\s*\{?\s*["']([^"']+)["']/g,
      )) {
        if (!defined.has(m[1])) defined.set(m[1], `${arm.name}: ${rel(file)}`);
      }
    }
  }
  return { defined, filesRead };
}

/** Every testid a spec names, with the file and line that names it. */
function referencedTestids() {
  const refs = [];
  const dynamic = [];
  const files = walk(SPEC_DIR, SPEC_EXTS, []);
  for (const file of files) {
    const src = stripComments(fs.readFileSync(file, 'utf8'));
    const lines = src.split('\n');
    lines.forEach((line, i) => {
      const at = { file: rel(file), line: i + 1 };
      // CSS-attribute form: [data-testid="x"], including the ^= *= $= ~= |= operators.
      for (const m of line.matchAll(
        /\[\s*data-testid\s*([~^|*$]?)=\s*["']([^"']+)["']/g,
      )) {
        refs.push({ ...at, op: m[1] || '=', id: m[2] });
      }
      // Playwright form: getByTestId('x'). A template literal carrying ${} has no literal to
      // compare, so it is recorded as dynamic rather than silently dropped.
      for (const m of line.matchAll(/getByTestId\(\s*([`'"])([^`'"]*)\1/g)) {
        if (m[2].includes('${')) dynamic.push({ ...at, id: m[2] });
        else refs.push({ ...at, op: '=', id: m[2] });
      }
      for (const m of line.matchAll(/getByTestId\(\s*`([^`]*\$\{[^`]*)`/g)) {
        dynamic.push({ ...at, id: m[1] });
      }
    });
  }
  return { refs, dynamic, fileCount: files.length };
}

function satisfied(ref, defined) {
  const ids = [...defined.keys()];
  switch (ref.op) {
    case '=':
      return defined.has(ref.id);
    case '^':
      return ids.some((id) => id.startsWith(ref.id));
    case '$':
      return ids.some((id) => id.endsWith(ref.id));
    case '*':
      return ids.some((id) => id.includes(ref.id));
    case '~':
      return ids.some((id) => id.split(/\s+/).includes(ref.id));
    case '|':
      return ids.some((id) => id === ref.id || id.startsWith(`${ref.id}-`));
    default:
      return false;
  }
}

const { defined, filesRead } = definedTestids();
const { refs, dynamic, fileCount } = referencedTestids();

// Fail closed, three ways. Each of these has a real shape behind it: a moved spec directory, a
// renamed markup tree, and a reference regex that stopped matching. All three produce "two empty
// sets agree" in a naive implementation, which is a pass.
if (fileCount === 0) {
  fail(
    `no spec files under ${rel(SPEC_DIR)} -- refusing to verify an empty suite.`,
  );
}
if (filesRead === 0) {
  fail(
    'read no markup from ui/src or pkg/server, so nothing could be verified.',
  );
}
if (defined.size === 0) {
  fail(
    'parsed no data-testid out of any markup -- refusing to report that a suite of ' +
      'references agrees with an empty pool.',
  );
}
if (refs.length < MIN_REFS) {
  fail(
    `parsed ${refs.length} testid reference(s) from the specs, below the floor of ${MIN_REFS} ` +
      '-- either the specs stopped using testids or the reference matcher is broken.',
  );
}

if (!failed) {
  for (const ref of refs) {
    if (EXEMPT[ref.id] || satisfied(ref, defined)) continue;
    fail(
      `${ref.file}:${ref.line} references data-testid "${ref.id}", which no markup defines.`,
    );
    fail(
      '    Looked in ui/src (V2) and pkg/server (V1). An assertion keyed on a testid that ' +
        'exists nowhere',
    );
    fail(
      '    passes forever -- toHaveCount(0) is satisfied by the testid being gone, not by the ' +
        'element being absent.',
    );
  }
}

if (failed) {
  console.error('');
  console.error('SPEC TESTID CHECK FAILED');
  console.error(
    '  Either the markup lost the attribute, or the spec names one that never existed.',
  );
  console.error(
    '  Fix whichever is wrong -- or add it to EXEMPT in this script with a reason. This is #1974.',
  );
  process.exit(1);
}

for (const d of dynamic) {
  console.log(
    `  skipped (dynamic): ${d.file}:${d.line} getByTestId(\`${d.id}\`)`,
  );
}
console.log(
  `OK -- ${refs.length} testid reference(s) across ${fileCount} spec file(s) all exist in markup ` +
    `(${defined.size} defined across ${filesRead} source file(s))`,
);
