#!/usr/bin/env bash
# test-v1-usage-parity.sh — the two Portal V1 gates must see the same corpus (#1841)
#
# scripts/check-css-modifiers.cjs and scripts/check-theme-tokens.mjs ask different questions of
# the same body of source: "is this class defined anywhere the page can reach" and "does this
# custom property resolve". They need the same INPUT to ask them — every class and token Portal
# V1 actually uses, from markup, inline styles, and the stylesheets and scripts a page links.
#
# They each grew their own collector, and each missed a different half:
#
#   #1744  check-css-modifiers.cjs did not read Portal V1 at all. Pointing it at V1 found 25
#          undefined classes, one of which -- action-menu-content -- left the vanity-domain
#          menu permanently open in production.
#   #1774  check-theme-tokens.mjs read stylesheets only. Sixteen inline styles referenced
#          --text / --text-color, which no theme defines.
#
# Both were then widened, separately, so neither is an open defect. What this file asserts is
# the property that made the asymmetry possible in the first place, and which no test either
# gate owns can see: a collector cannot test for input it never learned to find, so the only
# way to catch "one of them learned a new syntax and the other did not" is to plant the same
# construct in the same place and require BOTH to react.
#
# Each case below therefore plants a class AND a custom property in ONE location, and asserts
# check-css-modifiers names the class while check-theme-tokens names the property. A case that
# fails on one side and passes on the other is exactly the shape of #1744 and #1774, and it is
# now a red build rather than a comment in a script.
#
# WHICH CASES FAILED BEFORE THE SHARED PASS, AND WHAT THEY SAID (github-workflow §5c):
#
#   "a document in a subdirectory of pkg/server"
#       check-css-modifiers exited 0 -- its document list was pkg/server/*.html plus
#       pkg/server/static/*.html, non-recursive, so 34 templates were invisible to it.
#       check-theme-tokens named --probe-token-subdir correctly. One gate, not the other.
#
#   "a page that links its stylesheet and script by a relative href"
#       check-theme-tokens exited non-zero, but for the WRONG REASON: it matched only
#       href="/static/*.css", never followed the link, and reported
#       "Stylesheets under pkg/server that no scope read: pkg/server/probe/probe.css".
#       It never saw --probe-token-relsheet at all. check-css-modifiers resolved the same
#       relative href without trouble and named the class. One gate, not the other.
#
# Runs against a throwaway copy of the tree, never the working tree.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CSS_REL="scripts/check-css-modifiers.cjs"
TOK_REL="scripts/check-theme-tokens.mjs"

for f in "$CSS_REL" "$TOK_REL"; do
  [ -f "${REPO_ROOT}/${f}" ] || {
    echo "FATAL: ${f} missing"
    exit 1
  }
done
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
OUT_FILE="$(mktemp "${TMPDIR:-/tmp}/v1-parity-out.XXXXXX")"
cleanup() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  rm -f "$OUT_FILE"
}
trap cleanup EXIT

