#!/usr/bin/env bash
# test-theme-tokens.sh — tests scripts/check-theme-tokens.mjs
#                        (#1217, #1221, #1774, #1784, #1802, #1803)
#
# The check compares custom properties REFERENCED against those DEFINED by every theme.
# #1774 added the markup and script pass, after twenty references to four properties no
# theme defines sat in Portal V1's inline styles for months -- invisible to a gate that read
# only .css, on a portal that keeps most of its styling in `style` attributes.
#
# So most of what is asserted here is that the markup pass FIRES, not that it passes. A scan
# that reports success because it examined nothing reads as coverage and is none -- the lesson
# #1402 wrote down for the EDR guard, which shipped an `--include` matching no pattern and
# reported a clean run over zero files. Every fire-case below checks the exit status AND that
# the offending property is named, from a tree that differs from the real one by one line.
#
# The cases that matter most are the scope ones. Scope here is DERIVED -- a page is checked
# against the shared themes only if it links them -- and a derived rule can fail in both
# directions: too narrow (a themed page silently skipped) and too wide (a self-contained page
# reported for properties it defines itself). Both are asserted.
#
# #1784 added a third direction, which is the one the derived rule got wrong for a year: a page
# that is correctly held out of the SHARED check was then resolved against nothing at all, and
# setup.css sat there referencing three properties it does not define. So "held out" now means
# "resolved against its own tokens", and the difference between the two is asserted below --
# a page's own token passes, a token nothing defines fails, and the tokens a page picks up from
# a stylesheet it LINKS count as its own.
#
# #1802 added a fourth question, asked of a narrower place: inside @media print, does a token's
# VALUE depend on the screen theme? A token that resolves in every theme is still a defect there,
# because print-color-adjust: exact reproduces whatever it resolved to on paper. Cases 11a-11d
# assert the scope in both directions -- a theme-varying token fires, a theme-invariant one does
# not, and a reference outside the block does not -- because "any var() in a print block fails"
# and "the right var()s fail" are indistinguishable from a single fire-case.
#
# #1803 closed the gap case 10 used to pin. Coverage is derived from a <link>, so a stylesheet
# nothing links was read by neither scope; the shared scope now follows <link rel=stylesheet>
# and the run fails naming any .css under pkg/server nothing read. Case 10 is now the inverse of
# what it was -- it asserts the orphan FAILS -- which is the ratchet working: the old case was
# written to go red the day the gap closed, and it did.
#
# One consequence worth stating, because it changed what several cases below can use as a
# fixture: setup.html now links the shared themes (#1804), so it is no longer a self-contained
# page and there is no longer ANY page in the tree that is both self-contained and links a
# stylesheet. Cases 6c and 6d therefore build that fixture in the sandbox instead of borrowing
# setup.html. They previously passed against setup.html for the wrong reason once it moved
# scopes -- rc=0 because the shared scope resolved it -- which is the §5c failure this file
# exists to avoid, so they are not merely re-pointed but re-anchored on a page whose scope the
# case itself establishes.
#
# Runs against a throwaway copy of the tree, never the working tree: a test that mutates
# pkg/server to prove a point and then restores it loses on any interrupted run.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHECK_REL="scripts/check-theme-tokens.mjs"

[ -f "${REPO_ROOT}/${CHECK_REL}" ] || {
  echo "FATAL: ${CHECK_REL} missing"
  exit 1
}
command -v node >/dev/null 2>&1 || {
  echo "FATAL: node not on PATH"
  exit 1
}

PASS=0
FAIL=0
pass() {
  printf '  \033[32mPASS\033[0m  %s\n' "$1"
  PASS=$((PASS + 1))
}
fail() {
  printf '  \033[31mFAIL\033[0m  %s\n' "$1"
  FAIL=$((FAIL + 1))
}

SANDBOX=""
OUT_FILE="$(mktemp "${TMPDIR:-/tmp}/theme-tokens-out.XXXXXX")"
cleanup() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  rm -f "$OUT_FILE"
}
trap cleanup EXIT

