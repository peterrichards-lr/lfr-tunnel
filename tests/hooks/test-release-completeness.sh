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

# FIRING. Verifying is not enough if it only warns: an incomplete release must FAIL the job.
if awk '/Verify every built artefact/{f=1;next} f&&/^      - name:/{f=0} f' "$WF" | grep -qE "exit 1"; then
    pass "an incomplete release fails the job rather than warning"
else
    fail "the verification step cannot fail the job, so an incomplete release still reports success"
fi

# BOUNDING. It must re-upload rather than merely report, or a transient timeout still needs a
# human and a new tag to fix.
if awk '/Verify every built artefact/{f=1;next} f&&/^      - name:/{f=0} f' "$WF" | grep -qE "gh release upload"; then
    pass "BOUNDING  a missing asset is re-uploaded, not just reported"
else
    fail "BOUNDING  nothing re-uploads a missing asset, so a flake still costs a release"
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
