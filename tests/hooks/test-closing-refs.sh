#!/usr/bin/env bash
# test-closing-refs.sh — tests for scripts/check-closing-refs.sh (#1540)
#
# The check exists because a paragraph did not work: the rule was written, read, and tripped
# three times in one hour -- once while actively writing the warning about it. So the interesting
# cases are the phrasings people actually reach for when they mean "this does NOT finish that
# issue", and the ones that must stay legal so the check does not become something to route
# around.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/check-closing-refs.sh"

[ -x "$TARGET" ] || { echo "FATAL: $TARGET missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# rejects <description> <text>
rejects() {
    if printf '%s' "$2" | bash "$TARGET" >/dev/null 2>&1; then
        fail "$1 -- was allowed, and would close the issue on merge"
    else
        pass "$1"
    fi
}

# allows <description> <text>
allows() {
    if printf '%s' "$2" | bash "$TARGET" >/dev/null 2>&1; then
        pass "$1"
    else
        fail "$1 -- was rejected, which would make the check something to route around"
    fi
}

echo "Testing scripts/check-closing-refs.sh"
echo ""

# The exact sentence that closed #1521 with two of three parts outstanding.
rejects "the real one: 'Does not close #1521.'" "Does not close #1521."
rejects "contraction: \"Doesn't fix #12\"" "Doesn't fix #12"
rejects "will not: 'This will not resolve #3'" "This will not resolve #3"
rejects "bare not: 'not closes #9'" "not closes #9"
rejects "colon form: 'does not close: #44'" "does not close: #44"
rejects "mid-body, among other prose" "## Summary

Some context here.

Does not close #1521. See the parent for the other parts.

More prose."

# Must stay legal, or the check becomes an obstacle rather than a guard.
allows "a normal closing reference" "Closes #123"
allows "the recommended alternative" "Part 2 of #1521, after #1533."
allows "follow-up phrasing" "Follow-up to #1521. Closes #1535."
allows "negation with no issue number" "This does not close the issue; see the parent."
allows "keyword and number far apart" "This will not be closed until QA signs off. Closes #7."
allows "an ordinary body with no negation at all" "Closes #1540.

Adds a check. Fixes the gap #1533 fell into."

# The second shape (#1543): a placeholder bridges the keyword to a real number. GitHub skips a
# #token that is not a valid reference and matches the next one that is -- and every squashed
# commit here gains a (#PR) trailer, so a title always has a real number at the end.
rejects "the real one: a placeholder bridging to the trailer" \
    'docs(agents): record that "Does not close #N" closes #N (#1538) (#1539)'
rejects "angle-bracket placeholder" "Closes #<N> (#1234)"
rejects "word placeholder" "fixes #issue (#99)"

# ...and the legal forms it must not touch, which is where this could easily go wrong: the
# adjacent token being a real number is the whole distinction.
allows "an ordinary closing reference with a PR trailer" "ci: do a thing (#1543)

Closes #1540."
allows "a placeholder with no real number anywhere" "Write it as: Closes #<N>"
allows "two real references on one line" "Closes #12 and closes #13"

# A file argument, since CI passes one rather than piping.
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT INT TERM
printf 'Does not close #99\n' > "$TMP"
if bash "$TARGET" "$TMP" >/dev/null 2>&1; then
    fail "reading from a file -- was allowed"
else
    pass "reading from a file argument works"
fi

# A missing file must be an error, not a silent pass: a check that quietly succeeds when it
# cannot read its input is worse than no check.
if bash "$TARGET" "$TMP.nope" >/dev/null 2>&1; then
    fail "a missing file passed silently"
else
    pass "a missing file is an error rather than a silent pass"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
