#!/usr/bin/env bash
# test-gate-fires.sh — a deliberately broken input must make each gate exit non-zero (#1779)
#
# The sibling property to tests/hooks/test-gate-anti-vacuity.sh. That file asserts a gate
# refuses to report success having examined nothing; this one asserts it reports failure when
# there IS something wrong. A gate can hold either property without the other, and a gate
# holding neither is decoration that reads as coverage.
#
# Four gates in scripts/ had no test of any kind before this file:
#
#   check-theme-contrast.cjs     70 contrast checks across 4 themes, nothing proving one fails
#   check-i18n-keys.cjs          790 keys, nothing proving an undefined one is caught
#   check-commit-attribution.sh  the guard against a permanently unmergeable PR (#1384)
#   check_docs_review.py         only ever exercised incidentally, by test-nested-worktree-scope.sh
#
# check-required-contexts.sh had one (test-required-contexts-mirror.sh) covering check 5 only,
# so checks 1-4 -- including the one that catches a required context nobody emits -- were
# unproven.
#
# THE MESSAGE IS THE ASSERTION, not the exit code (github-workflow §5c). Every case below
# greps for text only the intended defect produces. "Exited non-zero" is shared by a syntax
# error, a missing binary, a bad path and a real finding, and this suite invokes real scripts
# against real fixtures, so all four are reachable. Where a case cannot reach its subject it
# reports HARNESS: rather than FAIL, so a broken fixture is never read as a working gate.
#
# Each case also carries its NARROWNESS CONTROL: an input the gate must still accept. A gate
# that refused everything would satisfy every assertion above while being useless.
#
# The fixtures are real: a genuine `git worktree add` for the gates that need a whole
# repository, and a genuine `git init` for the ones that read git config. Not a mock directory
# -- the thing under test is how these scripts behave against git's own on-disk shape.
#
# bash 3.2 compatible (macOS /bin/bash) -- no associative arrays, no mapfile. See AGENTS.md.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT" || exit 1

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
harness() { echo "  HARNESS: $1"; FAIL=$((FAIL + 1)); }

SANDBOX=$(mktemp -d)
WT_DIR=".lft-gate-fires-test-$$"
WT_PATH="$REPO_ROOT/$WT_DIR"

cleanup() {
    rm -rf "$SANDBOX"
    if [ -e "$WT_PATH" ]; then
        git worktree remove --force "$WT_PATH" >/dev/null 2>&1 || rm -rf "$WT_PATH"
    fi
    git worktree prune >/dev/null 2>&1
}
trap cleanup EXIT INT TERM

echo "Testing that every gate fires on a broken input..."

# ---------------------------------------------------------------------------
# Fixture A: a detached worktree, for the gates that need a whole repository.
#
# The gate SOURCES are copied in from the working tree afterwards. A worktree is checked out
# at HEAD, so without this the subject would be the last committed version of each script
# rather than the one being changed -- and a fix would appear to work before it was written.
# The corpus is the fixture; the script is production.
# ---------------------------------------------------------------------------
if ! git worktree add --detach "$WT_PATH" HEAD >/dev/null 2>&1; then
    harness "could not create the fixture worktree at $WT_DIR -- the corpus gates below were not tested"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi
cp "$REPO_ROOT"/scripts/*.cjs "$REPO_ROOT"/scripts/*.mjs "$WT_PATH/scripts/" 2>/dev/null

# ---------------------------------------------------------------------------
# 1. check-theme-contrast.cjs — a fill a white label cannot sit on.
#
# --danger-strong is .btn-danger's gradient start. Lightening it is the exact defect #1458
# shipped and #1514 repeated: the button renders, nothing errors, and the label is unreadable.
# ---------------------------------------------------------------------------
DARK="$WT_PATH/pkg/server/static/themes/dark.css"
if [ ! -f "$DARK" ]; then
    harness "no dark.css in the fixture worktree -- case 1 proves nothing"
else
    BASE_OUT=$(cd "$WT_PATH" && node scripts/check-theme-contrast.cjs 2>&1)
    if [ $? -ne 0 ]; then
        harness "check-theme-contrast.cjs already fails on the unmutated fixture, so a failure
        after mutating it would prove nothing: $(echo "$BASE_OUT" | head -2 | tr '\n' ' ')"
    else
        # Narrowness control, and it is the same run: an untouched palette must pass. Stated
        # explicitly because it passes both before and after any change to the gate -- it
        # bounds the assertion below rather than testing it.
        pass "check-theme-contrast.cjs passes an untouched palette (narrowness control)"

        sed 's/--danger-strong:[^;]*;/--danger-strong: #ffdddd;/' "$DARK" >"$DARK.mutated" &&
            mv "$DARK.mutated" "$DARK"
        if ! grep -q -- '--danger-strong: #ffdddd;' "$DARK"; then
            harness "the --danger-strong mutation did not apply to dark.css -- case 1 proves nothing"
        else
            OUT=$(cd "$WT_PATH" && node scripts/check-theme-contrast.cjs 2>&1)
            RC=$?
            if [ "$RC" -eq 0 ]; then
                fail "check-theme-contrast.cjs PASSED a near-white --danger-strong. A white label on
        it measures 1.26:1 against a 4.5:1 bar, which is the #1458 defect exactly."
            elif echo "$OUT" | grep -q 'white label on --danger-strong is'; then
                pass "check-theme-contrast.cjs fires on an illegible fill, naming the token and the ratio"
            else
                fail "check-theme-contrast.cjs exited $RC but did not name --danger-strong, so something
        other than the contrast measurement failed: $(echo "$OUT" | head -2 | tr '\n' ' ')"
            fi
        fi
    fi
fi

# ---------------------------------------------------------------------------
# 2. check-i18n-keys.cjs — a key used in markup with no entry in Language.properties.
#
# The defect is invisible in English: every call site carries an inline fallback, which is how
# 477 keys drifted out of the bundle before anything noticed (#1701).
# ---------------------------------------------------------------------------
SETUP="$WT_PATH/pkg/server/static/setup.html"
if [ ! -f "$SETUP" ]; then
    harness "no setup.html in the fixture worktree -- case 2 proves nothing"
else
    BASE_OUT=$(cd "$WT_PATH" && node scripts/check-i18n-keys.cjs 2>&1)
    if [ $? -ne 0 ]; then
        harness "check-i18n-keys.cjs already fails on the unmutated fixture: $(echo "$BASE_OUT" | head -2 | tr '\n' ' ')"
    else
        pass "check-i18n-keys.cjs passes the real bundle (narrowness control)"

        # A key name that cannot collide with a real one, and that Language.properties has no
        # entry for in any locale.
        sed 's|<body|<body data-i18n="lft_bogus_key_1779"|' "$SETUP" >"$SETUP.mutated" &&
            mv "$SETUP.mutated" "$SETUP"
        if ! grep -q 'lft_bogus_key_1779' "$SETUP"; then
            harness "the bogus-key mutation did not apply to setup.html -- case 2 proves nothing"
        else
            OUT=$(cd "$WT_PATH" && node scripts/check-i18n-keys.cjs 2>&1)
            RC=$?
            if [ "$RC" -eq 0 ]; then
                fail "check-i18n-keys.cjs PASSED a data-i18n key with no entry in Language.properties."
            elif echo "$OUT" | grep -q 'lft_bogus_key_1779'; then
                pass "check-i18n-keys.cjs fires on an undefined key, naming the key and its call site"
            else
                fail "check-i18n-keys.cjs exited $RC without naming lft_bogus_key_1779, so it failed for
        some other reason: $(echo "$OUT" | head -3 | tr '\n' ' ')"
            fi
        fi
    fi
fi

# ---------------------------------------------------------------------------
# 3. check-commit-attribution.sh — an address GitHub cannot map to an account.
#
# Fixture B: a real `git init`, with HOME and GIT_CONFIG_GLOBAL redirected. Without that the
# developer's own ~/.gitconfig supplies user.email and the "no email set" case cannot be
# reached at all -- verified: unsetting it locally simply fell through to the global value,
# and the case would have passed by reading a real address rather than no address (#1798 is
# the same lesson for the Go suite).
# ---------------------------------------------------------------------------
ATTR="$REPO_ROOT/scripts/check-commit-attribution.sh"
mkdir -p "$SANDBOX/home" "$SANDBOX/repo"
git init --quiet "$SANDBOX/repo" >/dev/null 2>&1
: >"$SANDBOX/gitconfig-empty"

run_attr() { # run_attr <email|-->
    cd "$SANDBOX/repo" || return 1
    if [ "$1" != "--" ]; then
        git config user.email "$1"
    else
        git config --unset user.email >/dev/null 2>&1
    fi
    OUT=$(HOME="$SANDBOX/home" GIT_CONFIG_GLOBAL="$SANDBOX/gitconfig-empty" \
        GIT_CONFIG_NOSYSTEM=1 bash "$ATTR" 2>&1)
    RC=$?
    cd "$REPO_ROOT" || return 1
}

if [ ! -f "$ATTR" ]; then
    harness "check-commit-attribution.sh not found -- case 3 proves nothing"
else
    run_attr 'nobody@example.invalid'
    if [ "$RC" -eq 0 ]; then
        fail "check-commit-attribution.sh ACCEPTED 'nobody@example.invalid'. An unattributed head
        commit needs an approving review master cannot supply, so the PR is unmergeable with
        every check green (#1384)."
    elif echo "$OUT" | grep -q "git user.email is 'nobody@example.invalid'"; then
        pass "check-commit-attribution.sh refuses a non-attributing address, naming it"
    else
        fail "check-commit-attribution.sh exited $RC without naming the address, so it failed for
        another reason: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    # The case the developer's own gitconfig hid.
    run_attr '--'
    if [ "$RC" -eq 0 ]; then
        fail "check-commit-attribution.sh ACCEPTED a commit with no user.email set at all"
    elif echo "$OUT" | grep -q 'No git user.email is set'; then
        pass "check-commit-attribution.sh refuses an unset user.email with its own message"
    else
        fail "check-commit-attribution.sh exited $RC on an unset email but said something else,
        so the empty-email branch was not the one that ran: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    # Narrowness controls. A hook that refused every address would satisfy both cases above
    # and make the repository uncommittable.
    run_attr '123456+someone@users.noreply.github.com'
    if [ "$RC" -eq 0 ]; then
        pass "check-commit-attribution.sh accepts a users.noreply.github.com address (narrowness control)"
    else
        fail "check-commit-attribution.sh REFUSED a users.noreply.github.com address, which GitHub
        issues itself and always attributes: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    cd "$SANDBOX/repo" && git config lft.attributableEmails 'allowed@example.invalid' && cd "$REPO_ROOT" || true
    run_attr 'allowed@example.invalid'
    if [ "$RC" -eq 0 ]; then
        pass "check-commit-attribution.sh honours the lft.attributableEmails allowlist (narrowness control)"
    else
        fail "check-commit-attribution.sh refused an explicitly allowlisted address, so the escape
        hatch is inert: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi
fi

# ---------------------------------------------------------------------------
# 4. check_docs_review.py — a markdown file with no review footer.
# ---------------------------------------------------------------------------
DOCS="$REPO_ROOT/scripts/check_docs_review.py"
printf '# no footer here\n\nNothing at the bottom.\n' >"$SANDBOX/nofooter.md"
printf '# fine\n\n---\n*Last Updated: %s* | *Last Reviewed: %s*\n' \
    "$(date +%Y-%m-%d)" "$(date +%Y-%m-%d)" >"$SANDBOX/withfooter.md"

OUT=$(python3 "$DOCS" --changed-files "$SANDBOX/nofooter.md" 2>&1)
RC=$?
if [ "$RC" -eq 0 ]; then
    fail "check_docs_review.py PASSED a markdown file with no review footer"
elif echo "$OUT" | grep -q 'MISSING-FOOTER'; then
    pass "check_docs_review.py fires on a footerless markdown file"
else
    fail "check_docs_review.py exited $RC but did not report MISSING-FOOTER, so a different
        branch ran: $(echo "$OUT" | head -2 | tr '\n' ' ')"
fi

OUT=$(python3 "$DOCS" --changed-files "$SANDBOX/withfooter.md" 2>&1)
if [ $? -eq 0 ]; then
    pass "check_docs_review.py accepts a file with a current footer (narrowness control)"
else
    fail "check_docs_review.py refused a file whose footer is today's date: $(echo "$OUT" | head -2 | tr '\n' ' ')"
fi

# ---------------------------------------------------------------------------
# 5. check-required-contexts.sh, check 1 — a required context no job emits.
#
# The failure mode is silent and permanent: the context never gets a check run, so it stays
# pending and the PR can never merge (#1380/#1386). The gate reads its workflow paths relative
# to the working directory, so a sandbox containing empty workflow files reproduces it without
# touching the real .github/.
# ---------------------------------------------------------------------------
CTX="$REPO_ROOT/scripts/check-required-contexts.sh"
mkdir -p "$SANDBOX/ci/.github/workflows"
: >"$SANDBOX/ci/.github/workflows/ci.yml"
: >"$SANDBOX/ci/.github/workflows/e2e-sso.yml"
: >"$SANDBOX/ci/.github/workflows/issue-link-check.yml"

OUT=$(cd "$SANDBOX/ci" && bash "$CTX" --offline 2>&1)
RC=$?
cd "$REPO_ROOT" || exit 1
if [ "$RC" -eq 0 ]; then
    fail "check-required-contexts.sh PASSED against workflow files that emit no jobs at all.
        Every required context would stay pending forever."
elif echo "$OUT" | grep -q "required context 'CI Gate' is not produced by any job"; then
    pass "check-required-contexts.sh fires when a required context has no job emitting it"
else
    fail "check-required-contexts.sh exited $RC without reporting an unemitted context, so
        check 1 was not what failed: $(echo "$OUT" | head -3 | tr '\n' ' ')"
fi

OUT=$(bash "$CTX" --offline 2>&1)
if [ $? -eq 0 ]; then
    pass "check-required-contexts.sh passes against the real workflows (narrowness control)"
else
    fail "check-required-contexts.sh fails on a clean checkout: $(echo "$OUT" | head -3 | tr '\n' ' ')"
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
