#!/usr/bin/env bash
# test-release-asset-verification.sh -- scripts/verify-release-assets.sh must tell "the asset is
# absent" apart from "the listing has not caught up", and must still fail a genuinely incomplete
# release (#2203).
#
# Why this file exists at all: .github/workflows/release.yml runs only on `push: tags: ['v*']`,
# has no workflow_dispatch, and the `Protect Version Tags` ruleset forbids deleting or updating
# a tag for everyone. A release therefore cannot be replayed, so the verification step could not
# be exercised except by shipping. Extracting it into a script is what makes these cases
# runnable; the cases are the reason the extraction was worth doing.
#
# The defect, measured on v1.48.52: all nine assets uploaded at 09:09:51Z, the listing returned
# four of them, and every re-upload came back `HTTP 422 ... ReleaseAsset.name already exists` --
# proof the assets were attached, printed in the job's own log, while the job failed with
# "5 built artefact(s) are missing ... must not be treated as shipped".
#
# So the two scenarios below are deliberately IDENTICAL from the listing's point of view and
# differ only in what the upload endpoint says. If they do not produce different verdicts, the
# fix is not present:
#
#   LAG      listing never settles + upload says "already exists"  -> exit 0, release complete
#   MISSING  listing never settles + upload fails otherwise        -> exit 1, INCOMPLETE
#
# bash 3.2 clean: the Makefile invokes this via test-hooks, so test-shell-portability.sh derives
# it into the portable set.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/verify-release-assets.sh"

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

# The nine artefacts a real release builds, in the order v1.48.52 uploaded them. The four the
# listing did return are the four alphabetically first, which is what the PARTIAL set below is.
ALL_ASSETS='checksums.txt
checksums.txt.minisig
lfr-tunnel-darwin-amd64
lfr-tunnel-darwin-arm64
lfr-tunnel-linux-amd64
lfr-tunnel-linux-arm64
lfr-tunnel-windows-amd64.exe
lfr-tunneld-linux-amd64
lfr-tunneld-linux-arm64'
PARTIAL_ASSETS='checksums.txt
checksums.txt.minisig
lfr-tunnel-darwin-amd64
lfr-tunnel-darwin-arm64'

# --------------------------------------------------------------------------------------------
# The stub. `gh` is replaced on PATH, so the gate exercises its real code path -- the same
# `gh release view --json assets` and `gh release upload` invocations, not a re-implementation.
# It records every call, which is how the assertions below can tell WHICH endpoint decided the
# outcome rather than only that the exit code was right.
# --------------------------------------------------------------------------------------------
mkdir -p "$WORK/bin"

# The clock and the sleep are stubbed TOGETHER, and the pairing is the point (#2238).
#
# The settle loop is bounded on time, so "the polls land at t=0, t=1 and t=3" depended on how fast
# this machine spawns the stubbed `gh` -- green in CI, red on a workstation, with the subject
# unchanged. Here the sleep ADVANCES the clock by exactly the delay it was asked to wait, so the
# timeline these cases describe is the timeline they get, on any machine, and the suite stops
# spending real seconds asleep.
#
# This replaces an earlier decision to let the backoff run for real, on the grounds that stubbing
# it would leave the retry untested. It does not: the loop still iterates, still doubles, and the
# delays it asks for are now RECORDED and asserted as a sequence -- stronger evidence than
# inferring them from elapsed wall-clock ever was. Same reasoning, and the same fix, as #2220
# applied to confirm-tap-version.sh.
cat > "$WORK/bin/lft-now" <<'CLOCK'
#!/usr/bin/env bash
D="$LFT_STUB_DIR"
cat "$D/clock" 2>/dev/null || printf '0\n'
CLOCK
cat > "$WORK/bin/lft-sleep" <<'NAP'
#!/usr/bin/env bash
D="$LFT_STUB_DIR"
printf '%s\n' "$1" >> "$D/backoff.log"
_n=$(cat "$D/clock" 2>/dev/null || printf '0\n')
printf '%s\n' "$((_n + $1))" > "$D/clock"
NAP
chmod +x "$WORK/bin/lft-now" "$WORK/bin/lft-sleep"

