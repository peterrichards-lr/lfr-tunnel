#!/usr/bin/env bash
# Safety guard to prevent plain 'go test' invocations that trigger SentinelOne EDR quarantines.
set -euo pipefail

echo "Scanning scripts and workflows for unsafe bare 'go test' invocations..."

# Check if any shell script or workflow contains bare 'go test' invocations outside Makefile
if grep -rnE "go test ([^\s\-c\-o]+)" --include="*.sh" --include="*.yml" . | grep -v "Makefile" | grep -v "check-edr-safety.sh" ; then
    echo "❌ EDR SAFETY CHECK FAILED!"
    echo "Direct 'go test' invocations detected."
    echo "ALWAYS use 'make test' (or 'make test PKG=... TEST_FLAGS=...') to execute test binaries safely through \$LFT_TEST_DIR."
    exit 1
fi

echo "✅ EDR Safety Check Passed: No un-whitelisted 'go test' commands found."
