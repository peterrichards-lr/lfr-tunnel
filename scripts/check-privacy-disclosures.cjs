#!/usr/bin/env node
/**
 * check-privacy-disclosures.cjs -- the served /privacy page must cover every category PRIVACY.md
 * discloses, in every language it is served in (#1954).
 *
 * There are two privacy texts in this repo. `PRIVACY.md` is the canonical one. The page a user
 * actually reads is `pkg/server/templates/<lang>/privacy.html`, rendered by handlePrivacyFallback
 * (`pkg/server/server.go:5797`) -- separate, hand-written prose, in five languages, with no i18n
 * keys, so `check-i18n-keys.cjs` cannot see it and nothing else did either.
 *
 * It drifted twice before anyone noticed:
 *   - #1894 added §1.D (the gateway-side diagnostic log store). The served page never got it, and
 *     `policy_version` was bumped to `2026-09-11-diagnostics-store` -- so every user was put into a
 *     14-day re-consent window over a disclosure that was absent from the page they would open to
 *     read it.
 *   - #1955 added §1.E (anonymous geographic distribution). The served page did not get that either.
 *
 * So this compares rather than documents, the same reason check-i18n-keys.cjs and
 * check-status-vocabulary.cjs exist.
 *
 * ## The property
 *
 * Exact text equality across five languages is not achievable and would be the wrong contract
 * anyway -- the served page is deliberately a summary. What IS checkable is coverage:
 *
 *   every `### <Letter>. <Title>` subsection of PRIVACY.md §1 has an element marked
 *   `data-disclosure="<Letter>"` in EVERY served privacy.html, that element carries at least
 *   MIN_SECTION_CHARS characters of visible text, and no template marks a letter §1 does not define.
 *
 * The letter is the join key precisely because it is the one thing that survives translation.
 *
 * ## What it does NOT check, deliberately -- and this is the limit to know
 *
 * It reads structure, not prose. A section can be present, substantial, and say something the code
 * does not do; and an edit to the BODY of an existing §1 subsection (say §1.D's retention period
 * changing from 30 days to 60) changes no heading, so this stays green. Those cases still need a
 * human. What this stops is the specific failure that happened twice: §1 gains a category and the
 * served page silently does not.
 *
 * `tests/hooks/test-privacy-disclosures.sh` pins that boundary as a BOUNDING case rather than
 * leaving it as a comment, per AGENTS.md / github-workflow §5b rule 6: prose does not fail.
 *
 * ## Fails closed
 *
 * If PRIVACY.md parses to no subsections, or the template glob matches no files, the check exits 1
 * rather than reporting that two empty sets agree. This repo has found five gates that could not
 * fail (#1779).
 */

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');
const rel = (p) => path.relative(REPO, p);

const CANONICAL = path.join(REPO, 'PRIVACY.md');
const TEMPLATE_DIR = path.join(REPO, 'pkg/server/templates');
const TEMPLATE_NAME = 'privacy.html';

// A marker on an element with nothing in it is a marker that lies. 120 characters is comfortably
// below the shortest real subsection (§1.C, the audit-log one) and far above a placeholder.
const MIN_SECTION_CHARS = 120;

let failed = false;
const fail = (msg) => {
  console.error(`  ${msg}`);
  failed = true;
};

if (!fs.existsSync(CANONICAL)) {
  console.error(`check-privacy-disclosures: ${rel(CANONICAL)} not found.`);
  console.error(
    '  If the canonical policy moved, move this check with it rather than deleting the check.',
  );
  process.exit(1);
}