# Everything BOTH gates read. The two existing suites each build their own copy of the tree, and
# they had diverged in the same direction the gates had: test-css-modifiers.sh did not copy
# pkg/server/templates, because the gate it tests could not see those documents anyway. Here
# there is one tree and both gates run against it, so a case cannot pass on one side because
# that side was handed a smaller corpus.
reset_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/v1-parity.XXXXXX")"
  mkdir -p "$SANDBOX/scripts" "$SANDBOX/pkg/server/static" "$SANDBOX/ui"
  cp "${REPO_ROOT}/${CSS_REL}" "${REPO_ROOT}/${TOK_REL}" "$SANDBOX/scripts/"
  if [ -d "${REPO_ROOT}/scripts/lib" ]; then
    cp -R "${REPO_ROOT}/scripts/lib" "$SANDBOX/scripts/lib"
  fi
  cp "${REPO_ROOT}"/pkg/server/*.html "$SANDBOX/pkg/server/"
  cp -R "${REPO_ROOT}"/pkg/server/static/. "$SANDBOX/pkg/server/static/"
  cp -R "${REPO_ROOT}"/pkg/server/templates "$SANDBOX/pkg/server/templates"
  cp -R "${REPO_ROOT}"/ui/src "$SANDBOX/ui/src"
  # The client inspector (#1779). Both V1 gates now walk pkg/client as well as pkg/server: it is
  # a page of the same kind, written in the same idiom, and it was outside both scans. Copied
  # here so the sandbox corpus is the one CI checks -- an absent directory is walked as empty,
  # which passes, so leaving it out would quietly shrink every case below.
  mkdir -p "$SANDBOX/pkg/client"
  cp "${REPO_ROOT}"/pkg/client/*.html "$SANDBOX/pkg/client/"
}

# run_gate <script-rel> -> $RC, $OUT. Not called through a command substitution: that runs in a
# subshell, so the exit status assigned inside it never reaches the caller and every fire-case
# silently asserts against rc=0.
RC=0
OUT=""
run_gate() {
  (cd "$SANDBOX" && node "$1") >"$OUT_FILE" 2>&1
  RC=$?
  OUT="$(cat "$OUT_FILE")"
  # The harness failing instead of the subject (github-workflow §5c.5). A gate that cannot load
  # the shared pass exits non-zero and prints a stack trace, which satisfies every "rc is
  # non-zero" below while having examined nothing -- and this whole file is about the shared
  # pass, so it is the most likely thing to be missing from a sandbox.
  case "$OUT" in
  # One pattern: ERR_MODULE_NOT_FOUND (the ESM form) ends in the same substring as
  # MODULE_NOT_FOUND (the CJS form), so this matches both.
  *MODULE_NOT_FOUND*)
    fail "the sandbox is missing the shared collection pass -- reset_sandbox is incomplete, and every assertion below would pass on the crash:
$(printf '%s' "$OUT" | head -6)"
    ;;
  esac
}

says() { printf '%s' "$1" | grep -q -- "$2"; }

# The core assertion. One location, one class, one property, both gates required to react.
#
# The exit status alone is not the check (§5c): both of these gates exit non-zero for a dozen
# unrelated reasons -- a stale exemption, an unread stylesheet, a print colour -- so each side
# asserts the NAME of the thing planted. That is the only outcome the planted construct can
# produce, and it is what tells a class-side miss apart from a token-side one.
parity_case() {
  local label="$1" class_needle="$2" token_needle="$3"

  run_gate "$CSS_REL"
  if [ "$RC" -ne 0 ] && says "$OUT" "$class_needle"; then
    pass "${label}: check-css-modifiers names ${class_needle}"
  else
    fail "${label}: check-css-modifiers did not name ${class_needle} (rc=$RC) -- the class half of the collection does not reach here:
$(printf '%s' "$OUT" | tail -20)"
  fi

  run_gate "$TOK_REL"
  if [ "$RC" -ne 0 ] && says "$OUT" "$token_needle"; then
    pass "${label}: check-theme-tokens names ${token_needle}"
  else
    fail "${label}: check-theme-tokens did not name ${token_needle} (rc=$RC) -- the token half of the collection does not reach here:
$(printf '%s' "$OUT" | tail -20)"
  fi
}

# The counterpart. A pair of gates that fail on everything is not a pair of gates.
both_quiet() {
  local label="$1"
  run_gate "$CSS_REL"
  if [ "$RC" -eq 0 ]; then
    pass "${label}: check-css-modifiers stays quiet"
  else
    fail "${label}: check-css-modifiers fired (rc=$RC):
$(printf '%s' "$OUT" | tail -20)"
  fi
  run_gate "$TOK_REL"
  if [ "$RC" -eq 0 ]; then
    pass "${label}: check-theme-tokens stays quiet"
  else
    fail "${label}: check-theme-tokens fired (rc=$RC):
$(printf '%s' "$OUT" | tail -20)"
  fi
}

# A mutation that did not apply is not a mutation: a sed whose anchor has moved exits 0 having
# changed nothing, and the case then runs against the pristine tree and reports a guard that was
# never exercised. Every plant is verified to have landed.
planted() {
  if grep -q -- "$2" "$SANDBOX/$1"; then
    return 0
  fi
  fail "the probe never landed in $1 -- its anchor has moved, and the case below would have run against the unmodified tree"
  return 1
}

echo "Testing V1 usage-collection parity (check-css-modifiers / check-theme-tokens)..."

# ---------------------------------------------------------------------------
# 0. The committed tree passes both gates. Without this the fire-cases below prove nothing:
#    a tree that already fails would satisfy every "rc is non-zero" on its own.
# ---------------------------------------------------------------------------
reset_sandbox
both_quiet "the committed tree"

# ---------------------------------------------------------------------------
# 1. A themed top-level page. The control: this location has always been read by both, so a
#    failure here means the harness is wrong, not the gates.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak \
  's|<div id="dashboard-screen">|<div id="dashboard-screen"><span class="probe-class-toplevel" style="color: var(--probe-token-toplevel)"></span>|' \
  "$SANDBOX/pkg/server/dashboard.html" && rm -f "$SANDBOX/pkg/server/dashboard.html.bak"
if planted "pkg/server/dashboard.html" "probe-class-toplevel"; then
  parity_case "a themed top-level page" "probe-class-toplevel" "probe-token-toplevel"
fi

# ---------------------------------------------------------------------------
# 2. A JS template literal. Portal V1 renders most of its tables from these, and it is where
#    #1744's own defect lived (dashboard.js applied `alert alert-warning` with no rule).
# ---------------------------------------------------------------------------
reset_sandbox
cat >>"$SANDBOX/pkg/server/static/dashboard.js" <<'PROBE'
function v1ParityProbeTemplate() {
  return `<div class="probe-class-template" style="color: var(--probe-token-template)"></div>`;
}
PROBE
if planted "pkg/server/static/dashboard.js" "probe-class-template"; then
  parity_case "a JS template literal" "probe-class-template" "probe-token-template"
fi

# ---------------------------------------------------------------------------
# 3. A document in a SUBDIRECTORY of pkg/server. Pre-refactor this fired on the token gate and
#    not on the class gate: check-css-modifiers listed pkg/server/*.html and
#    pkg/server/static/*.html only, so the 34 localized templates were invisible to it while
#    check-theme-tokens walked the tree and read all of them.
#
#    The page is self-contained (it does not link the shared themes), so the token half is
#    answered by the document scope and the class half by that page's own <style> block.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak \
  's|<body>|<body><span class="probe-class-subdir" style="color: var(--probe-token-subdir)"></span>|' \
  "$SANDBOX/pkg/server/templates/en/cookies.html" &&
  rm -f "$SANDBOX/pkg/server/templates/en/cookies.html.bak"
if planted "pkg/server/templates/en/cookies.html" "probe-class-subdir"; then
  parity_case "a document in a subdirectory" "probe-class-subdir" "probe-token-subdir"
fi

# ---------------------------------------------------------------------------
# 4. A page that links its stylesheet and its script by a RELATIVE href. Pre-refactor this
#    fired on the class gate and not on the token gate: check-theme-tokens matched only
#    href="/static/…", so it never opened either file. It still exited non-zero -- but on
#    "Stylesheets under pkg/server that no scope read", a different finding about a different
#    file, which is why this case asserts the token name rather than the exit status.
# ---------------------------------------------------------------------------
reset_sandbox
mkdir -p "$SANDBOX/pkg/server/probe"
cat >"$SANDBOX/pkg/server/probe/page.html" <<'PROBE'
<!doctype html>
<html>
  <head>
    <link rel="stylesheet" href="probe.css" />
    <script src="probe.js"></script>
  </head>
  <body>
    <div class="probe-class-relsheet"></div>
  </body>
</html>
PROBE
cat >"$SANDBOX/pkg/server/probe/probe.css" <<'PROBE'
.probe-defined-here {
  color: var(--probe-token-relsheet);
}
PROBE
cat >"$SANDBOX/pkg/server/probe/probe.js" <<'PROBE'
function v1ParityProbeRelScript(el) {
  el.classList.add('probe-class-relscript');
  el.style.color = 'var(--probe-token-relscript)';
}
PROBE
if planted "pkg/server/probe/page.html" "probe-class-relsheet"; then
  parity_case "a relatively-linked stylesheet" "probe-class-relsheet" "probe-token-relsheet"
  parity_case "a relatively-linked script" "probe-class-relscript" "probe-token-relscript"
fi

# ---------------------------------------------------------------------------
# 5. Narrowness controls. Each of these is a construct one gate must NOT report, and they are
#    here rather than in the two per-gate suites because the shared pass is where a widening
#    would break them both at once.
# ---------------------------------------------------------------------------

# 5a. A class read back through querySelector is a behaviour hook, not styling.
reset_sandbox
cat >>"$SANDBOX/pkg/server/static/dashboard.js" <<'PROBE'
function v1ParityProbeHook(el) {
  el.classList.add('probe-behaviour-hook');
  return document.querySelectorAll('.probe-behaviour-hook');
}
PROBE
both_quiet "a behaviour hook"

# 5b. Commented-out markup is not live styling, for EITHER gate. This is the one behaviour the
#     shared pass changed rather than preserved, and it is asserted here rather than described
#     because it is a narrowing: before #1841, check-css-modifiers read .html without stripping
#     `<!-- -->` and reported `.probe-class-in-comment` at dashboard.html:225 as an undefined
#     class, while check-theme-tokens had stripped HTML comments since #1774 and reported
#     nothing. Two gates, one document, opposite answers -- which is the disagreement this
#     refactor exists to remove, and the token gate's answer is the correct one: the browser
#     never sees that element, so demanding a CSS rule for it fails a build over dead markup.
#
#     Stripping applies to .html only. A `<!--` inside a JS template literal is a string, not a
#     comment, so nothing in dashboard.js is hidden by this -- which is the arm that would
#     actually matter, since that is where V1 renders most of its markup.
reset_sandbox
sed -i.bak \
  's|<div id="dashboard-screen">|<!-- <div class="probe-commented-class" style="color: var(--probe-commented-token)"> --><div id="dashboard-screen">|' \
  "$SANDBOX/pkg/server/dashboard.html" && rm -f "$SANDBOX/pkg/server/dashboard.html.bak"
if planted "pkg/server/dashboard.html" "probe-commented-class"; then
  both_quiet "a class and a token inside an HTML comment"
fi

# 5c. ...and the control that keeps 5b from being a hole. The same two probes, in live markup
#     four characters away from the commented form, must still fire on both. Without this,
#     stripping every `<` would satisfy 5b.
reset_sandbox
sed -i.bak \
  's|<div id="dashboard-screen">|<div class="probe-live-class" style="color: var(--probe-live-token)"></div><div id="dashboard-screen">|' \
  "$SANDBOX/pkg/server/dashboard.html" && rm -f "$SANDBOX/pkg/server/dashboard.html.bak"
if planted "pkg/server/dashboard.html" "probe-live-class"; then
  parity_case "the same markup uncommented" "probe-live-class" "probe-live-token"
fi

# 5d. A property a self-contained page defines itself resolves against that page.
reset_sandbox
sed -i.bak \
  's|<body>|<body><span style="color: var(--probe-own-token)"></span>|' \
  "$SANDBOX/pkg/server/templates/en/cookies.html" &&
  rm -f "$SANDBOX/pkg/server/templates/en/cookies.html.bak"
sed -i.bak \
  's|<style>|<style>:root{--probe-own-token:#123456;}|' \
  "$SANDBOX/pkg/server/templates/en/cookies.html" &&
  rm -f "$SANDBOX/pkg/server/templates/en/cookies.html.bak"
if planted "pkg/server/templates/en/cookies.html" "probe-own-token"; then
  both_quiet "a token a self-contained page defines"
fi

# ---------------------------------------------------------------------------
# 6. Both gates must AGREE on the corpus, not merely each be wide enough today. A new document
#    in a new subdirectory has to show up in both scope reports -- the class gate counts the
#    documents it examined, the token gate lists the pages it resolved. If a future edit teaches
#    one of them about a new location and not the other, this goes red before any defect has to
#    exist to expose it.
# ---------------------------------------------------------------------------
reset_sandbox
run_gate "$CSS_REL"
before="$(printf '%s' "$OUT" | sed -n 's/.*\[V1\]: OK -- .* across \([0-9]*\) Portal V1 document.*/\1/p')"

