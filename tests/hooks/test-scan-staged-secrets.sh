#!/bin/bash
#
# Tests scripts/scan-staged-secrets.sh (#1377).
#
# Two layers, deliberately:
#
#   STUBBED  - a fake `docker` on PATH replays real captured gitleaks output, inside a throwaway
#              git repo so the staged set is controlled rather than whatever the developer happens
#              to have staged. Covers the fail-closed branches, which are the part that silently
#              passed before. No Docker, no network, milliseconds.
#
#   REAL     - linked worktrees with real staged content and real Docker. This is the case the bug
#              was found in, and a stub cannot prove the bind mount is right. Skipped only when
#              Docker is genuinely unavailable, and even then it fails unless the skip was asked
#              for -- a suite that quietly drops its only real case is this issue's own defect.
#
# Assertions are on EXIT STATUS, not log text, so a gitleaks output-format change cannot turn a
# regression into a pass. Secrets are generated at run time rather than committed, so the fixture
# cannot itself trip the scanner or need a .gitleaksignore entry.
#
# One deliberate exception to "exit status only", added for #1923: a third layer asserts WHICH
# REASON the scanner reports. Exit status alone cannot cover it, because the defect was two
# different states sharing one exit code AND one sentence -- with colima stopped, every commit was
# refused with "a secret or private token was detected" when gitleaks had never run and there was
# nothing to find. The suite already asserted fail-closed, which is why that reached a person:
# every assertion in it was satisfied by the wrong behaviour. Those cases are labelled PREMISE /
# FIRING / BOUNDING / CONTROL so it is clear which of them is evidence of the fix.
#
# Each real case gets its OWN worktree. Reusing one and unstaging between cases makes a failure
# ambiguous between the scanner and the test's own cleanup, which cost time when writing this.

set -uo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
SCANNER="$REPO_ROOT/scripts/scan-staged-secrets.sh"
GITLEAKS_IMAGE="${GITLEAKS_IMAGE:-zricethezav/gitleaks:v8.30.1}"

PASS=0
FAIL=0
SKIP=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; SKIP=$((SKIP + 1)); }

# A synthetic token matching gitleaks' `github-pat` rule. Generated, never stored.
make_token() {
    printf 'ghp_%s' "$(LC_ALL=C tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 36)"
}

STUB_DIR=$(mktemp -d)
STUB_REPO=$(mktemp -d)
WORKTREES=()

cleanup() {
    rm -rf "$STUB_DIR" "$STUB_REPO"
    for wt in "${WORKTREES[@]:-}"; do
        [ -n "$wt" ] || continue
        git -C "$REPO_ROOT" worktree remove --force "$wt" >/dev/null 2>&1
        rm -rf "$wt"
    done
    git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1
}
trap cleanup EXIT

# ---------------------------------------------------------------------------------------------
# Stubbed cases
# ---------------------------------------------------------------------------------------------

# A throwaway repo with exactly one staged file, so the 0-bytes guard's "are files staged?"
# condition is a known quantity instead of ambient state.
git -C "$STUB_REPO" init -q
git -C "$STUB_REPO" config user.email test@example.com
git -C "$STUB_REPO" config user.name Test
printf 'placeholder\n' > "$STUB_REPO/staged.txt"
git -C "$STUB_REPO" add staged.txt

# $1 human name, $2 expected exit status, $3 stub exit status, $4 stub stdout.
run_stub_case() {
    local name="$1" want="$2" stub_rc="$3" stub_out="$4" got

    cat > "$STUB_DIR/docker" <<STUB
#!/bin/bash
cat <<'PAYLOAD'
$stub_out
PAYLOAD
exit $stub_rc
STUB
    chmod +x "$STUB_DIR/docker"

    ( cd "$STUB_REPO" && PATH="$STUB_DIR:$PATH" "$SCANNER" >/dev/null 2>&1 )
    got=$?

    if [ "$got" -eq "$want" ]; then
        pass "$name (exit $got)"
    else
        fail "$name (expected exit $want, got $got)"
    fi
}

echo "Stubbed cases (no Docker required):"

# The exact shape of the bug: git failed inside the container, nothing was read, gitleaks
# still exited 0. Captured verbatim from a real worktree run before the fix.
run_stub_case "git unreadable + 0 bytes + exit 0 must FAIL closed" 1 0 \
'ERR [git] fatal: not a git repository: /repo/.git/worktrees/wt-leak
INF 0 commits scanned.
INF scanned ~0 bytes (0) in 56ms
INF no leaks found'

