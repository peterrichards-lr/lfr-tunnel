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
#                (scripts/scan-staged-secrets.sh -- extracted in #1377 so it can be tested)
#   EDR guard  - 15 lines, and the thing it prevents costs a machine reinstall
#   gofmt      - stops formatting churn entering the tree at all
#   node -c    - milliseconds, catches a typo before it ships
#
# Every check is scoped to STAGED files. `gofmt -l .` used to walk the whole tree on every
# commit regardless of what changed.

set -uo pipefail

staged() {
    git diff --cached --name-only --diff-filter=ACM -- "$@"
}

# Guarded on existence, not assumed present. Hooks are COPIES in .git/hooks (#1425), so an
# installed hook outlives the branch it came from: installing this one and then checking out
# a branch that predates the script made every commit there fail on a missing file. Skipping
# loudly is right here -- the check is absent, not passing, and saying so beats both a hard
# failure on unrelated branches and a silent pass.
if [ -x ./scripts/check-commit-attribution.sh ]; then
    echo "[Git Hook] Checking the commit will be attributable..."
    if ! ./scripts/check-commit-attribution.sh; then
        exit 1
    fi
else
    echo "[Git Hook] SKIPPED attribution check -- scripts/check-commit-attribution.sh is not on this branch."
fi

echo "[Git Hook] Scanning staged files for secrets/tokens..."
if ! ./scripts/scan-staged-secrets.sh; then
    exit 1
fi

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