cat > "$WORK/bin/gh" <<'STUB'
#!/usr/bin/env bash
D="$LFT_STUB_DIR"
printf '%s\n' "$*" >> "$D/calls.log"
case "$1 $2" in
    "release view")
        n=$(cat "$D/view-count" 2>/dev/null || echo 0)
        n=$((n + 1))
        printf '%s\n' "$n" > "$D/view-count"
        if [ "$n" -le "$(cat "$D/partial-calls")" ]; then
            cat "$D/list-partial"
        else
            cat "$D/list-full"
        fi
        exit 0
        ;;
    "release upload")
        printf '%s\n' "$*" >> "$D/uploads.log"
        case "$(cat "$D/upload-mode")" in
            ok)
                printf 'Successfully uploaded %s\n' "$3"
                exit 0
                ;;
            exists)
                # Verbatim from run 35840689318, the v1.48.52 release job.
                printf 'HTTP 422: Validation Failed (https://uploads.github.com/repos/o/r/releases/394480358/assets?label=&name=%s)\n' "$3"
                printf 'ReleaseAsset.name already exists\n'
                exit 1
                ;;
            *)
                printf 'HTTP 502: Bad gateway (https://uploads.github.com/repos/o/r/releases/394480358/assets)\n'
                exit 1
                ;;
        esac
        ;;
esac
printf 'stub: unexpected invocation: %s\n' "$*" >&2
exit 127
STUB
chmod +x "$WORK/bin/gh"

