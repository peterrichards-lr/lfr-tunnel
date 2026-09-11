#!/usr/bin/env node
/**
 * check-load-failure-surfaced.cjs -- a portal page must not swallow a failed data load (#1868).
 *
 * Six admin pages ended their fetch in `catch (e) { console.error(e); }` and then let
 * `setLoading(false)` run, so the page rendered as if the load had succeeded. For read-only pages
 * that is an empty table indistinguishable from "no results" -- the same shape as the
 * absence-only assertions in the e2e skill §3, where an empty page satisfies a check for absence
 * perfectly. For System Settings it was worse: the form showed its React initial state
 * ('round_robin', empty, false), the Save button was gated only on role, and saving wrote those
 * defaults over the real configuration.
 *
 * The rule enforced here is narrow on purpose: a catch block whose entire body is console.error
 * (optionally with a bare return) is a swallow. Anything that also sets state, shows a toast or
 * rethrows is surfacing the failure somehow, and this gate does not judge how.
 *
 * Fails closed: if no pages or no catch blocks are found at all, it exits 1 rather than reporting
 * that nothing was wrong. This repo has found five gates that passed on a scan of nothing (#1779).
 */

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');
const PAGES_DIR = path.join(REPO, 'ui/src/pages');
const rel = (p) => path.relative(REPO, p);

if (!fs.existsSync(PAGES_DIR)) {
  console.error(`check-load-failure-surfaced: ${rel(PAGES_DIR)} not found.`);
  console.error(
    '  If the portal moved, move this check with it rather than deleting it.',
  );
  process.exit(1);
}

const files = fs
  .readdirSync(PAGES_DIR)
  .filter((f) => f.endsWith('.tsx'))
  .map((f) => path.join(PAGES_DIR, f));

if (files.length === 0) {
  console.error(
    `check-load-failure-surfaced: no .tsx pages found in ${rel(PAGES_DIR)}.`,
  );
  console.error('  Refusing to report a clean scan of nothing.');
  process.exit(1);
}

// `catch (e) { ... }` with a body containing no braces of its own. Nested blocks are rare in these
// handlers and a body with one is doing more than swallowing by definition.
const CATCH = /catch\s*\(([^)]*)\)\s*\{([^{}]*)\}/g;

const findings = [];
let catchBlocks = 0;

for (const file of files) {
  const src = fs.readFileSync(file, 'utf8');
  const lineOf = (idx) => src.slice(0, idx).split('\n').length;

  for (const m of src.matchAll(CATCH)) {
    catchBlocks += 1;
    const body = m[2];

    // Strip comments before judging: a comment explaining the handler is not handling.
    const code = body
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/\/\/[^\n]*/g, '')
      .trim();

    // An explicit opt-out, with the reason next to the code rather than in a list in here.
    // Not every catch is a page load: a malformed WebSocket frame should be logged and ignored,
    // not turned into a banner that blanks the page.
    // [ \t] not \s: \s matches newlines, so a bare marker matched the first character of the
    // NEXT line and an unreasoned opt-out passed. Found by this gate's own control case.
    if (/load-failure-gate:[ \t]*\S/.test(body)) continue;

    if (code === '') continue; // an empty catch is a different (and rarer) sin; not this gate's.

    const onlyConsole = code
      .split(';')
      .map((s) => s.trim())
      .filter(Boolean)
      .every(
        (stmt) =>
          /^console\.(error|warn|log)\s*\(/.test(stmt) || stmt === 'return',
      );

    if (onlyConsole) {
      findings.push({
        file,
        line: lineOf(m.index),
        code: code.replace(/\s+/g, ' ').slice(0, 80),
      });
    }
  }
}

if (catchBlocks === 0) {
  console.error(
    'check-load-failure-surfaced: no catch blocks matched in any page.',
  );
  console.error(
    '  The pattern may have changed. Refusing to report a clean scan of nothing.',
  );
  process.exit(1);
}

console.log(
  `Checking that portal pages surface load failures (${catchBlocks} catch block(s) in ${files.length} page(s))...`,
);

if (findings.length > 0) {
  console.error('');
  console.error('LOAD FAILURE CHECK FAILED -- these catch blocks only log:');
  console.error('');
  for (const f of findings) {
    console.error(`  ${rel(f.file)}:${f.line}`);
    console.error(`      ${f.code}`);
  }
  console.error('');
  console.error(
    '  A page that logs and carries on renders as if the load succeeded. Set an error',
  );
  console.error(
    "  state and show it -- t('admin_load_failed', ...) is the shared string.",
  );
  console.error(
    '  For a page that WRITES, also gate the save: a banner does not stop a PUT.',
  );
  process.exit(1);
}

console.log(`OK -- no page swallows a load failure.`);
