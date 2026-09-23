#!/usr/bin/env bash
# test-release-completeness.sh -- a release must not be able to look shipped while incomplete,
# and a re-run must be able to reach the step it exists to retry (#2041).
#
# The defect this guards, measured on v1.48.36:
#
#   17:12:04  ✅ Uploaded lfr-tunnel-linux-arm64      (7 of 9 assets)
#   17:17:05  ##[error]Headers Timeout Error          (the two lfr-tunneld binaries, the largest)
#
# The job failed, but the RELEASE existed, was not a draft, and read as published. Only counting
# its assets revealed two were missing, so anyone installing the gateway from it got a 404.
#
# Re-running then failed EARLIER, at Push Checksums: a re-run produces identical checksums, and
# `git commit` exits non-zero with nothing to commit. Under `bash -e` that killed the step before
# Publish Release -- the step a re-run exists to retry. One transient timeout therefore became
# unrecoverable, and the tap step (which runs after publish) never ran at all, leaving Homebrew a
# version behind with nothing saying so.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WF="${REPO_ROOT}/.github/workflows/release.yml"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Release completeness cases:"
echo ""

if [ ! -f "$WF" ]; then
    fail "release.yml is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# PREMISE. Without this every case below could pass because the file is empty or renamed.
if grep -q "Publish Release" "$WF"; then
    pass "PREMISE  the workflow still has a Publish Release step to reason about"
else
    fail "PREMISE  no Publish Release step -- this guard is reading the wrong file"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# FIRING. The re-runnability defect: a no-op commit must not kill the step.
checksum_commit=$(grep -n 'git commit -m "update checksums' "$WF" | head -1 | cut -d: -f2-)
if printf '%s' "$checksum_commit" | grep -qE '\|\|'; then
    pass "a re-run's unchanged checksums do not kill the step before Publish Release"
else
    fail "git commit for checksums has no || fallback -- an unchanged checksums file exits
        non-zero under 'bash -e', so a re-run dies BEFORE the step it exists to retry"
fi

# FIRING. The completeness defect: something must compare published assets to what was built.
if grep -qiE "Verify every built artefact|assets\[\]\.name" "$WF"; then
    pass "the published asset set is verified against what was built"
else
    fail "nothing checks that every built artefact reached the release -- an upload timeout
        leaves a release that looks published and is missing files"
fi

# The verification logic moved out of the `run:` block and into a script in #2203, because a
# workflow that triggers only on a tag push -- and a tag that cannot be re-pushed -- has no way
# to exercise a change to it short of shipping. So the assertions below follow it there. The
# behaviour itself (polling, and the two distinct verdicts) is covered by executing the script in
# tests/hooks/test-release-asset-verification.sh; what is asserted HERE is the wiring, which has
# no runtime to observe.
STEP="$(awk '/Verify every built artefact/{f=1;next} f&&/^      - name:/{f=0} f' "$WF")"
GATE_REL="$(printf '%s\n' "$STEP" | grep -oE 'scripts/[A-Za-z0-9_.-]+\.sh' | head -1)"

if [ -n "$GATE_REL" ] && [ -x "${REPO_ROOT}/${GATE_REL}" ]; then
    pass "the verification step calls $GATE_REL, which exists and is executable"
    GATE="${REPO_ROOT}/${GATE_REL}"
else
    fail "the verification step does not call an executable script under scripts/ -- if the logic
        moved back inline, move these assertions back with it (it is then untestable again)"
    GATE=""
fi

if [ -n "$GATE" ]; then
    # FIRING. Verifying is not enough if it only warns: an incomplete release must FAIL the job.
    if grep -qF "The release is INCOMPLETE and must not be treated as shipped." "$GATE" &&
       grep -qE '^[[:space:]]*exit 1$' "$GATE"; then
        pass "an incomplete release fails the job rather than warning"
    else
        fail "the verification gate cannot fail the job, so an incomplete release still reports
        success. That wording is what makes people act on a red release run -- keep it."
    fi

    # BOUNDING. It must re-upload rather than merely report, or a transient timeout still needs a
    # human and a new tag to fix.
    if grep -qE 'release upload' "$GATE"; then
        pass "BOUNDING  a missing asset is re-uploaded, not just reported"
    else
        fail "BOUNDING  nothing re-uploads a missing asset, so a flake still costs a release"
    fi

    # FIRING (#2203). The listing must not be read once and believed. A fixed `sleep` before a
    # single read is explicitly NOT the fix -- it moves the race rather than removing it, and
    # v1.48.52's listing was still stale 105 seconds after the uploads finished.
    if grep -qE '(_delay|delay)=\$\(\((_delay|delay) \* 2\)\)' "$GATE"; then
        pass "the listing is polled with a growing backoff, not read once"
    else
        fail "no backoff found in $GATE_REL -- if this became a fixed sleep, the race is still
        there and returns whenever GitHub is slower than the chosen constant"
    fi

    # FIRING (#2203, §5c). "5 artefacts are missing" had two possible causes and one message.
    # Both verdicts must be reachable and must read differently.
    if grep -qF "GENUINELY MISSING" "$GATE" && grep -qF "NOT a missing artefact" "$GATE"; then
        pass "a shortfall says WHICH cause it has: absent, or a listing that has not caught up"
    else
        fail "the gate cannot name which of the two causes a shortfall has, which is the defect
        #2203 was filed for -- see §5c"
    fi

    # The gate on the gate (#1929): a behavioural suite nobody runs protects nothing.
    if grep -qF "tests/hooks/test-release-asset-verification.sh" "${REPO_ROOT}/Makefile"; then
        pass "the behavioural suite for $GATE_REL runs under 'make test-hooks'"
    else
        fail "test-release-asset-verification.sh is not in the Makefile's test-hooks list, so the
        only executable coverage of the release gate runs nowhere"
    fi
fi

# CONTROL. The guard must be reading the real file, not passing on any text that mentions the
# words. A deliberately wrong path has to fail every case above.
if grep -q "softprops/action-gh-release" "$WF"; then
    pass "CONTROL  the file really is the release workflow, not something that merely mentions it"
else
    fail "CONTROL  no publish action found -- these assertions are matching the wrong file"
fi

# CONTROL for the tap ordering, which is why an early failure costs Homebrew a version: the tap
# step must come AFTER publish, so anything that stops publish also stops the tap. Pinning it so
# a future edit that reorders them is a deliberate choice rather than an accident.
pub_line=$(grep -n "name: Publish Release" "$WF" | head -1 | cut -d: -f1)
tap_line=$(grep -n "name: Update Homebrew Tap" "$WF" | head -1 | cut -d: -f1)
if [ -n "$pub_line" ] && [ -n "$tap_line" ] && [ "$tap_line" -gt "$pub_line" ]; then
    pass "CONTROL  the tap step still runs after publish (so its failure mode is understood)"
else
    fail "CONTROL  the tap/publish ordering changed; re-read why a publish failure also loses the tap"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
