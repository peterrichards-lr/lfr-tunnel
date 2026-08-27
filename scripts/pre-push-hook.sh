#!/bin/bash
#
# Pre-push hook: the checks worth running before work becomes shared, but too slow to sit in
# front of every local checkpoint (#1343).
#
# Split out of pre-commit, which ran go vet, the test suite, a full React build and
# golangci-lint on every commit. golangci-lint is not repeated here -- CI runs it, and running
# it twice buys nothing but latency.
#
# The UI block is skipped entirely unless the push actually contains a ui/ change, which is
# what removes a pnpm install + React build from every backend push.

set -uo pipefail

ZERO="0000000000000000000000000000000000000000"

# git feeds pre-push "<local ref> <local oid> <remote ref> <remote oid>" per ref on stdin.
# Prefer that over a hardcoded origin/master so the diff is what is really being pushed.
RANGE=""
while read -r _local_ref local_oid _remote_ref remote_oid; do
    [ "$local_oid" = "$ZERO" ] && continue          # deleting a remote ref; nothing to check
    if [ "$remote_oid" = "$ZERO" ]; then
        RANGE="origin/master..$local_oid"           # new branch: everything not on master
    else
        RANGE="$remote_oid..$local_oid"
    fi
done

# Run by hand (no stdin), or a range git could not resolve.
if [ -z "$RANGE" ] || ! git rev-parse --quiet --verify "${RANGE%%..*}" >/dev/null 2>&1; then
    RANGE="origin/master..HEAD"
fi

CHANGED=$(git diff --name-only "$RANGE" 2>/dev/null)
if [ -z "$CHANGED" ]; then
    echo "[Git Hook] Nothing to check in $RANGE."
    exit 0
fi

changed_matching() {
    echo "$CHANGED" | grep -qE "$1"
}

if changed_matching '\.go$|^go\.(mod|sum)$'; then
    echo "[Git Hook] Running go vet..."
    if ! go vet ./...; then
        echo "❌ Error: 'go vet' failed. Please fix before pushing."
        exit 1
    fi

    echo "[Git Hook] Running tests..."
    # Via make test, which exports GOTMPDIR and asserts it before building (#1334, #1335).
    # Never open-code the loop here; that divergence is what put unsigned binaries outside the
    # EDR whitelist on every commit.
    #
    # pkg/server stays excluded: its tests open real listeners and are slow enough to make even
    # a push hook painful. CI runs the full set on every PR.
    PKGS=$(go list ./... | grep -v /pkg/server | tr '\n' ' ')
    if ! make test PKG="$PKGS"; then
        echo "❌ Error: Tests failed. Please fix before pushing."
        exit 1
    fi
else
    echo "[Git Hook] No Go changes in $RANGE; skipping vet and tests."
fi

if changed_matching '^ui/'; then
    if command -v pnpm >/dev/null 2>&1; then
        echo "[Git Hook] Checking React UI syntax and types..."
        if ! (cd ui && pnpm install && pnpm run lint && pnpm run build); then
            echo "❌ Error: React UI lint or build failed. Please fix before pushing."
            exit 1
        fi
        echo "✅ React UI checks passed."
    else
        echo "⚠️ Warning: 'pnpm' not found in PATH. Skipping React UI checks."
        echo "   pnpm 11 needs Node >= 22.13; see ui/.nvmrc."
    fi
else
    echo "[Git Hook] No ui/ changes in $RANGE; skipping the UI build."
fi

echo "✅ Pre-push checks passed."
exit 0