mkdir -p "$SANDBOX/pkg/server/corpus"
cat >"$SANDBOX/pkg/server/corpus/page.html" <<'PROBE'
<!doctype html>
<html>
  <head>
    <style>
      :root {
        --corpus-token: #112233;
      }
      .corpus-class {
        color: var(--corpus-token);
      }
    </style>
  </head>
  <body>
    <div class="corpus-class"></div>
  </body>
</html>
PROBE

run_gate "$CSS_REL"
after="$(printf '%s' "$OUT" | sed -n 's/.*\[V1\]: OK -- .* across \([0-9]*\) Portal V1 document.*/\1/p')"
if [ -n "$before" ] && [ -n "$after" ] && [ "$after" -eq $((before + 1)) ]; then
  pass "a new document in a new subdirectory enters check-css-modifiers' corpus (${before} -> ${after})"
else
  fail "check-css-modifiers examined '${before}' documents and then '${after}' -- a page in a new subdirectory did not enter its corpus"
fi

run_gate "$TOK_REL"
if says "$OUT" 'pkg/server/corpus/page.html'; then
  pass "the same document enters check-theme-tokens' corpus"
else
  fail "check-theme-tokens did not report pkg/server/corpus/page.html -- the two gates disagree about what Portal V1 is:
$(printf '%s' "$OUT" | tail -20)"
fi

