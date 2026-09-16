#!/usr/bin/env node
/**
 * check-mkdocs-nav.cjs -- every page under docs/ is reachable from the site's navigation (#1918).
 *
 * Publishing is automatic and navigation is not. `.github/workflows/docs.yml` runs
 * `mkdocs build --strict` and deploys to Pages on every push to master, but `mkdocs.yml` carries a
 * hand-written `nav:` block and its `plugins:` list is only `search` -- no awesome-pages, no
 * literate-nav, no gen-files. So a new page anywhere under docs/ is built, deployed, and reachable
 * by direct URL or site search alone. Nothing fails, which is what makes it easy to miss -- and it
 * had already happened: `docker_hub_readme.md` has been on disk and absent from `nav:` since #94.
 *
 * `mkdocs --strict` does not cover this. It promotes WARNINGs -- an unresolvable link among them,
 * which is the case ci.yml's Documentation Build already guards (#1725) -- but a page outside the
 * nav is an INFO, so the build is green either way.
 *
 * So this compares rather than documents, the same reasoning as check-status-vocabulary.cjs and
 * check-i18n-keys.cjs: the two sides are maintained by hand in different files, and a miss is
 * silent in both directions.
 *
 * The exclusion list lives in mkdocs.yml, as `# nav-exclude: <path> -- <reason>` comment lines
 * next to `nav:` itself, rather than as an array in this file. Two reasons: it is the file
 * someone edits when adding a page, so the exclusion is in front of whoever needs it; and a
 * reason is required by the parser, so "deliberately out" can be told apart from "forgotten",
 * which is the whole point of the issue. A comment cannot affect the mkdocs build.
 *
 * Fails closed. If the nav parses to no local pages, or the docs tree walks to no files, this
 * exits 1 rather than reporting that two empty sets agree -- a gate that cannot fail is worse
 * than none, and this repo has found five of those (#1779).
 */

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');
const rel = (p) => path.relative(REPO, p).split(path.sep).join('/');
const CONFIG = path.join(REPO, 'mkdocs.yml');

let failed = false;
const fail = (msg) => {
  console.error(`  ${msg}`);
  failed = true;
};

if (!fs.existsSync(CONFIG)) {
  console.error(`check-mkdocs-nav: ${rel(CONFIG)} not found.`);
  console.error(
    '  If the site config moved, move this check with it rather than deleting the check.',
  );
  process.exit(1);
}

const src = fs.readFileSync(CONFIG, 'utf8');
const lines = src.split('\n');

