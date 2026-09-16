#!/usr/bin/env bash
# test-print-selectors.sh -- assert check-print-selectors.cjs actually fires (#1916).
#
# The defect it guards: a print stylesheet rule targeting a class nothing renders. `.app-container`
# was reset in @media print to release the viewport confinement; no component has ever carried
# that class, so every V2 export was clipped to a single page -- and the stylesheet looked right,
# because a rule that matches nothing is indistinguishable from a rule that works.
#
# Every case plants a fixture and demands a specific verdict, including the vacuity case where
# both sides parse to nothing and a naive implementation reports agreement.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-print-selectors.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/print-sel.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-print-selectors.cjs is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# run_case <label> <expected-exit> <print-css-body> <v2-markup> <v1-markup>
run_case() {
    local label="$1" want="$2" css="$3" v2markup="$4" v1markup="$5"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/scripts" "$dir/ui/src" "$dir/pkg/server/static"
    cp "$GATE" "$dir/scripts/"

    printf '@media print {\n%s\n}\n' "$css" > "$dir/ui/src/index.css"
    printf '@media print {\n%s\n}\n' "$css" > "$dir/pkg/server/static/dashboard.css"
    printf '%s\n' "$v2markup" > "$dir/ui/src/App.tsx"
    printf '%s\n' "$v1markup" > "$dir/pkg/server/dashboard.html"
    printf '// no dynamic classes\n' > "$dir/pkg/server/static/dashboard.js"

    ( cd "$dir" && node scripts/check-print-selectors.cjs >/dev/null 2>&1 )
    local got=$?
    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-print-selectors.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Print selector gate cases:"
echo ""

# PREMISE. Without this every failing case below could be passing because the gate refuses
# everything rather than because it detects the specific defect.
run_case "a selector that matches real markup" 0 \
    '  .sidebar { display: none; }' \
    '<div className="sidebar" />' \
    '<div class="sidebar"></div>'

# FIRING. The #1916 defect exactly: a print rule targeting a class nothing renders.
run_case "a print rule targeting a class nothing renders" 1 \
    '  .app-container { height: auto; }' \
    '<div className="sidebar" />' \
    '<div class="sidebar"></div>'

# FIRING. One arm correct, the other not -- a per-arm check must not be satisfied by its sibling.
run_case "dead in one arm only is still a failure" 1 \
    '  .only-in-v1 { display: none; }' \
    '<div className="something-else" />' \
    '<div class="only-in-v1"></div>'

# FIRING. A comment naming the dead selector must not rescue it. Documenting a bug is not
# fixing it, and an earlier version of this gate passed for exactly that reason.
run_case "a comment naming the selector does not count as markup" 1 \
    '  /* .app-container is gone */
  .app-container { height: auto; }' \
    '<div className="sidebar" />' \
    '<div class="sidebar"></div>'

# BOUNDING. Element selectors are not classes and have no markup attribute to find, so they
# must not be reported as dead. Paired with a real class selector deliberately: a block holding
# ONLY element selectors parses zero classes and correctly trips the anti-vacuity floor below,
# which would make this case pass for the wrong reason.
run_case "an element selector is not reported as dead" 0 \
    '  canvas { max-width: 100%; }
  .sidebar { display: none; }' \
    '<div className="sidebar"><canvas /></div>' \
    '<div class="sidebar"><canvas></canvas></div>'

# BOUNDING. A property value containing a # must not be read as an id selector.
run_case "a colour value is not mistaken for a selector" 0 \
    '  .sidebar { color: #767676; border: 1px solid #ffffff; }' \
    '<div className="sidebar" />' \
    '<div class="sidebar"></div>'

# CONTROL. Nothing to check on either side. Without an anti-vacuity floor this reports
# agreement and the gate passes forever on a tree it cannot parse.
run_case "an empty print block must NOT be reported as agreement" 1 \
    '' \
    '<div className="sidebar" />' \
    '<div class="sidebar"></div>'

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