# ---------------------------------------------------------------------------
# 7. The shared pass is load-bearing, and a gate must refuse rather than pass when it stops
#    finding anything. One collector means one place to break; the counterpart to that is that
#    breaking it has to be loud in every consumer, or the saving is bought with a silent gate.
#
#    check-theme-tokens' half of this is asserted in tests/hooks/test-theme-tokens.sh, which
#    breaks the same file's VAR_REF and requires "Not one var() reference". Here is the class
#    half, which nothing else covers.
# ---------------------------------------------------------------------------
reset_sandbox
COLLECTOR="scripts/lib/collect-v1-usage.cjs"
sed -i.bak 's|^const CLASS_ATTR = .*|const CLASS_ATTR = /__never_matches_anything__\\s*=\\s*"([^"]*)"/g;|' \
  "$SANDBOX/$COLLECTOR" && rm -f "$SANDBOX/$COLLECTOR.bak"
if grep -q '__never_matches_anything__' "$SANDBOX/$COLLECTOR"; then
  run_gate "$CSS_REL"
  if [ "$RC" -ne 0 ] && says "$OUT" 'pass over nothing'; then
    pass "a class pattern that matches nothing makes check-css-modifiers refuse"
  else
    fail "check-css-modifiers reported success with the shared class pattern matching nothing (rc=$RC):
$(printf '%s' "$OUT" | tail -10)"
  fi
else
  fail "the mutation never applied: CLASS_ATTR was not replaced in $COLLECTOR"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