const unquote = (s) => s.trim().replace(/^(['"])(.*)\1$/, '$2');

// -- docs_dir, defaulted the way mkdocs defaults it. Read rather than assumed: if someone moves
//    the tree, this check must move with it instead of walking a directory that no longer exists.
const docsDirMatch = src.match(/^docs_dir:\s*(.+?)\s*$/m);
const DOCS_DIR = path.resolve(
  REPO,
  docsDirMatch ? unquote(docsDirMatch[1]) : 'docs',
);

// ---------------------------------------------------------------------------
// The nav block.
//
// Hand-parsed rather than pulled through a YAML library: this repo has no root package.json and
// none of its sibling gates take a dependency, so a parser here would be the only one. The shape
// being read is small and fixed -- a list of `- Title: target` entries, nested one level under
// section headings -- and the anti-vacuity floor below is what catches a parse that has gone
// wrong, rather than the parse being trusted.
// ---------------------------------------------------------------------------
const navStart = lines.findIndex((l) => /^nav:\s*(#.*)?$/.test(l));
if (navStart === -1) {
  console.error(`check-mkdocs-nav: no 'nav:' block in ${rel(CONFIG)}.`);
  console.error(
    '  If the site moved to awesome-pages/literate-nav, this check is obsolete -- delete it',
  );
  console.error('  deliberately rather than leaving it parsing nothing.');
  process.exit(1);
}

const navBody = [];
for (let i = navStart + 1; i < lines.length; i++) {
  const line = lines[i];
  if (/^\s*$/.test(line)) continue;
  if (!/^\s/.test(line)) break; // dedented back to a top-level key: the block has ended.
  navBody.push(line);
}

const navTargets = [];
for (const line of navBody) {
  const item = line.match(/^\s*-\s*(.*?)\s*$/);
  if (!item || !item[1]) continue;
  const entry = item[1];

  // Greedy up to the LAST ': ', so a title containing a colon resolves to the right half and a
  // `https://` target -- whose colon is followed by '/', not by whitespace -- is left intact.
  const kv = entry.match(/^(.*):\s+(\S.*)$/);
  let target;
  if (kv) {
    target = kv[2];
  } else if (/:$/.test(entry)) {
    continue; // A section heading, e.g. `- Client Guides:`. It has no target of its own.
  } else {
    target = entry; // mkdocs allows a bare path with no title.
  }

  target = unquote(target);
  if (target) navTargets.push(target);
}

// External links are nav entries too. CODE_OF_CONDUCT.md, CONTRIBUTING.md and SECURITY.md are
// linked on github.com rather than copied into docs/, so treating them as local paths would
// report three missing files on a correct tree.
const isExternal = (t) =>
  /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(t) || t.startsWith('//');
const navLocal = navTargets
  .filter((t) => !isExternal(t))
  .map((t) => t.replace(/^\.\//, ''));
const navExternal = navTargets.filter(isExternal);

// ---------------------------------------------------------------------------
// The exclusion list: `# nav-exclude: <path> -- <reason>` in mkdocs.yml.
// ---------------------------------------------------------------------------
const EXCLUDE_LINE = /^\s*#\s*nav-exclude:\s*(.*)$/;
const excluded = [];
for (const line of lines) {
  const m = line.match(EXCLUDE_LINE);
  if (!m) continue;
  const payload = m[1].trim();
  const parts = payload.match(/^(\S+)\s+--\s+(\S.*)$/);
  if (!parts) {
    fail(`nav-exclude line is missing its reason: "${payload}"`);
    fail(
      '  write it as `# nav-exclude: <path> -- <why this page is not on the site>`; the reason is',
    );
    fail(
      '  the entire point -- without one, an exclusion is indistinguishable from an oversight.',
    );
    continue;
  }
  excluded.push({ page: parts[1].replace(/^\.\//, ''), reason: parts[2] });
}

// ---------------------------------------------------------------------------
// The docs tree.
// ---------------------------------------------------------------------------
const walk = (dir, prefix) => {
  const found = [];
  for (const dirent of fs.readdirSync(dir, { withFileTypes: true })) {
    // mkdocs skips dot-prefixed files and directories itself, so they are never built and
    // cannot be orphaned pages. Flagging one would be a finding nobody can act on.
    if (dirent.name.startsWith('.')) continue;
    const name = prefix ? `${prefix}/${dirent.name}` : dirent.name;
    if (dirent.isDirectory()) {
      found.push(...walk(path.join(dir, dirent.name), name));
    } else if (dirent.isFile() && dirent.name.endsWith('.md')) {
      found.push(name);
    }
  }
  return found;
};

if (!fs.existsSync(DOCS_DIR) || !fs.statSync(DOCS_DIR).isDirectory()) {
  console.error(
    `check-mkdocs-nav: docs directory ${rel(DOCS_DIR)} does not exist.`,
  );
  process.exit(1);
}
const onDisk = walk(DOCS_DIR, '').sort();

// -- Anti-vacuity. Two empty sets agree perfectly, and this gate's whole job is a comparison.
if (navLocal.length === 0 || onDisk.length === 0) {
  console.error('check-mkdocs-nav: one of the two sides parsed to nothing.');
  console.error(
    `  nav local pages=${navLocal.length} (external links=${navExternal.length})  ${rel(DOCS_DIR)}/**/*.md=${onDisk.length}`,
  );
  console.error('  Refusing to report that empty sets agree.');
  process.exit(1);
}

console.log(
  `Checking mkdocs navigation (${onDisk.length} pages under ${rel(DOCS_DIR)}, ${navLocal.length} in nav, ${excluded.length} excluded)...`,
);

const excludedPages = excluded.map((e) => e.page);

// 1. On disk, not in nav, not excluded -- the #1918 defect: built, deployed, unreachable.
const orphans = onDisk.filter(
  (p) => !navLocal.includes(p) && !excludedPages.includes(p),
);
for (const p of orphans) {
  fail(`${rel(DOCS_DIR)}/${p} is in neither nav: nor the nav-exclude list`);
}
if (orphans.length) {
  fail(
    '  it will build and deploy, and be reachable only by direct URL or site search. Add it to',
  );
  fail(
    `  nav: in ${rel(CONFIG)}, or exclude it there with \`# nav-exclude: <path> -- <reason>\`.`,
  );
}

// 2. In nav, not on disk. mkdocs --strict catches this too, but only in CI and only once the
//    whole site has been built; catching it here makes a rename cheap to check before pushing.
for (const p of navLocal) {
  if (!onDisk.includes(p)) {
    fail(`nav: points at ${rel(DOCS_DIR)}/${p}, which does not exist`);
  }
}

// 3. Excluded but gone. A stale exclusion is a reason nobody can act on, and it silently covers
//    the next page that happens to be given the same name.
for (const e of excluded) {
  if (!onDisk.includes(e.page)) {
    fail(
      `nav-exclude names ${rel(DOCS_DIR)}/${e.page}, which is not on disk -- drop the exclusion`,
    );
  }
}

// 4. Both at once. The two lists then disagree about the same page, and which one wins depends on
//    reading order rather than on anyone's intent.
for (const e of excluded) {
  if (navLocal.includes(e.page)) {
    fail(`${e.page} is both in nav: and nav-excluded -- it cannot be both`);
  }
}

if (failed) {
  console.error('');
  console.error('MKDOCS NAV CHECK FAILED');
  console.error(`  on disk:  ${onDisk.join(', ')}`);
  console.error(`  in nav:   ${navLocal.join(', ')}`);
  console.error(
    `  excluded: ${excluded.map((e) => e.page).join(', ') || '(none)'}`,
  );
  process.exit(1);
}

console.log(
  `OK -- every page under ${rel(DOCS_DIR)} is in nav: or explicitly excluded.`,
);
for (const e of excluded) {
  console.log(`  excluded: ${e.page} -- ${e.reason}`);
}
