#!/bin/bash
#
# Pre-commit hook: only the checks that are fast AND catch something painful to undo (#1343).
#
# A commit is a local checkpoint; a push is where work becomes shared. Gating commits on the
# full suite meant a checkpoint cost minutes, which is a good way to teach people --no-verify.
# Everything slower lives in scripts/pre-push-hook.sh, and golangci-lint moved to CI only,
# where it already ran.
#
# What stays here, and why:
#   gitleaks   - a secret that reaches a commit is in history; rewriting is the expensive fix
#   EDR guard  - 15 lines, and the thing it prevents costs a machine reinstall
#   gofmt      - stops formatting churn entering the tree at all
#   node -c    - milliseconds, catches a typo before it ships
#
# Every check is scoped to STAGED files. `gofmt -l .` used to walk the whole tree on every
# commit regardless of what changed.

set -uo pipefail

# Pinned rather than :latest so Docker resolves from cache instead of hitting the registry on
# every commit, and so a gitleaks release cannot change what this hook accepts (#1343).
GITLEAKS_IMAGE="zricethezav/gitleaks:v8.30.1"

staged() {
    git diff --cached --name-only --diff-filter=ACM -- "$@"
}

echo "[Git Hook] Scanning staged files for secrets/tokens..."
if ! docker run --rm -v "$(pwd)":/app -w /app "$GITLEAKS_IMAGE" \
        protect --source=/app --verbose --staged; then
    echo ""
    echo "❌ Error: Git commit blocked because a secret or private token was detected."
    echo "If this is a false positive, add the secret value to '.gitleaksignore' to allow it."
    echo ""
    exit 1
fi
echo "✅ No secrets detected."

echo "[Git Hook] Running SentinelOne EDR Safety Guard check..."
if ! ./scripts/check-edr-safety.sh; then
    echo "❌ Error: EDR safety check failed."
    exit 1
fi

GO_FILES=$(staged '*.go')
if [ -n "$GO_FILES" ]; then
    echo "[Git Hook] Checking staged Go files are formatted..."
    # shellcheck disable=SC2086
    UNFORMATTED=$(gofmt -l $GO_FILES)
    if [ -n "$UNFORMATTED" ]; then
        echo "❌ Error: The following staged files are not formatted properly:"
        echo "$UNFORMATTED"
        echo "Formatting them now..."
        # shellcheck disable=SC2086
        gofmt -w $UNFORMATTED
        echo "❌ Error: Git commit blocked because files were modified by formatting."
        echo "Please restage these files ('git add .') and try committing again."
        exit 1
    fi
fi

JS_FILES=$(staged 'pkg/server/static/*.js')
if [ -n "$JS_FILES" ]; then
    if command -v node >/dev/null 2>&1; then
        echo "[Git Hook] Checking staged JavaScript syntax..."
        while IFS= read -r js_file; do
            if ! node -c "$js_file"; then
                echo "❌ Error: JavaScript syntax check failed for $js_file."
                exit 1
            fi
        done <<< "$JS_FILES"
        echo "✅ Vanilla JavaScript syntax check passed."
    else
        echo "⚠️ Warning: 'node' not found in PATH. Skipping JavaScript syntax check."
    fi
fi

echo "✅ Pre-commit checks passed. (vet, tests and the UI build run on push.)"
exit 0
