#!/usr/bin/env bash
# test-theme-tokens.sh — tests scripts/check-theme-tokens.mjs (#1217, #1221, #1774)
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
# The two cases that matter most are the last two. Scope here is DERIVED -- a page is checked
# against the shared themes only if it links them -- and a derived rule can fail in both
# directions: too narrow (a themed page silently skipped) and too wide (a self-contained page
# reported for properties it defines itself). Both are asserted.
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

DASHBOARD_HTML="pkg/server/dashboard.html"
DASHBOARD_JS="pkg/server/static/dashboard.js"
DASHBOARD_CSS="pkg/server/static/dashboard.css"
PASSCODE_HTML="pkg/server/passcode.html"

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
# The pages that are NOT checked are reported too. An exclusion nobody can see is
# indistinguishable from a scan that quietly stopped looking.
if says "$OUT" 'do not link the shared themes'; then
  pass "the pages held out of the check are named"
else
  fail "the output does not say which pages were skipped: $OUT"
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
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's/<body/<body style="color: var(--probe-selfcontained-token);"/' \
  "$SANDBOX/$PASSCODE_HTML" && rm -f "$SANDBOX/$PASSCODE_HTML.bak"
run
if [ "$RC" -eq 0 ] && ! says "$OUT" 'probe-selfcontained-token'; then
  pass "a self-contained page is not checked against the shared themes"
else
  fail "passcode.html was checked against themes it does not link (rc=$RC): $OUT"
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
# 8. Anti-vacuity. Membership is a <link> in the markup, so deleting that link empties the
#    markup scan while every stylesheet still resolves -- a green run over nothing, which is
#    exactly how the blind spot this pass closes went unnoticed.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's|/static/themes/|/static/nowhere/|g' "$SANDBOX/$DASHBOARD_HTML" &&
  rm -f "$SANDBOX/$DASHBOARD_HTML.bak"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'covered nothing'; then
  pass "a markup scan that covers nothing fails instead of reporting success"
else
  fail "an empty markup scan reported success (rc=$RC): $OUT"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
