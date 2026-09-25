#!/usr/bin/env bash
# test-ci-path-filter.sh -- the CI path filter must light up the areas a change actually touches
# (#2250).
#
# `Detect Changed Paths` decides whether the Go suite, the UI E2E job and the hook tests run. It is
# a gate ON the gates, and it was wrong in both directions for as long as Portal V1 has existed:
#
#   - V1 (pkg/server/static/, pkg/server/dashboard.html) matched NEITHER `go` nor `ui`, so a
#     V1-only change skipped the 14-minute E2E job entirely -- including the V1 specs written for
#     exactly that code. Masked because a V1 change usually drags a scripts/ or tests/ file along,
#     so the job ran by accident.
#   - ^scripts/ sat in `go`, so editing a Node linter ran the Go suite on three operating systems
#     and the E2E job too.
#
# Neither was visible from the workflow: a job that does not run reports nothing, and a job that
# runs for the wrong reason looks identical to one that runs for the right one.
#
# The regexes are READ OUT OF ci.yml rather than copied here. A copy would agree with itself
# however far the workflow drifted -- the mistake §5c calls testing a mirror.
#
# bash 3.2 clean: the Makefile invokes this via test-hooks.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CI="${REPO_ROOT}/.github/workflows/ci.yml"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# Pull a filter's regex straight out of the workflow: the line assigning that area from a grep -qE.
regex_for() {
    sed -n "/^ *if echo \"\$CHANGED\" | grep -qE '.*then\$/p" "$CI" \
        | sed -n "${1}p" | sed "s/.*grep -qE '//; s/'; then.*//"
}

GO_RE="$(regex_for 1)"
UI_RE="$(regex_for 2)"

# Anti-vacuity: an empty or degenerate regex matches nothing (or everything), and every case below
# would then "pass" while checking nothing at all.
if [ ${#GO_RE} -lt 20 ] || [ ${#UI_RE} -lt 20 ]; then
    fail "the regexes could not be read out of ci.yml (go='$GO_RE' ui='$UI_RE') -- the derivation
        is broken, so a green result here would mean nothing was checked"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# $1 label, $2 area (go|ui), $3 expected (yes|no), $4.. the changed files
expect() {
    _label="$1"; _area="$2"; _want="$3"; shift 3
    case "$_area" in
        go) _re="$GO_RE" ;;
        ui) _re="$UI_RE" ;;
    esac
    _got=no
    for _f in "$@"; do
        if printf '%s\n' "$_f" | grep -qE "$_re"; then _got=yes; fi
    done
    if [ "$_got" = "$_want" ]; then
        pass "$_label"
    else
        fail "$_label -- $_area was '$_got', want '$_want' (files: $*)"
    fi
}

echo "-- FIRING: a V1-only change must run the UI E2E job"
# Both were 'no' before #2250, so the V1 specs never ran on a V1-only change.
expect "dashboard.js alone sets ui" ui yes pkg/server/static/dashboard.js
expect "dashboard.html alone sets ui" ui yes pkg/server/dashboard.html

echo ""
echo "-- FIRING: a checker script must NOT run the Go suite"
# ^scripts/ used to be in the go filter; it belongs to hooks.
expect "a Node checker does not set go" go no scripts/check-i18n-keys.cjs

echo ""
echo "-- the areas that must keep working"
expect "V2 sources set ui" ui yes ui/src/components/ReservationsPanel.tsx
expect "Go sources set go" go yes pkg/server/proxy.go
expect "the e2e harness sets go" go yes tests/e2e/ui/tests/dashboard.spec.ts
expect "the Makefile sets go" go yes Makefile

echo ""
echo "-- BOUNDING: what must NOT light these up"
# A translations-only change was already correctly skipping the Go suite; it now sets ui, because
# applyTranslations() is a code path a spec drives.
expect "a translations-only change does not set go" go no pkg/server/i18n/Language_de.properties
expect "a docs-only change sets neither (go)" go no docs/custom_domains.md
expect "a docs-only change sets neither (ui)" ui no docs/custom_domains.md

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