# Rebuilds a pristine copy of everything the check reads, so each case starts from a tree
# known to pass and differs from it by one edit.
reset_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/theme-tokens-test.XXXXXX")"
  mkdir -p "$SANDBOX/scripts" "$SANDBOX/pkg/server/static" "$SANDBOX/ui"
  cp "${REPO_ROOT}/${CHECK_REL}" "$SANDBOX/scripts/"
  cp "${REPO_ROOT}"/pkg/server/*.html "$SANDBOX/pkg/server/"
  cp -R "${REPO_ROOT}"/pkg/server/static/. "$SANDBOX/pkg/server/static/"
  cp -R "${REPO_ROOT}"/pkg/server/templates "$SANDBOX/pkg/server/templates"
  cp -R "${REPO_ROOT}"/ui/src "$SANDBOX/ui/src"
}

# run -> sets $OUT and $RC. Deliberately not called through a command substitution: that runs
# in a subshell, so the exit status assigned inside it never reaches the caller and every
# fire-case silently asserts against rc=0.
RC=0
OUT=""
run() {
  (cd "$SANDBOX" && node "$CHECK_REL") >"$OUT_FILE" 2>&1
  RC=$?
  OUT="$(cat "$OUT_FILE")"
}

says() { printf '%s' "$1" | grep -q -- "$2"; }

# A mutation that did not apply is not a mutation. Every sed below is anchored on a string in
# a real file, and a sed whose anchor has moved exits 0 having changed nothing -- so the case
# then runs against the committed tree and passes, reporting a guard that was never tested.
# Cases that expect their edit to FIRE assert it landed first.
landed() {
  grep -q -- "$2" "$SANDBOX/$1" && return 0
  fail "the mutation never applied: $2 not found in $1"
  return 1
}

DASHBOARD_HTML="pkg/server/dashboard.html"
DASHBOARD_JS="pkg/server/static/dashboard.js"
DASHBOARD_CSS="pkg/server/static/dashboard.css"
PASSCODE_HTML="pkg/server/passcode.html"
SETUP_HTML="pkg/server/static/setup.html"
SETUP_CSS="pkg/server/static/setup.css"
A11Y_CSS="pkg/server/static/shared/a11y.css"

# A self-contained page that links a stylesheet -- the fixture cases 6c and 6d need and the
# tree no longer contains. Built here rather than borrowed from a real page so the case
# establishes the scope it is testing instead of inheriting it from whatever setup.html
# happens to link this month.
PROBE_CSS="pkg/server/static/probe-doc-scope.css"
make_self_contained_page_with_sheet() {
  printf ':root {\n  --probe-sheet-token: #123456;\n}\n' >"$SANDBOX/$PROBE_CSS"
  sed -i.bak 's|<body|<link rel="stylesheet" href="/static/probe-doc-scope.css"><body|' \
    "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
}

echo "Testing check-theme-tokens..."

# ---------------------------------------------------------------------------
# 1. The tree as committed passes, and says what it read. A green run has to be evidence the
#    markup pass ran, not evidence it was skipped.
# ---------------------------------------------------------------------------
reset_sandbox
run
if [ "$RC" -eq 0 ]; then
  pass "the committed tree passes"
else
  fail "the committed tree fails: $OUT"
fi
if says "$OUT" 'pkg/server/dashboard.html'; then
  pass "the markup pass names the page it scanned"
else
  fail "no scanned page in the output -- the markup pass did not run: $OUT"
fi
if says "$OUT" 'pkg/server/static/dashboard.js'; then
  pass "the script a scanned page loads is scanned with it"
else
  fail "dashboard.js was not scanned -- V1 renders most of its markup there: $OUT"
fi
# The pages held out of the SHARED check are reported too, with what each was resolved
# against instead. A scope nobody can see is indistinguishable from a scan that quietly
# stopped looking, and until #1784 those two were in fact the same thing here.
if says "$OUT" 'do not link the shared themes'; then
  pass "the pages outside the shared scope are named"
else
  fail "the output does not say which pages were resolved separately: $OUT"
fi
# The stylesheet a themed page LINKS is scanned with it (#1803). a11y.css is the file this
# widening was for: dashboard.html has always linked it, the shared scope has always read
# dashboard.html, and the link was never followed -- so it was covered by nothing at all
# while looking exactly like part of a scanned page.
if says "$OUT" "$DASHBOARD_HTML  +  $A11Y_CSS"; then
  pass "the stylesheet a scanned page links is scanned with it"
else
  fail "a11y.css was not scanned -- both portals load it: $OUT"
fi
# Both new scopes report on a passing run, not only on a failing one. Without this a scope
# that silently stopped running would be invisible here: every fire-case below would still
# pass on the tree it mutates, because a scope that never runs cannot contradict them.
if says "$OUT" 'were read by some scope'; then
  pass "the coverage scope reports on a passing run"
else
  fail "the run does not say how many stylesheets it read: $OUT"
fi
if says "$OUT" 'blocks follows the screen theme'; then
  pass "the print scope reports on a passing run"
else
  fail "the run does not say how many print blocks it read: $OUT"
fi

# ---------------------------------------------------------------------------
# 2. It fires on an undefined property in an inline style attribute. This is the exact shape
#    #1774 was filed for: sixteen of them, in `style="…"`, invisible to a .css-only scan.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's/<div id="dashboard-screen">/<div id="dashboard-screen" style="color: var(--probe-undefined-in-attr);">/' \
  "$SANDBOX/$DASHBOARD_HTML" && rm -f "$SANDBOX/$DASHBOARD_HTML.bak"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-attr'; then
  pass "an undefined token in an inline style attribute fails and is named"
else
  fail "an undefined token in dashboard.html did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 3. It fires on an `element.style.*` assignment. Five of #1774's references were written
#    this way -- not in an attribute at all, so an attribute-only regex would have reported a
#    clean pass over them.
# ---------------------------------------------------------------------------
reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function themeTokenProbeAssign(el) {
  el.style.color = 'var(--probe-undefined-in-assignment)';
}
PROBE
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-assignment'; then
  pass "an undefined token in a style assignment fails and is named"
else
  fail "an undefined token assigned in dashboard.js did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 4. It fires on markup inside a JS template literal, which is where V1 builds its tables and
#    where nine of #1774's references lived.
# ---------------------------------------------------------------------------
reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function themeTokenProbeTemplate(el) {
  el.innerHTML = `<div style="color: var(--probe-undefined-in-template);">x</div>`;
}
PROBE
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-template'; then
  pass "an undefined token in a template literal fails and is named"
else
  fail "an undefined token in a template literal did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 5. The stylesheet pass still fires. Widening a gate must not trade one blind spot for
#    another, and #1217/#1221 are the bugs the .css pass exists for.
# ---------------------------------------------------------------------------
reset_sandbox
printf '\n.theme-token-probe {\n  color: var(--probe-undefined-in-css);\n}\n' \
  >>"$SANDBOX/$DASHBOARD_CSS"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-css'; then
  pass "an undefined token in dashboard.css still fails"
else
  fail "the stylesheet pass stopped firing (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 6. Scope is derived, direction one: a page that does NOT link the shared themes is not
#    resolved against them. passcode.html carries its own tokens in its own <style> block, so
#    checking it here would report every one of them as undefined -- the false positive that
#    makes a maintainer narrow a gate until it stops finding anything.
#
#    --text-primary is one of passcode.html's OWN eleven properties and is defined in no theme
#    file, so a reference to it passes only if the page was resolved against itself.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's/<body/<body style="color: var(--text-primary);"/' \
  "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
run
if [ "$RC" -eq 0 ] && ! says "$OUT" '--text-primary'; then
  pass "a self-contained page's own token resolves against its own <style> block"
else
  fail "passcode.html was checked against themes it does not link (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 6b. Scope is derived, direction three (#1784): held out of the SHARED check is not the same
#     as unchecked. The case above passes just as well on a scan that reads self-contained
#     pages and then does nothing with them -- which is precisely what the gate did until
#     #1784, and how setup.css kept three undefined properties through two widenings.
#
#     Asserts the page is named as well as the property: the shared scope reports property
#     names too, so "the token appears in the output" alone does not say which scope found it.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's/<body/<body style="color: var(--probe-undefined-selfcontained);"/' \
  "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-selfcontained' &&
  says "$OUT" 'does not define'; then
  pass "a self-contained page referencing a token nothing defines fails and is named"
else
  fail "passcode.html's undefined token was not reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 6c. A self-contained page's definitions include the stylesheets it LINKS, not only its own
#     <style> block. This was setup.html until #1804: it defined nothing itself and got all
#     eighteen of its properties from setup.css, so a gate reading only <style> blocks would
#     have reported all eighteen and been turned off within the day. setup.html now links the
#     shared themes, so the fixture is built here instead -- see the note at the top of the
#     file about why re-pointing this case at setup.html would have passed for the wrong
#     reason rather than testing anything.
#
#     --probe-sheet-token is defined ONLY in the linked stylesheet and in no theme, so this
#     passes only if the link was followed and its definitions counted as the page's own.
# ---------------------------------------------------------------------------
reset_sandbox
make_self_contained_page_with_sheet
sed -i.bak 's|<body>|<body style="color: var(--probe-sheet-token);">|' \
  "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
landed "$PASSCODE_HTML" 'probe-sheet-token' &&
  landed "$PASSCODE_HTML" 'probe-doc-scope.css'
run
if [ "$RC" -eq 0 ] && ! says "$OUT" 'probe-sheet-token'; then
  pass "a linked stylesheet's tokens count as the page's own definitions"
else
  fail "passcode.html was not resolved against the stylesheet it links (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 6d. And the other half of the same link: a reference added to that linked stylesheet is
#     found. The stylesheet is in no walked directory of its own -- it is reached only through
#     the page that links it -- so this is the path #1784's five references travelled.
# ---------------------------------------------------------------------------
reset_sandbox
make_self_contained_page_with_sheet
printf '\n.theme-token-probe {\n  color: var(--probe-undefined-in-linked-css);\n}\n' \
  >>"$SANDBOX/$PROBE_CSS"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-linked-css' &&
  says "$OUT" "$PASSCODE_HTML"; then
  pass "an undefined token in a linked stylesheet fails, against the page that links it"
else
  fail "the linked stylesheet's undefined token was not reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 7. Scope is derived, direction two: the same page, once it links the themes, IS checked.
#    Without this the case above proves only that the scan is narrow, not that it is right --
#    a scan that reads nothing would pass it too.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak \
  's|<body|<link rel="stylesheet" href="/static/themes/dark.css"><body style="color: var(--probe-newly-themed-page);"|' \
  "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-newly-themed-page'; then
  pass "a page that starts linking the themes is picked up automatically"
else
  fail "a newly themed page was not covered (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 8. Anti-vacuity, shared scope. Membership is a <link> in the markup, so deleting that link
#    empties the markup scan while every stylesheet still resolves -- a green run over nothing,
#    which is exactly how the blind spot this pass closes went unnoticed.
#
#    Matched on 'markup and script scan', not on 'covered nothing' alone: both scopes now have
#    an anti-vacuity message and both contain that phrase, so the looser match would be
#    satisfied by the wrong one of the two.
#
#    Applied to EVERY page, not just dashboard.html. Two pages link the themes since #1804, so
#    de-theming one of them no longer empties the scope -- it moves that page into the document
#    scope, where it reports its own tokens as undefined and the run goes red with a message
#    about resolution rather than about vacuity. The case caught that itself when setup.html
#    moved: rc was 1 and the message did not match, which is the whole reason it matches on the
#    message.
# ---------------------------------------------------------------------------
reset_sandbox
find "$SANDBOX/pkg/server" -name '*.html' -exec \
  sed -i.bak 's|/static/themes/|/static/nowhere/|g' {} + &&
  find "$SANDBOX/pkg/server" -name '*.html.bak' -delete
landed "$DASHBOARD_HTML" '/static/nowhere/'
run
if [ "$RC" -ne 0 ] && says "$OUT" 'markup and script scan'; then
  pass "a markup scan that covers nothing fails instead of reporting success"
else
  fail "an empty markup scan reported success (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 9. Anti-vacuity, document scope. The mirror of case 8, and the one that has teeth: if the
#    document scan resolves nothing -- because every page joined the shared scope, or because
#    the pattern that finds a page's own <style> block stopped matching -- the run still ends
#    in the shared scope's success message. Every page is given the themes link here, which
#    empties the document scope while leaving the shared one busier than ever.
#
#    Matched on the message, and the exit code alone would NOT do. Measured against the mutant
#    that deletes the guard: the run still exits 1, because forty pages resolved against themes
#    they do not belong to report their own tokens as undefined. An 'exited non-zero' assertion
#    passes on that mutant and reports a working guard that is not there.
# ---------------------------------------------------------------------------
reset_sandbox
find "$SANDBOX/pkg/server" -name '*.html' -exec \
  sed -i.bak 's|<body|<link rel="stylesheet" href="/static/themes/dark.css"><body|' {} + &&
  find "$SANDBOX/pkg/server" -name '*.html.bak' -delete
run
if [ "$RC" -ne 0 ] && says "$OUT" 'document scan'; then
  pass "a document scan that covers nothing fails instead of reporting success"
else
  fail "an empty document scan reported success (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 10. Coverage, direction one (#1803). This case used to assert the OPPOSITE: it dropped an
#     orphan stylesheet into the sandbox and required the gate to stay green, pinning a known
#     blind spot so that closing it could not happen by accident. It went red the moment the
#     shared scope started following <link rel=stylesheet>, which is the ratchet doing its job
#     -- a deferral that fails the build when it becomes stale, rather than an exclusion that
#     quietly outlives the thing it excused.
#
#     So it is now inverted. A stylesheet nothing links must FAIL and be named.
# ---------------------------------------------------------------------------
reset_sandbox
printf '.orphan-probe {\n  color: var(--probe-in-unlinked-stylesheet);\n}\n' \
  >"$SANDBOX/pkg/server/static/orphan-probe.css"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'orphan-probe.css' &&
  says "$OUT" 'no scope read'; then
  pass "a stylesheet no page links fails the run and is named"
else
  fail "an unlinked stylesheet was not reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 10b. Coverage, direction two: it is the LINK that covers a stylesheet, not a list of names
#      in the script. Without this, case 10 is satisfied by a gate that hardcodes the files it
#      expects and reports anything else as an orphan -- which would pass every case here and
#      be wrong the first time someone adds a stylesheet.
#
#      a11y.css is the real instance. Removing dashboard.html's link to it, and nothing else,
#      has to turn a covered file into an orphan.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's|<link rel="stylesheet" href="/static/shared/a11y.css">||' \
  "$SANDBOX/$DASHBOARD_HTML" && rm -f "$SANDBOX/$DASHBOARD_HTML.bak"
if grep -q 'shared/a11y.css' "$SANDBOX/$DASHBOARD_HTML"; then
  fail "the mutation never applied: dashboard.html still links a11y.css"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" "$A11Y_CSS" && says "$OUT" 'no scope read'; then
    pass "coverage follows the link: unlinking a stylesheet orphans it"
  else
    fail "a11y.css stayed covered with nothing linking it (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 11. The print scope (#1802). A token that resolves in every theme is still wrong inside
#     @media print if its VALUE differs between them, because print-color-adjust: exact
#     reproduces it on paper -- so the printout follows whichever theme the reader was using.
#
#     --bg-base is theme-varying (#09090b dark, #f8fafc light, and two more). A fresh @media
#     print block is appended rather than the committed one edited, so this also proves a file
#     with more than one print block has all of them read.
# ---------------------------------------------------------------------------
reset_sandbox
printf '\n@media print {\n  .print-probe {\n    background: var(--bg-base);\n  }\n}\n' \
  >>"$SANDBOX/$DASHBOARD_CSS"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'follow the screen theme' &&
  says "$OUT" '\-\-bg-base' && says "$OUT" "$DASHBOARD_CSS"; then
  pass "a theme-varying colour inside @media print fails, named with its file"
else
  fail "a theme-varying colour in a print block was not reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 11b. The same reference, OUTSIDE the print block, must not fire. Without this, case 11 is
#      equally satisfied by a check that flags --bg-base anywhere in the file -- which would
#      be a check on the wrong thing, and would fail on the hundred legitimate screen rules
#      that use it two lines further down.
# ---------------------------------------------------------------------------
reset_sandbox
printf '\n.print-probe {\n  background: var(--bg-base);\n}\n' \
  >>"$SANDBOX/$DASHBOARD_CSS"
run
if [ "$RC" -eq 0 ]; then
  pass "the same token outside a print block is not reported"
else
  fail "a screen rule using a theme-varying token was reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 11c. And a theme-INVARIANT token inside a print block must not fire either. The spacing
#      scale is defined once and identically for every theme, so a print rule is welcome to
#      use it -- the defect is "the value follows the theme", not "a var() appears in print".
#      Without this case, "flag every var() inside @media print" passes 11 and 11b both.
# ---------------------------------------------------------------------------
reset_sandbox
printf '\n@media print {\n  .print-probe {\n    padding: var(--spacing-md);\n  }\n}\n' \
  >>"$SANDBOX/$DASHBOARD_CSS"
run
if [ "$RC" -eq 0 ]; then
  pass "a theme-invariant token inside @media print is not reported"
else
  fail "--spacing-md is identical in every theme but was reported (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 11d. Anti-vacuity, print scope. Its healthy state is ZERO findings, so a scope that has
#      stopped extracting blocks reports exactly what a clean tree reports. Nothing else in
#      this repo renders a print rule, and review does not catch them either -- #1221 and
#      #1784 both shipped a print defect past review -- so a silent scan of nothing here is
#      the whole failure mode.
#
#      Renaming the at-rule leaves every declaration in place and every other check green, so
#      the run can only go red for this reason.
# ---------------------------------------------------------------------------
reset_sandbox
find "$SANDBOX" -name '*.css' -exec \
  sed -i.bak 's|@media print|@media screen|g' {} + &&
  find "$SANDBOX" -name '*.css.bak' -delete
if grep -q '@media print' "$SANDBOX/$DASHBOARD_CSS"; then
  fail "the mutation never applied: dashboard.css still has an @media print block"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'print scan found 0 blocks'; then
    pass "a print scan that covers nothing fails instead of reporting success"
  else
    fail "an empty print scan reported success (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 12. Anti-vacuity, reference side. Every scope in the checker finds its work with one
#     pattern, VAR_REF, so if that stops matching they all go quiet together and the run ends
#     on the success message having resolved nothing. This is the one guard the scope-level
#     cases cannot give: they prove a scope was handed files, not that anything was read out
#     of them.
#
#     The subject here is the checker itself rather than the tree, because that is where the
#     failure would live. It is also the only in-script cover the print scope's reference half
#     gets -- a clean tree has zero print references, so the checker cannot assert it found
#     one, and case 11 is what proves that half runs.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's|^const VAR_REF = .*|const VAR_REF = /__never_matches_anything__(--[a-z0-9-]+)/g;|' \
  "$SANDBOX/$CHECK_REL" && rm -f "$SANDBOX/$CHECK_REL.bak"
if grep -q '__never_matches_anything__' "$SANDBOX/$CHECK_REL"; then
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'Not one var() reference'; then
    pass "a reference pattern that matches nothing fails instead of passing"
  else
    fail "VAR_REF matching nothing reported success (rc=$RC): $OUT"
  fi
else
  fail "the mutation never applied: VAR_REF was not replaced"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