# $1 case name, $2 partial-call count, $3 upload mode, $4 assets to place in dist/,
# $5 settle budget in seconds (default 1 -- only the "listing catches up" case needs room).
# Result in RUN_OUT / RUN_RC / RUN_STUB.
RUN_OUT=""
RUN_RC=0
RUN_STUB=""
run_case() {
    _name="$1"
    _stub="$WORK/state-$_name"
    _dist="$WORK/dist-$_name"
    rm -rf "$_stub" "$_dist"
    mkdir -p "$_stub" "$_dist"

    printf '%s\n' "$2" > "$_stub/partial-calls"
    printf '%s\n' "$3" > "$_stub/upload-mode"
    printf '%s\n' "$PARTIAL_ASSETS" > "$_stub/list-partial"
    printf '%s\n' "$ALL_ASSETS" > "$_stub/list-full"

    while IFS= read -r _a; do
        [ -n "$_a" ] || continue
        printf 'bytes\n' > "$_dist/$_a"
    done <<DIST_INPUT
$4
DIST_INPUT

    RUN_STUB="$_stub"
    # The clock is virtual and the sleep advances it, so the polls land at t=0, t=1 and t=3 as the
    # delay doubles 1 -> 2 on every machine (#2238). Budgets stay as they were: 1s for the cases
    # where the listing never settles, 5s for the one that catches up on its third poll.
    RUN_OUT="$(
        PATH="$WORK/bin:$PATH" \
        LFT_STUB_DIR="$_stub" \
        LFT_RELEASE_NOW="$WORK/bin/lft-now" \
        LFT_RELEASE_SLEEP="$WORK/bin/lft-sleep" \
        LFT_RELEASE_SETTLE_SECONDS="${5:-1}" \
        LFT_RELEASE_POLL_INITIAL=1 \
        LFT_RELEASE_POLL_MAX=2 \
        "$GATE" v1.48.52 "$_dist" 2>&1
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

echo "Release asset verification cases:"
echo ""

# --------------------------------------------------------------------------------------------
echo "-- happy path: the listing already agrees"
# --------------------------------------------------------------------------------------------
run_case happy 0 error "$ALL_ASSETS"
if [ "$RUN_RC" -eq 0 ]; then
    pass "a complete release passes (exit 0)"
else
    fail "a complete release exited $RUN_RC"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it says the release is complete" "$RUN_OUT" "is COMPLETE: all 9 built artefact(s) are published"
says "it reports how many polls it needed" "$RUN_OUT" "Asset listing agreed after 1 poll(s)"
# CONTROL for the harness: if the stub were never reached, every case here would be measuring
# nothing. upload-mode is deliberately `error` so a stray upload would also turn this red.
if [ -s "$RUN_STUB/calls.log" ]; then
    pass "CONTROL  the gate really invoked the stubbed gh"
else
    fail "CONTROL  the stub was never called -- these cases are not exercising the gate"
fi
if [ ! -f "$RUN_STUB/uploads.log" ]; then
    pass "the happy path costs no upload calls"
else
    fail "the happy path uploaded something it did not need to"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: the listing is slow but catches up. This is what the fixed-read gate got wrong."
# --------------------------------------------------------------------------------------------
run_case slow 2 error "$ALL_ASSETS" 5
if [ "$RUN_RC" -eq 0 ]; then
    pass "a slow listing that catches up passes (exit 0)"
else
    fail "a slow listing that catches up exited $RUN_RC -- polling did not absorb the lag"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it polled more than once" "$RUN_OUT" "Asset listing agreed after 3 poll(s)"
says_not "it did not report anything missing" "$RUN_OUT" "INCOMPLETE"
# The distinction that matters: this was resolved by POLLING, not by probing the upload
# endpoint. If it had uploaded, the gate would be papering over the lag rather than waiting it
# out -- and a re-upload of an asset that is already there is exactly what v1.48.52 did 15 times.
if [ ! -f "$RUN_STUB/uploads.log" ]; then
    pass "the lag was absorbed by polling alone, with no upload attempted"
    # The backoff ASKED FOR, asserted as a sequence (#2238).
    #
    # This is what the wall-clock budget used to stand in for, and it is a better witness:
    # elapsed time is satisfied by any arrangement that takes about that long, including one
    # that polls twice on a slow box and gives up. The delays themselves can only be produced
    # by the doubling the loop is supposed to perform -- 1, then 2, capped at POLL_MAX=2.
    _backoff="$(tr '\n' ' ' < "$RUN_STUB/backoff.log" 2>/dev/null | sed 's/[[:space:]]*$//')"
    if [ "$_backoff" = "1 2" ]; then
        pass "the backoff doubled and was capped: it waited 1s then 2s between polls"
    else
        fail "the backoff sequence was \"$_backoff\", want \"1 2\" -- the loop is not doubling
        its delay, or is not capping it at LFT_RELEASE_POLL_MAX"
    fi
else
    fail "it re-uploaded assets that were already published"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: v1.48.52 exactly. Listing never settles; the assets ARE there."
# --------------------------------------------------------------------------------------------
run_case lag 999 exists "$ALL_ASSETS"
if [ "$RUN_RC" -eq 0 ]; then
    pass "a complete release with a permanently stale listing PASSES (exit 0)"
else
    fail "exit $RUN_RC -- this is #2203 reproduced: a complete release failed the gate"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it names the cause as listing lag" "$RUN_OUT" "read-after-write lag in GitHub's listing API, NOT a missing artefact"
says "it cites the evidence it decided on" "$RUN_OUT" "ReleaseAsset.name already exists"
says "it still concludes the release is complete" "$RUN_OUT" "is COMPLETE"
says_not "it does not use the incomplete-release wording" "$RUN_OUT" "must not be treated as shipped"
if [ -f "$RUN_STUB/uploads.log" ]; then
    pass "it asked the upload endpoint, which is the only thing that can tell the two apart"
else
    fail "it never probed the upload endpoint, so it cannot have distinguished anything"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: genuinely absent, and recoverable. Same listing, upload succeeds."
# --------------------------------------------------------------------------------------------
run_case recover 999 ok "$ALL_ASSETS"
if [ "$RUN_RC" -eq 0 ]; then
    pass "an asset that really was absent is re-uploaded and the release passes"
else
    fail "exit $RUN_RC -- a recoverable gap should be recovered, not reported"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it says the asset was genuinely absent" "$RUN_OUT" "was GENUINELY ABSENT from v1.48.52 and has now been re-uploaded"
says_not "it does not blame the listing when the upload succeeded" "$RUN_OUT" "read-after-write lag"

# --------------------------------------------------------------------------------------------
echo ""
echo "-- FIRING: genuinely incomplete. The gate must still fail, and say so in those words."
# --------------------------------------------------------------------------------------------
run_case missing 999 error "$ALL_ASSETS"
if [ "$RUN_RC" -eq 1 ]; then
    pass "a genuinely incomplete release FAILS (exit 1)"
else
    fail "exit $RUN_RC -- the gate has been weakened into uselessness"
    printf '%s\n' "$RUN_OUT" | sed 's/^/        /'
fi
says "it names the cause as genuinely missing" "$RUN_OUT" "are GENUINELY MISSING from v1.48.52"
says "it reports the upload endpoint's reason" "$RUN_OUT" "HTTP 502: Bad gateway"
says "it keeps the wording people act on" "$RUN_OUT" "The release is INCOMPLETE and must not be treated as shipped."
says_not "it does not excuse the failure as listing lag" "$RUN_OUT" "read-after-write lag"

# --------------------------------------------------------------------------------------------
echo ""
echo "-- the two verdicts must be separable, not merely both present somewhere"
# --------------------------------------------------------------------------------------------
# LAG and MISSING above differ in exactly one input -- what the upload endpoint replies -- and
# nothing else. Asserting that here rather than trusting the two blocks to have stayed comparable.
run_case sep_lag 999 exists "$ALL_ASSETS"
lag_rc=$RUN_RC
lag_out="$RUN_OUT"
run_case sep_missing 999 error "$ALL_ASSETS"
if [ "$lag_rc" -eq 0 ] && [ "$RUN_RC" -eq 1 ]; then
    pass "identical listing behaviour, opposite verdicts (lag 0 / missing 1)"
else
    fail "lag exited $lag_rc and missing exited $RUN_RC -- the gate cannot tell them apart"
fi
if printf '%s' "$lag_out" | grep -qF "NOT a missing artefact" &&
   printf '%s' "$RUN_OUT" | grep -qF "GENUINELY MISSING"; then
    pass "and each says which cause it found"
else
    fail "the two causes do not produce distinguishable messages"
fi

# --------------------------------------------------------------------------------------------
echo ""
echo "-- anti-vacuity: nothing to verify is a failure, not a pass"
# --------------------------------------------------------------------------------------------
run_case empty 0 error ""
if [ "$RUN_RC" -eq 1 ]; then
    pass "an empty dist directory fails rather than reporting 0 of 0 published"
else
    fail "exit $RUN_RC on an empty expected set -- a scan of nothing reported success"
fi
says "and it says why" "$RUN_OUT" "Refusing to report a release complete against an empty expected set."

# --------------------------------------------------------------------------------------------
echo ""
echo "-- MUTATION CONTROL: remove the branch that reads the 422, and the lag case must go red"
# --------------------------------------------------------------------------------------------
# Without this, every assertion above could be satisfied by a gate that simply never fails. The
# mutant keeps the polling and the upload probe and removes only the recognition of
# "already exists", which is the single line that separates the two causes. The lag case must
# then be misreported as a missing artefact -- i.e. #2203 must come back.
MUTANT="$WORK/mutant.sh"
sed "s/grep -qi 'already exist'/grep -qi 'this-will-never-match'/" "$GATE" > "$MUTANT"
chmod +x "$MUTANT"
if cmp -s "$GATE" "$MUTANT"; then
    fail "MUTATION  the mutation changed nothing -- the branch it targets has been renamed"
else
    mut_stub="$WORK/state-mutant"
    mut_dist="$WORK/dist-sep_lag"
    rm -rf "$mut_stub"; mkdir -p "$mut_stub"
    printf '999\n' > "$mut_stub/partial-calls"
    printf 'exists\n' > "$mut_stub/upload-mode"
    printf '%s\n' "$PARTIAL_ASSETS" > "$mut_stub/list-partial"
    printf '%s\n' "$ALL_ASSETS" > "$mut_stub/list-full"
    mut_out="$(
        PATH="$WORK/bin:$PATH" \
        LFT_STUB_DIR="$mut_stub" \
        LFT_RELEASE_NOW="$WORK/bin/lft-now" \
        LFT_RELEASE_SLEEP="$WORK/bin/lft-sleep" \
        LFT_RELEASE_SETTLE_SECONDS=1 \
        LFT_RELEASE_POLL_INITIAL=1 \
        LFT_RELEASE_POLL_MAX=2 \
        "$MUTANT" v1.48.52 "$mut_dist" 2>&1
    )"
    mut_rc=$?
    if [ "$mut_rc" -eq 1 ] && printf '%s' "$mut_out" | grep -qF "GENUINELY MISSING"; then
        pass "MUTATION  without the 422 branch the complete release is misreported as missing (exit 1)"
    else
        fail "MUTATION  the mutant exited $mut_rc and was not killed for the right reason -- the
        lag/missing distinction is coming from somewhere other than the 422 check"
        printf '%s\n' "$mut_out" | sed 's/^/        /'
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