// -- The canonical side. Scoped to the "## 1." section on purpose: a `###` heading elsewhere in the
//    document is not a category of processing, and counting it would make the gate demand a
//    counterpart for something that is not one.
const md = fs.readFileSync(CANONICAL, 'utf8');
const sectionOneStart = md.search(/^## 1\. /m);
if (sectionOneStart === -1) {
  console.error(
    `check-privacy-disclosures: could not find the "## 1." section in ${rel(CANONICAL)}.`,
  );
  console.error(
    '  That section is what enumerates the categories of processing; without it there is nothing to compare.',
  );
  process.exit(1);
}
const afterOne = md.slice(sectionOneStart + 1).search(/^## /m);
const sectionOne =
  afterOne === -1
    ? md.slice(sectionOneStart)
    : md.slice(sectionOneStart, sectionOneStart + 1 + afterOne);

const canonical = new Map(); // letter -> title
for (const m of sectionOne.matchAll(/^### ([A-Z])\.\s+(.+?)\s*$/gm)) {
  canonical.set(m[1], m[2]);
}

// -- The served side. Discovered rather than listed, so a sixth language added later is covered by
//    default instead of by someone remembering.
let templates = [];
if (fs.existsSync(TEMPLATE_DIR)) {
  templates = fs
    .readdirSync(TEMPLATE_DIR, { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .map((e) => path.join(TEMPLATE_DIR, e.name, TEMPLATE_NAME))
    .filter((p) => fs.existsSync(p))
    .sort();
}

// -- Anti-vacuity. Two empty sets agree perfectly, and so does a set compared against no files.
if (canonical.size === 0) {
  console.error(
    `check-privacy-disclosures: §1 of ${rel(CANONICAL)} parsed to zero subsections.`,
  );
  console.error(
    '  Refusing to report that the served page covers an empty policy.',
  );
  process.exit(1);
}
if (templates.length === 0) {
  console.error(
    `check-privacy-disclosures: found no ${TEMPLATE_NAME} under ${rel(TEMPLATE_DIR)}/*/.`,
  );
  console.error(
    '  The served page is what users read; a check with nothing to read is not a check.',
  );
  process.exit(1);
}

const letters = [...canonical.keys()].sort();
console.log(
  `Checking the served /privacy page against ${rel(CANONICAL)} §1 ` +
    `(${letters.length} categories: ${letters.join(', ')}; ${templates.length} languages)...`,
);

const visibleText = (html) =>
  html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&[a-zA-Z]+;|&#\d+;/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

for (const file of templates) {
  const lang = path.basename(path.dirname(file));
  const src = fs.readFileSync(file, 'utf8');

  const found = new Map(); // letter -> visible text
  for (const m of src.matchAll(
    /<section\b[^>]*\bdata-disclosure="([^"]*)"[^>]*>([\s\S]*?)<\/section>/g,
  )) {
    // A nested <section> would make the non-greedy close above the wrong one, so the block this
    // read is not the block that exists. Fail rather than measure the wrong thing.
    if (/<section\b/.test(m[2])) {
      fail(
        `[${lang}] data-disclosure="${m[1]}" contains a nested <section>; this check cannot read it reliably.`,
      );
      continue;
    }
    if (found.has(m[1])) {
      fail(`[${lang}] data-disclosure="${m[1]}" appears more than once.`);
      continue;
    }
    found.set(m[1], visibleText(m[2]));
  }

  const missing = letters.filter((l) => !found.has(l));
  const extra = [...found.keys()].filter((l) => !canonical.has(l)).sort();

  for (const l of missing) {
    fail(
      `[${lang}] ${rel(file)} has no counterpart for §1.${l} "${canonical.get(l)}"`,
    );
  }
  for (const l of extra) {
    fail(
      `[${lang}] ${rel(file)} marks data-disclosure="${l}", which §1 of ${rel(CANONICAL)} does not define`,
    );
  }
  for (const l of letters) {
    const text = found.get(l);
    if (text === undefined) continue;
    if (text.length < MIN_SECTION_CHARS) {
      fail(
        `[${lang}] §1.${l} is marked but holds only ${text.length} characters of text ` +
          `(minimum ${MIN_SECTION_CHARS}) -- a marker on an empty element is not a disclosure.`,
      );
    }
  }
}

if (failed) {
  console.error('');
  console.error('PRIVACY DISCLOSURE COVERAGE CHECK FAILED');
  console.error(
    `  ${rel(CANONICAL)} §1 discloses: ${letters.map((l) => `${l} (${canonical.get(l)})`).join(', ')}`,
  );
  console.error('');
  console.error(
    '  Add a <section data-disclosure="<letter>"> to EVERY pkg/server/templates/*/privacy.html,',
  );
  console.error(
    '  summarising that subsection in that language. Users read the served page, not PRIVACY.md,',
  );
  console.error(
    '  so a category that exists only in the canonical document has not actually been disclosed.',
  );
  console.error('');
  console.error(
    '  If you genuinely cannot write a faithful translation, say so in the PR rather than shipping',
  );
  console.error(
    '  a guess -- a policy that says something different in one language is worse than one that is',
  );
  console.error('  briefly English-only with an honest note.');
  process.exit(1);
}

console.log(
  `OK -- all ${templates.length} served privacy pages cover §1.${letters.join(', §1.')}`,
);
