#!/usr/bin/env bash
# test-required-contexts-mirror.sh — tests check 5 of scripts/check-required-contexts.sh (#1729)
#
# Check 5 compares REQUIRED_JOBS against what master actually enforces. It exists because the
# four checks before it all read FROM that list, so a line missing from it does not fail
# anything -- it removes a required context from every check above while the script still
# prints OK. That is what #1729 was: "CI Gate" was required by both the ruleset and classic
# branch protection, absent from the mirror, and checks 1-3 had quietly never covered the most
# load-bearing context on the repo.
#
# The failure being guarded against is therefore "the guard compared nothing and said OK", so
# the cases below are mostly about a comparison that must NOT be mistaken for a passing one:
# an unreadable API, an empty response, a missing gh. Each must be visibly unverified rather
# than quietly green.
#
# `gh` is stubbed. That is deliberate -- this test must run offline, in CI, with no admin
# token -- and it is worth being explicit about what that does NOT cover: the stub ignores the
# --jq filters, so it proves the comparison logic, not that the two API paths and their jq
# still return what we think. Those were verified against the live repo when this was written
# and are re-verified on every run of `make check-contexts-live`.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/check-required-contexts.sh"

[ -f "$TARGET" ] || { echo "FATAL: $TARGET missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ------------------------------------------------------------------------------------------
# Fixture: a copy of the repo's workflows plus the script, so checks 1-4 pass and only the
# mirror varies between cases. Without the real workflows an earlier check would fail first
# and every case would "fail" for the wrong reason -- passing the test while proving nothing.
# ------------------------------------------------------------------------------------------
TREE="$WORK/tree"
mkdir -p "$TREE/scripts" "$TREE/.github/workflows"
cp "$TARGET" "$TREE/scripts/check-required-contexts.sh"
for wf in ci.yml e2e-sso.yml issue-link-check.yml; do
    cp "$REPO_ROOT/.github/workflows/$wf" "$TREE/.github/workflows/$wf"
done

# A stub gh, serving canned lists from files the cases write. A missing file is a 403, which
# is what CI's contents:read token actually gets from the protection endpoint.
STUB="$WORK/bin"
mkdir -p "$STUB"
cat > "$STUB/gh" <<'STUBEOF'
#!/usr/bin/env bash
[ "${1:-}" = "api" ] || { echo "stub gh: unexpected command $*" >&2; exit 1; }
case "$2" in
    */branches/master/protection) src="$LFT_STUB_DIR/classic" ;;
    */rules/branches/master)      src="$LFT_STUB_DIR/ruleset" ;;
    *) echo "stub gh: unexpected path $2" >&2; exit 1 ;;
esac
[ -f "$src" ] || { echo "HTTP 403: Resource not accessible by integration" >&2; exit 1; }
cat "$src"
STUBEOF
chmod +x "$STUB/gh"

export LFT_STUB_DIR="$WORK/canned"
mkdir -p "$LFT_STUB_DIR"

# The lists master really carried when this was written. Used as the "agreeing" baseline, with
# Test Suite matrix-expanded exactly as GitHub reports it.
CLASSIC_REAL='Test Suite (ubuntu-latest)
Test Suite (macos-latest)
Test Suite (windows-latest)
Lint & Format Check
Documentation Review Check
E2E Docker Integration Test
E2E Playwright UI Test
E2E Keycloak SSO Integration Test
Verify PR references an issue
CI Gate'
RULESET_REAL='Lint & Format Check
Test Suite (ubuntu-latest)
Test Suite (macos-latest)
Test Suite (windows-latest)
Verify PR references an issue
CI Gate'

# set_live <classic-or-"none"> <ruleset-or-"none">
set_live() {
    rm -f "$LFT_STUB_DIR/classic" "$LFT_STUB_DIR/ruleset"
    [ "$1" = "none" ] || printf '%s\n' "$1" > "$LFT_STUB_DIR/classic"
    [ "$2" = "none" ] || printf '%s\n' "$2" > "$LFT_STUB_DIR/ruleset"
}

# drop_from_mirror <job name> — recreate the fixture script with that entry removed.
drop_from_mirror() {
    grep -v "^$1|" "$TARGET" > "$TREE/scripts/check-required-contexts.sh"
}
add_to_mirror() {
    sed "s#^CI Gate|.*#&\\n$1|.github/workflows/ci.yml#" "$TARGET" \
        > "$TREE/scripts/check-required-contexts.sh"
}
reset_mirror() { cp "$TARGET" "$TREE/scripts/check-required-contexts.sh"; }

# run_case <description> <expected exit> <expected stderr+stdout substring> [args...]
run_case() {
    local desc="$1" want="$2" needle="$3"; shift 3
    local out rc
    out=$(cd "$TREE" && PATH="$STUB:$PATH" bash ./scripts/check-required-contexts.sh "$@" 2>&1)
    rc=$?
    if [ "$rc" -ne "$want" ]; then
        fail "$desc -- exited $rc, expected $want"
        printf '%s\n' "$out" | sed 's/^/        /'
        return
    fi
    if ! printf '%s' "$out" | grep -qF "$needle"; then
        fail "$desc -- exit $rc was right but output never mentioned '$needle'"
        printf '%s\n' "$out" | sed 's/^/        /'
        return
    fi
    pass "$desc"
}