# The same silent pass without the word "fatal" -- proves the 0-bytes guard stands on its own
# rather than relying on error text, which is what makes it hold for an unforeseen cause.
run_stub_case "0 bytes with staged files and no error text must FAIL closed" 1 0 \
'INF 0 commits scanned.
INF scanned ~0 bytes (0) in 51ms
INF no leaks found'

# A real clean scan must still pass, or the guards would block every commit.
run_stub_case "genuine clean scan must PASS" 0 0 \
'INF 1 commits scanned.
INF scanned ~1284 bytes (1.28 kB) in 61ms
INF no leaks found'

# A byte count that merely starts with 0 must not be mistaken for zero.
run_stub_case "scanned ~1024 bytes must not match the 0-byte guard" 0 0 \
'INF scanned ~1024 bytes (1.02 kB) in 40ms
INF no leaks found'

# gitleaks' own non-zero exit still has to block, unchanged from before the fix.
run_stub_case "leak found (exit 1) must FAIL" 1 1 \
'Finding:     token = ghp_REDACTED
RuleID:      github-pat
INF scanned ~54 bytes (54 bytes) in 43ms
WRN leaks found: 1'

# ---------------------------------------------------------------------------------------------
# Failure-reason cases: WHICH reason is reported, not just that it fails closed (#1923)
# ---------------------------------------------------------------------------------------------
#
# All of these still exit 1. Failing closed is not what is under test here and must not change;
# what is under test is that a person can tell "gitleaks found something" from "gitleaks never
# ran", which they could not, because both printed the same sentence.
#
# The payloads are captured verbatim from this machine on 2026-09-16, and the exit codes are the
# measured ones. They are the reason the classification cannot key on exit status:
#
#   daemon unreachable -> exit 1     <- identical to gitleaks' "leaks found" status
#   image unpullable   -> exit 125
#   genuine finding    -> exit 1

# $1 name, $2 expected exit, $3 regex that MUST appear, $4 regex that must NOT appear ("-" for
# none), $5 stub exit status, $6 stub stdout, $7 scanner under test (defaults to the real one).
run_reason_case() {
    local name="$1" want="$2" want_re="$3" deny_re="$4" stub_rc="$5" stub_out="$6"
    local scanner="${7:-$SCANNER}"
    local got out problems

    cat > "$STUB_DIR/docker" <<STUB
#!/bin/bash
cat <<'PAYLOAD'
$stub_out
PAYLOAD
exit $stub_rc
STUB
    chmod +x "$STUB_DIR/docker"

    # Declared first, assigned second: `local out=$(...)` would make $? the status of `local`,
    # not of the scanner (shellcheck SC2155, and this repo has been bitten by it).
    out=$( cd "$STUB_REPO" && PATH="$STUB_DIR:$PATH" "$scanner" 2>&1 )
    got=$?

    problems=""
    if [ "$got" -ne "$want" ]; then
        problems="$problems
            expected exit $want, got $got"
    fi
    if ! printf '%s\n' "$out" | grep -qE "$want_re"; then
        problems="$problems
            expected output matching /$want_re/, which is absent"
    fi
    if [ "$deny_re" != "-" ] && printf '%s\n' "$out" | grep -qE "$deny_re"; then
        problems="$problems
            output matches /$deny_re/, which it must not"
    fi

    if [ -z "$problems" ]; then
        pass "$name"
    else
        fail "$name$problems"
    fi
}

# Captured verbatim. The socket path is the one from the report; only the username is changed.
DAEMON_DOWN='failed to connect to the docker API at unix:///Users/dev/.colima/default/docker.sock; check if the path is correct and if the daemon is running: dial unix /Users/dev/.colima/default/docker.sock: connect: no such file or directory'

IMAGE_MISSING="Unable to find image '$GITLEAKS_IMAGE' locally
docker: Error response from daemon: failed to resolve reference \"docker.io/$GITLEAKS_IMAGE\": docker.io/$GITLEAKS_IMAGE: not found

Run 'docker run --help' for more information"

MOUNT_REFUSED='docker: Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /Volumes/SanDisk/repos/lfr-tunnel

Run '"'"'docker run --help'"'"' for more information'

# Real gitleaks v8.30.1 output for a staged ghp_ token, ANSI colour codes stripped and the token
# redacted. Note the exit code is 1 -- the same as the daemon failure above.
LEAK_FOUND='Finding:     const token = "ghp_REDACTED";
Secret:      ghp_REDACTED
RuleID:      github-pat
Entropy:     4.734184
File:        probe.js
Line:        1
Fingerprint: probe.js:github-pat:1

9:38AM INF 0 commits scanned.
9:38AM INF scanned ~58 bytes (58 bytes) in 28.1ms
9:38AM WRN leaks found: 1'

