#!/usr/bin/env bash
# test-tap-version-confirmation.sh -- scripts/confirm-tap-version.sh must tell "the push did not
# land" apart from "the contents API has not caught up", and must still fail a tap that really
# was not updated (#2204).
#
# The defect: .github/workflows/tap-bucket.yml read the formula back exactly ONCE, immediately
# after the push, and reported a mismatch as
#
#   ::error::The tap still does not advertise $want after a push that reported success.
#
# One read of an eventually-consistent API, and one sentence for two different facts. GitHub's
# contents API is served from a cache keyed on the ref, so a read taken seconds after a push to
# the default branch can legitimately return the previous blob -- and the step then fails a
# correct release and blames the push. Same class as #2203, one API over.
#
# So the two scenarios below are deliberately IDENTICAL from the cached default-branch read's
# point of view and differ only in what the file contains AT THE BRANCH HEAD. If they do not
# produce different verdicts, the fix is not present:
#
#   LAG          cached read never agrees + head commit HAS the version  -> exit 0, confirmed
#   NOT LANDED   cached read never agrees + head commit lacks it         -> exit 1, push failed
#
# bash 3.2 clean: the Makefile invokes this via test-hooks, so test-shell-portability.sh derives
# it into the portable set.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/confirm-tap-version.sh"
WORKFLOW="${REPO_ROOT}/.github/workflows/tap-bucket.yml"

TAP_REPO="acme/homebrew-tap"
TAP_FILE="Formula/lfr-tunnel.rb"
NEW_VERSION="1.48.53"
OLD_VERSION="1.48.52"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -x "$GATE" ]; then
    echo "FATAL: $GATE missing or not executable -- if it moved, move this guard with it"
    exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT INT TERM

# --------------------------------------------------------------------------------------------
# The stub. `gh` is replaced on PATH, so the gate exercises its real code path -- the same
# `gh api .../contents/...`, `gh api repos/<repo>` and `gh api .../git/ref/heads/...` calls, not
# a re-implementation. It records every call, which is how the assertions below can tell WHICH
# endpoint decided the outcome rather than only that the exit code was right.
#
# The two contents reads are distinguished exactly as GitHub distinguishes them: the sha-pinned
# one carries `?ref=`. That is the whole mechanism under test, so the stub must not conflate them.
# --------------------------------------------------------------------------------------------
mkdir -p "$WORK/bin"
cat > "$WORK/bin/gh" <<'STUB'
#!/usr/bin/env bash
D="$LFT_STUB_DIR"
printf '%s\n' "$*" >> "$D/calls.log"