echo "Testing scripts/check-required-contexts.sh check 5 (mirror vs live enforcement)"
echo ""

# ------------------------------------------------------------------------------------------
# The regression itself
# ------------------------------------------------------------------------------------------
if grep -qxF 'CI Gate|.github/workflows/ci.yml' "$TARGET"; then
    pass "REQUIRED_JOBS lists 'CI Gate' (#1729)"
else
    fail "REQUIRED_JOBS does not list 'CI Gate' -- it is required by BOTH master's ruleset and
        its classic branch protection, and while it is absent every other check silently
        skips it"
fi

set_live "$CLASSIC_REAL" "$RULESET_REAL"
reset_mirror
run_case "agreeing lists verify, and say so" 0 "VERIFIED: REQUIRED_JOBS matches live enforcement"

drop_from_mirror "CI Gate"
run_case "the #1729 state fails and names the context" 1 \
    "'CI Gate' is required on master but is missing from REQUIRED_JOBS"

# Same defect, seen only in the ruleset -- neither list may be read alone (#1380).
set_live "$(printf '%s\n' "$CLASSIC_REAL" | grep -vxF 'CI Gate')" "$RULESET_REAL"
run_case "a context required by the ruleset alone still fails" 1 \
    "'CI Gate' is required on master but is missing from REQUIRED_JOBS"

# ...and the mirror image: classic protection alone, which is the list #1380 was blocked by.
set_live "$CLASSIC_REAL" "$(printf '%s\n' "$RULESET_REAL" | grep -vxF 'CI Gate')"
run_case "a context required by classic protection alone still fails" 1 \
    "'CI Gate' is required on master but is missing from REQUIRED_JOBS"

# ------------------------------------------------------------------------------------------
# Over-claiming: a mirror entry nothing enforces means checks 1-4 are guarding a context that
# does not gate anything, which misreads just as badly as an omission.
# ------------------------------------------------------------------------------------------
set_live "$CLASSIC_REAL" "$RULESET_REAL"
add_to_mirror "Shell Lint"
run_case "a mirror entry nothing enforces fails" 1 \
    "'Shell Lint' is in REQUIRED_JOBS but is required by neither"

# ------------------------------------------------------------------------------------------
# Matrix expansion: the live lists name "Test Suite (ubuntu-latest)" while the mirror names the
# job. If the fold broke, every matrix context would report as both missing and extra.
# ------------------------------------------------------------------------------------------
reset_mirror
run_case "matrix contexts fold to the job name" 0 "VERIFIED"

# ------------------------------------------------------------------------------------------
# Unreadable / empty: the cases where a guard is most tempted to compare nothing and pass.
# ------------------------------------------------------------------------------------------
set_live none none
run_case "a 403 is reported, not silently passed" 0 "NOT VERIFIED"
run_case "a 403 never claims verification" 0 "make check-contexts-live"
run_case "a 403 is fatal under --verify-mirror" 1 \
    "could not verify REQUIRED_JOBS against live enforcement" --verify-mirror

set_live "" ""
run_case "empty lists are treated as unread, not as agreement" 0 \
    "both enforcement lists came back empty"
run_case "empty lists are fatal under --verify-mirror" 1 \
    "both enforcement lists came back empty" --verify-mirror

# Empty lists plus a wrong mirror must NOT report VERIFIED -- this is the "compared nothing and
# called it coverage" failure in its purest form.
drop_from_mirror "CI Gate"
out=$(cd "$TREE" && PATH="$STUB:$PATH" bash ./scripts/check-required-contexts.sh 2>&1)
if printf '%s' "$out" | grep -qF "VERIFIED: REQUIRED_JOBS matches"; then
    fail "empty lists reported a wrong mirror as VERIFIED"
else
    pass "empty lists never report a wrong mirror as verified"
fi
reset_mirror

# gh absent altogether: the offline pre-push run this script was written for. A PATH built
# from symlinks to just the tools the script uses, rather than an emptied one -- emptying PATH
# loses bash itself, and rather than a real "gh is missing" run you get exit 127, which the
# case would have accepted as a failure signal from the wrong thing entirely.
NOGH="$WORK/nogh"
mkdir -p "$NOGH"
for tool in bash sh awk sed grep sort tr cut rm; do
    tool_path=$(command -v "$tool" 2>/dev/null) && ln -sf "$tool_path" "$NOGH/$tool"
done
out=$(cd "$TREE" && PATH="$NOGH" "$NOGH/bash" ./scripts/check-required-contexts.sh 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -qF "NOT VERIFIED"; then
    pass "no gh on PATH: still passes offline, still says it did not verify"
else
    fail "no gh on PATH: exited $rc, expected 0 with NOT VERIFIED"
    printf '%s\n' "$out" | sed 's/^/        /'
fi

# ------------------------------------------------------------------------------------------
# Flags
# ------------------------------------------------------------------------------------------
set_live "$CLASSIC_REAL" "$RULESET_REAL"
run_case "--offline skips the comparison and says so" 0 "NOT VERIFIED" --offline
run_case "an unknown flag is rejected rather than ignored" 2 "usage:" --wat

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