CLEAN_SCAN='9:38AM INF 1 commits scanned.
9:38AM INF scanned ~1284 bytes (1.28 kB) in 61ms
9:38AM INF no leaks found'

FINDING_MSG='secret or private token was detected'
NOT_RUN_MSG='NOTHING was scanned'
IGNORE_ADVICE='\.gitleaksignore'

echo ""
echo "Failure-reason cases (no Docker required):"

# PREMISE -- the harness can produce a pass at all. If the stub were not on PATH, the throwaway
# repo unusable, or the output capture broken, the happy path would fail too and every assertion
# below would be reporting on the harness rather than on the scanner.
run_reason_case "PREMISE: a clean scan passes and says so" \
    0 'No secrets detected' '❌' 0 "$CLEAN_SCAN"

# FIRING -- each of these fails against the pre-#1923 scanner, which printed the finding message
# for all three.
run_reason_case "FIRING: daemon unreachable is reported as 'nothing was scanned', not a finding" \
    1 "$NOT_RUN_MSG" "$FINDING_MSG" 1 "$DAEMON_DOWN"

run_reason_case "FIRING: daemon unreachable names the cause and gives no ignore advice" \
    1 'Cause: the Docker daemon is unreachable' "$IGNORE_ADVICE" 1 "$DAEMON_DOWN"

run_reason_case "FIRING: an unpullable image (exit 125) is reported as 'nothing was scanned'" \
    1 'Cause: .* image could not be pulled' "$IGNORE_ADVICE|$FINDING_MSG" 125 "$IMAGE_MISSING"

run_reason_case "FIRING: a refused bind mount is reported as 'nothing was scanned'" \
    1 'Cause: a bind mount was refused' "$IGNORE_ADVICE|$FINDING_MSG" 125 "$MOUNT_REFUSED"

# BOUNDING -- passes before AND after, on purpose. A genuine finding must keep the message and
# the remedy it has always had; going red here means the fix over-reached and started calling a
# real leak an infrastructure failure, which is the dangerous direction.
run_reason_case "BOUNDING: a genuine finding keeps the 'secret detected' message" \
    1 "$FINDING_MSG" "$NOT_RUN_MSG" 1 "$LEAK_FOUND"

run_reason_case "BOUNDING: a genuine finding still points at .gitleaksignore" \
    1 "$IGNORE_ADVICE" '-' 1 "$LEAK_FOUND"

# CONTROL -- the FIRING cases above assert an ABSENCE, and an absence is satisfied by a scanner
# that prints nothing at all, by a stub that never ran, and by a typo in the regex. So run the
# same payload against a copy of the scanner with the classification removed, and require the
# pre-fix sentence to come back. If these two go red, the four FIRING cases prove nothing.
MUTANT="$STUB_DIR/scanner-no-classification.sh"
sed 's/if scan_reached_gitleaks; then/if true; then/' "$SCANNER" > "$MUTANT"
chmod +x "$MUTANT"

if cmp -s "$SCANNER" "$MUTANT"; then
    fail "CONTROL: the mutation did not apply -- 'if scan_reached_gitleaks; then' is no longer in
        $SCANNER, so the control is inert and the FIRING cases above are unverified. Re-point the
        sed at whatever the classification is spelled as now."
else
    run_reason_case "CONTROL: without the classification, a daemon failure is misreported as a secret" \
        1 "$FINDING_MSG" '-' 1 "$DAEMON_DOWN" "$MUTANT"

    run_reason_case "CONTROL: without the classification, the ignore advice comes back" \
        1 "$IGNORE_ADVICE" '-' 1 "$DAEMON_DOWN" "$MUTANT"
fi

# ---------------------------------------------------------------------------------------------
# Real end-to-end cases: linked worktrees, real Docker
# ---------------------------------------------------------------------------------------------
echo ""
echo "End-to-end cases (real Docker, real worktree):"

# Sibling of the repo, not inside it and not under a system temp dir. The mount has to be a path
# the Docker daemon can actually see: on macOS /private/tmp is NOT shared by default, and a bind
# mount of an unshared path silently presents as an EMPTY directory -- which looks exactly like
# the bug being tested, and did mislead the first attempt at this test. Overridable for hosts that
# share a different root.
WT_BASE="${LFT_HOOK_TEST_DIR:-$(dirname "$REPO_ROOT")}"