# The URL is whichever argument looks like an API path; -H and --jq surround it.
url=""
for a in "$@"; do
    case "$a" in repos/*) url="$a" ;; esac
done

case "$url" in
    */contents/*\?ref=*)
        printf '%s\n' "$url" >> "$D/pinned.log"
        case "$(cat "$D/pinned-mode")" in
            new)     cat "$D/formula-new" ;;
            old)     cat "$D/formula-old" ;;
            noversion) cat "$D/formula-broken" ;;
            *)       printf 'gh: Not Found (HTTP 404)\n' >&2; exit 1 ;;
        esac
        exit 0
        ;;
    */contents/*)
        printf '%s\n' "$url" >> "$D/branch-reads.log"
        n=$(cat "$D/read-count" 2>/dev/null || echo 0)
        n=$((n + 1))
        printf '%s\n' "$n" > "$D/read-count"
        if [ "$n" -le "$(cat "$D/stale-reads")" ]; then
            cat "$D/formula-old"
        else
            cat "$D/formula-new"
        fi
        exit 0
        ;;
    */git/ref/heads/*)
        printf '%s\n' "$url" >> "$D/ref.log"
        if [ "$(cat "$D/ref-mode")" = "fail" ]; then
            printf 'gh: Not Found (HTTP 404)\n' >&2
            exit 1
        fi
        cat "$D/head-sha"
        exit 0
        ;;
    repos/*)
        printf '%s\n' "$url" >> "$D/repo.log"
        if [ "$(cat "$D/repo-mode")" = "fail" ]; then
            printf 'gh: Bad credentials (HTTP 401)\n' >&2
            exit 1
        fi
        printf 'main\n'
        exit 0
        ;;
esac
printf 'stub: unexpected invocation: %s\n' "$*" >&2
exit 127
STUB
chmod +x "$WORK/bin/gh"

HEAD_SHA="4f3c1b9a2d7e605c8a1b4d9e2f7c3a5b6d8e0f19"

# $1 case name, $2 stale-read count, $3 pinned mode (new|old|noversion|fail),
# $4 settle budget seconds (default 1), $5 ref mode (ok|fail), $6 repo mode (ok|fail),
# $7 expected version passed to the gate (default $NEW_VERSION; an explicit "" stays empty, which
#    is why it is ${7-...} and not ${7:-...} -- the anti-vacuity case depends on that).
# Result in RUN_OUT / RUN_RC / RUN_STUB.
RUN_OUT=""
RUN_RC=0
RUN_STUB=""
run_case() {
    _name="$1"
    _stub="$WORK/state-$_name"
    rm -rf "$_stub"
    mkdir -p "$_stub"

    printf '%s\n' "$2" > "$_stub/stale-reads"
    printf '%s\n' "$3" > "$_stub/pinned-mode"
    printf '%s\n' "${5:-ok}" > "$_stub/ref-mode"
    printf '%s\n' "${6:-ok}" > "$_stub/repo-mode"
    printf '%s\n' "$HEAD_SHA" > "$_stub/head-sha"

    # Real formula shape, so the extraction under test is the one the workflow relies on.
    printf 'class LfrTunnel < Formula\n  desc "Liferay Tunnel"\n  version "%s"\n  sha256 "deadbeef"\nend\n' \
        "$NEW_VERSION" > "$_stub/formula-new"
    printf 'class LfrTunnel < Formula\n  desc "Liferay Tunnel"\n  version "%s"\n  sha256 "deadbeef"\nend\n' \
        "$OLD_VERSION" > "$_stub/formula-old"
    printf 'class LfrTunnel < Formula\n  desc "Liferay Tunnel"\n  sha256 "deadbeef"\nend\n' \
        > "$_stub/formula-broken"

    RUN_STUB="$_stub"
    # The backoff runs for real rather than being stubbed out -- a stubbed sleep would leave the
    # thing the fix turns on untested. Budgets are kept small: 1s is enough for the cases where
    # the read never agrees, and the one case that does catch up gets 5s so its polls land
    # deterministically at t=0, t=1 and t=3 as the delay doubles 1 -> 2.
    RUN_OUT="$(
        PATH="$WORK/bin:$PATH" \
        LFT_STUB_DIR="$_stub" \
        LFT_TAP_SETTLE_SECONDS="${4:-1}" \
        LFT_TAP_POLL_INITIAL=1 \
        LFT_TAP_POLL_MAX=2 \
        "$GATE" "$TAP_REPO" "$TAP_FILE" "${7-$NEW_VERSION}" 2>&1
    )"
    RUN_RC=$?
}

# $1 label, $2 haystack, $3 needle
says() {
    if printf '%s' "$2" | grep -qF "$3"; then
        pass "$1"
    else
        fail "$1 -- output did not contain: $3"
        printf '%s\n' "$2" | sed 's/^/        /'
    fi
}
says_not() {
    if printf '%s' "$2" | grep -qF "$3"; then
        fail "$1 -- output wrongly contained: $3"
        printf '%s\n' "$2" | sed 's/^/        /'
    else
        pass "$1"
    fi
}

echo "Tap version confirmation cases:"
echo ""

# --------------------------------------------------------------------------------------------
echo "-- happy path: the first read already advertises the new version"
# --------------------------------------------------------------------------------------------
run_case happy 0 fail
if [ "$RUN_RC" -eq 0 ]; then
    pass "a landed push passes (exit 0)"
else
    fail "a landed push exited $RUN_RC"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it says what it confirmed" "$RUN_OUT" "advertises ${NEW_VERSION}. Confirmed."
says "it reports how many polls it needed" "$RUN_OUT" "agreed after 1 poll(s)"
# CONTROL for the harness: if the stub were never reached, every case here would be measuring
# nothing. pinned-mode is deliberately `fail` so a stray sha-pinned read would also turn this red.
if [ -s "$RUN_STUB/calls.log" ]; then
    pass "CONTROL  the gate really invoked the stubbed gh"
else
    fail "CONTROL  the stub was never called -- these cases are not exercising the gate"
fi
if [ ! -f "$RUN_STUB/pinned.log" ]; then
    pass "the happy path costs no extra API calls"
else
    fail "the happy path read the branch head it did not need"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: the cached read is slow but catches up. This is what the single read got wrong."
# --------------------------------------------------------------------------------------------
# pinned-mode is `old` on purpose: if the gate stopped polling after one read it would fall
# through to the branch head, find the previous version there, and fail. That is the pre-fix
# behaviour, and it is what the MUTATION CONTROL at the bottom reproduces.
run_case slow 2 old 5
if [ "$RUN_RC" -eq 0 ]; then
    pass "a slow cached read that catches up passes (exit 0)"
else
    fail "a slow cached read that catches up exited $RUN_RC -- polling did not absorb the lag"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it polled more than once" "$RUN_OUT" "agreed after 3 poll(s)"
says_not "it did not report a failed push" "$RUN_OUT" "THE PUSH DID NOT LAND"
# The distinction that matters: this was resolved by POLLING, not by reading the branch head.
if [ ! -f "$RUN_STUB/pinned.log" ]; then
    pass "the lag was absorbed by polling alone, with no sha-pinned read needed"
else
    fail "it read the branch head for a lag that polling had already resolved"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: the cache never catches up, but the push DID land."
# --------------------------------------------------------------------------------------------
run_case lag 999 new
if [ "$RUN_RC" -eq 0 ]; then
    pass "a landed push with a permanently stale cache PASSES (exit 0)"
else
    fail "exit $RUN_RC -- this is #2204 reproduced: a correct release failed its confirmation"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it names the cause as contents-API lag" "$RUN_OUT" "read-after-write lag in GitHub's contents API, NOT a failed push"
says "it cites the commit it decided on" "$RUN_OUT" "$HEAD_SHA"
says "it still confirms the version" "$RUN_OUT" "advertises ${NEW_VERSION}. Confirmed."
says_not "it does not use the failed-push wording" "$RUN_OUT" "THE PUSH DID NOT LAND"
if [ -f "$RUN_STUB/pinned.log" ]; then
    pass "it read the file at the branch head, which is the only thing that can tell the two apart"
else
    fail "it never read the branch head, so it cannot have distinguished anything"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: the push genuinely did not land. The gate must still fail, and say so."
# --------------------------------------------------------------------------------------------
run_case notlanded 999 old
if [ "$RUN_RC" -eq 1 ]; then
    pass "a push that did not land FAILS (exit 1)"
else
    fail "exit $RUN_RC -- the gate has been weakened into uselessness"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it names the cause as a failed push" "$RUN_OUT" "THE PUSH DID NOT LAND"
says "it reports what the head commit actually carries" "$RUN_OUT" "still advertises ${OLD_VERSION}, not ${NEW_VERSION}"
says "it says what it costs users" "$RUN_OUT" "will stay on ${OLD_VERSION}"
says_not "it does not excuse the failure as cache lag" "$RUN_OUT" "read-after-write lag"

# --------------------------------------------------------------------------------------------
echo ""
echo "-- the two verdicts must be separable, not merely both present somewhere"
# --------------------------------------------------------------------------------------------
# LAG and NOT LANDED above differ in exactly one input -- what the branch head carries -- and
# nothing else. Asserting that here rather than trusting the two blocks to have stayed comparable.
run_case sep_lag 999 new
sep_lag_rc=$RUN_RC
sep_lag_out="$RUN_OUT"
run_case sep_notlanded 999 old
if [ "$sep_lag_rc" -eq 0 ] && [ "$RUN_RC" -eq 1 ]; then
    pass "identical cached-read behaviour, opposite verdicts (lag 0 / not landed 1)"
else
    fail "lag exited $sep_lag_rc and not-landed exited $RUN_RC -- the gate cannot tell them apart"
fi
if printf '%s' "$sep_lag_out" | grep -qF "NOT a failed push" &&
   printf '%s' "$RUN_OUT" | grep -qF "THE PUSH DID NOT LAND"; then
    pass "and each says which cause it found"
else
    fail "the two causes do not produce distinguishable messages"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: a formula that landed with no version at all is its own cause, not either of those"
# --------------------------------------------------------------------------------------------
run_case broken 999 noversion
if [ "$RUN_RC" -eq 1 ]; then
    pass "a formula carrying no version fails (exit 1)"
else
    fail "exit $RUN_RC -- a broken render passed as a confirmed tap"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it names the render, not the push or the cache" "$RUN_OUT" "THE PUSH LANDED SOMETHING BROKEN"
says_not "it does not report a stale push" "$RUN_OUT" "THE PUSH DID NOT LAND"

# --------------------------------------------------------------------------------------------
echo ""
echo "-- undetermined is a failure, and must not be reported as either cause"
# --------------------------------------------------------------------------------------------
run_case norefs 999 new 1 fail
if [ "$RUN_RC" -eq 1 ] && printf '%s' "$RUN_OUT" | grep -qF "UNDETERMINED whether the push landed"; then
    pass "an unresolvable branch head fails as undetermined rather than guessing"
else
    fail "exit $RUN_RC -- an unresolvable branch head did not report itself as undetermined"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says_not "and it does not blame the push on no evidence" "$RUN_OUT" "THE PUSH DID NOT LAND"

run_case norepo 999 new 1 ok fail
if [ "$RUN_RC" -eq 1 ] && printf '%s' "$RUN_OUT" | grep -qF "UNDETERMINED whether the push landed"; then
    pass "an unreadable repository fails as undetermined too"
else
    fail "exit $RUN_RC -- an unreadable repository did not report itself as undetermined"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi

run_case nopinned 999 fail
if [ "$RUN_RC" -eq 1 ] && printf '%s' "$RUN_OUT" | grep -qF "UNDETERMINED whether the push landed"; then
    pass "a sha-pinned read that errors fails as undetermined, not as a verdict"
else
    fail "exit $RUN_RC -- a failed sha-pinned read produced a verdict it had no evidence for"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- anti-vacuity: nothing to confirm is a failure, not a pass"
# --------------------------------------------------------------------------------------------
run_case empty 0 new 1 ok ok ""
if [ "$RUN_RC" -eq 1 ]; then
    pass "an empty expected version fails rather than matching an unparseable read"
else
    fail "exit $RUN_RC on an empty expectation -- a confirmation of nothing reported success"
fi
says "and it says why" "$RUN_OUT" "Refusing to report the tap confirmed against an empty expectation."
if [ ! -f "$RUN_STUB/calls.log" ]; then
    pass "and it refuses before spending an API call"
else
    fail "it called the API before noticing it had nothing to compare against"
fi

# A leading v must be accepted, because the workflow passes its own tag input straight through.
run_case vprefix 0 fail 1 ok ok "v${NEW_VERSION}"
if [ "$RUN_RC" -eq 0 ]; then
    pass "a vX.Y.Z tag is accepted as-is, so the workflow need not strip it"
else
    fail "exit $RUN_RC -- a v-prefixed tag was not recognised"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- the workflow must actually use the script"
# --------------------------------------------------------------------------------------------
# The confirmation is only worth anything if tap-bucket.yml runs it. This is the half that
# test-tap-bucket.sh's CONTROL used to cover by grepping the inline step's error text; the text
# moved into the script, so the assertion moves with it (#2204).
if [ ! -f "$WORKFLOW" ]; then
    fail "tap-bucket.yml is missing -- if it moved, move this guard with it"
else
    if grep -qF "scripts/confirm-tap-version.sh" "$WORKFLOW"; then
        pass "tap-bucket.yml invokes the confirmation script"
    else
        fail "tap-bucket.yml no longer runs the confirmation -- a silent no-op could pass again"
    fi
    if grep -qE 'gh api .*contents/Formula' "$WORKFLOW"; then
        fail "tap-bucket.yml still reads the contents API inline -- the unretried read is back"
    else
        pass "the single unretried contents read is gone from the workflow"
    fi
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- MUTATION CONTROL: restore the single unretried read, and the slow case must go red"
# --------------------------------------------------------------------------------------------
# Without this, every assertion above could be satisfied by a gate that simply never fails. The
# mutant keeps everything else and removes only the deadline check that lets the loop poll again,
# so the loop breaks after one read -- exactly the pre-fix behaviour. The `slow` case must then
# be misreported as a failed push, i.e. #2204 must come back.
MUTANT="$WORK/mutant.sh"
sed 's/\[ "$(now)" -ge "$_deadline" \] && break/break/' "$GATE" > "$MUTANT"
chmod +x "$MUTANT"
if cmp -s "$GATE" "$MUTANT"; then
    fail "MUTATION  the mutation changed nothing -- the poll loop it targets has been rewritten"
else
    mut_stub="$WORK/state-mutant"
    rm -rf "$mut_stub"; mkdir -p "$mut_stub"
    printf '2\n' > "$mut_stub/stale-reads"
    printf 'old\n' > "$mut_stub/pinned-mode"
    printf 'ok\n' > "$mut_stub/ref-mode"
    printf 'ok\n' > "$mut_stub/repo-mode"
    printf '%s\n' "$HEAD_SHA" > "$mut_stub/head-sha"
    cp "$WORK/state-slow/formula-new" "$mut_stub/formula-new"
    cp "$WORK/state-slow/formula-old" "$mut_stub/formula-old"
    cp "$WORK/state-slow/formula-broken" "$mut_stub/formula-broken"
    mut_out="$(
        PATH="$WORK/bin:$PATH" \
        LFT_STUB_DIR="$mut_stub" \
        LFT_TAP_SETTLE_SECONDS=5 \
        LFT_TAP_POLL_INITIAL=1 \
        LFT_TAP_POLL_MAX=2 \
        "$MUTANT" "$TAP_REPO" "$TAP_FILE" "$NEW_VERSION" 2>&1
    )"
    mut_rc=$?
    if [ "$mut_rc" -eq 1 ] && printf '%s' "$mut_out" | grep -qF "THE PUSH DID NOT LAND"; then
        pass "MUTATION  with a single unretried read the slow-but-correct tap is misreported as a failed push (exit 1)"
    else
        fail "MUTATION  the mutant exited $mut_rc and was not killed for the right reason -- the
        lag tolerance is coming from somewhere other than the poll loop"
        printf '%s\n' "$mut_out" | sed 's/^/        /'
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