# Creates a fresh worktree and verifies Docker can actually see it. Sets NEW_WT rather than
# echoing the path: a `wt=$(new_worktree ...)` command substitution runs in a SUBSHELL, so the
# WORKTREES append made inside it is lost to the parent and the cleanup trap then has nothing to
# remove. That leaked four worktrees per run before it was caught.
NEW_WT=""
new_worktree() {
    local wt="$WT_BASE/.lft-hook-test-$$-$1" mounted
    NEW_WT=""
    git -C "$REPO_ROOT" worktree add --detach -q "$wt" HEAD >/dev/null 2>&1 || return 1
    # Registered for cleanup before the reachability probe, so a worktree that Docker cannot see
    # is still removed rather than left behind by the early return.
    WORKTREES+=("$wt")
    mounted=$(docker run --rm -v "$wt":/probe --entrypoint sh "$GITLEAKS_IMAGE" \
        -c 'ls -a /probe | wc -l' 2>/dev/null)
    [ "${mounted:-0}" -gt 2 ] || return 2
    NEW_WT="$wt"
}

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    if [ "${LFT_HOOK_TEST_ALLOW_SKIP:-0}" = "1" ]; then
        skip "worktree cases -- Docker unavailable, skip explicitly allowed"
    else
        fail "worktree cases -- Docker unavailable. These are the only cases that exercise the
        bind mount, so skipping them silently would defeat the test. Set
        LFT_HOOK_TEST_ALLOW_SKIP=1 to allow the skip deliberately."
    fi
else
    # Case: a staged secret in a worktree must be blocked. This is the regression.
    if new_worktree secret; then
        ( cd "$NEW_WT" && printf 'const token = "%s";\n' "$(make_token)" > probe.js \
            && git add probe.js && "$SCANNER" >/dev/null 2>&1 )
        rc=$?
        if [ "$rc" -ne 0 ]; then
            pass "staged secret in a worktree is blocked (exit $rc)"
        else
            fail "staged secret in a worktree was NOT blocked (exit 0) -- #1377 has regressed"
        fi
    else
        rc=$?
        if [ "$rc" -eq 2 ]; then
            fail "Docker cannot see $WT_BASE (bind mount came through empty).
        Set LFT_HOOK_TEST_DIR to a path the daemon shares."
        else
            fail "could not create a worktree under $WT_BASE"
        fi
    fi

    # Case: a clean file in a worktree must NOT be blocked. This is the case that actually
    # proves the bind mount works, and it is not redundant with the one above -- verified by
    # mutation. With the worktree mount removed but the fail-closed guards left in place:
    #
    #     staged secret in a worktree is blocked   PASS  <- the 0-byte guard caught it
    #     clean file in a worktree is allowed      FAIL  <- only this one noticed
    #
    # So the secret case alone cannot tell "the mount is fixed" from "the mount is broken and
    # the guard compensated". Both cases are load-bearing; do not drop this one as a duplicate.
    # Case: a change consisting only of DELETIONS must not be blocked (#1649).
    #
    # gitleaks protect --staged scans added content, so a deletion-only edit legitimately
    # scans 0 bytes. The guard keyed on "files are staged" instead, and --diff-filter=ACM
    # still lists such an edit as M -- so every deletion-only commit was refused, and the only
    # ways past were --no-verify (which disables the scan entirely, the opposite of the point)
    # or padding the diff with a cosmetic addition.
    #
    # Committed content first, then a commit, then the deletion: staging a deletion requires
    # something to delete, and a file added and removed in the same staging area nets to
    # nothing at all, which is a different case.
    if new_worktree deletiononly; then
        ( cd "$NEW_WT" \
            && printf 'line one\nline two\nline three\n' > todelete.txt \
            && git add todelete.txt \
            && git -c user.email=t@t -c user.name=t commit -qm "fixture" --no-verify \
            && printf 'line one\nline three\n' > todelete.txt \
            && git add todelete.txt \
            && "$SCANNER" >/dev/null 2>&1 )
        rc=$?
        if [ "$rc" -eq 0 ]; then
            pass "deletion-only change is allowed (exit $rc)"
        else
            fail "deletion-only change was blocked (exit $rc) -- false positive, see #1649"
        fi
    else
        fail "could not create a worktree for the deletion-only case"
    fi

    if new_worktree clean; then
        ( cd "$NEW_WT" && printf 'const greeting = "hello";\n' > probe.js \
            && git add probe.js && "$SCANNER" >/dev/null 2>&1 )
        rc=$?
        if [ "$rc" -eq 0 ]; then
            pass "clean file in a worktree is allowed (exit $rc)"
        else
            fail "clean file in a worktree was blocked (exit $rc) -- false positive"
        fi
    else
        fail "could not create the second worktree under $WT_BASE"
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL  skipped: $SKIP"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
