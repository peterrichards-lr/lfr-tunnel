#!/usr/bin/env bash
# check-staged-prettier.sh — Prettier-check the staged files Prettier owns (#1447)
#
# gofmt has been gated at commit time since #1343, on the stated grounds that it "stops
# formatting churn entering the tree at all". Nothing gated the other half of the tree --
# JavaScript, TypeScript, CSS and JSON -- so churn there reached CI instead, and cost a full
# round trip on #1446 for a whitespace-only change to a JSON file.
#
# Extracted from the hook rather than written inline, for the same reason scan-staged-secrets.sh
# was in #1377: a check embedded in a hook cannot be tested, and the interesting behaviour here is
# all in the failure paths.
#
# Exit codes:
#   0  nothing to check, everything formatted, or Prettier could not run
#   1  staged files are not formatted
#
# Deliberately exits 0 when the tool is unavailable. Unformatted code is not irreversible the way
# a leaked secret is, so a missing npm cache or no network must not block a commit -- the same
# call the hook's node syntax check makes. It says so loudly rather than passing quietly.
set -uo pipefail

# Pinned to the version CI uses. Prettier changes its output between releases, so an unpinned
# hook would start disagreeing with CI the day upstream ships a new one -- the same reasoning as
# the pinned invocation in ci.yml and the pinned gitleaks image in the secret scan.
PRETTIER_VERSION="${LFT_PRETTIER_VERSION:-prettier@3.9.6}"

# Prettier's own scope, per .prettierrc.yaml. Markdown, HTML and YAML are held back on purpose --
# .prettierignore records why for each -- and Prettier applies those ignore rules even to paths
# passed explicitly, so staged files can be handed over without filtering them here first.
# .cjs and .mjs are in the list because CI runs `prettier --check .` over the WHOLE tree, and a
# hook covering a narrower set than the gate it is meant to pre-empt is the shape that erodes
# trust in it -- a green hook stops meaning anything. That is not hypothetical: #1548 failed CI on
# scripts/check-theme-contrast.cjs after passing here (#1550). The repo uses both extensions for
# its check scripts.
staged_prettier_files() {
    git diff --cached --name-only --diff-filter=ACM -- \
        '*.js' '*.cjs' '*.mjs' '*.jsx' '*.ts' '*.tsx' '*.css' '*.json'
}

FILES="${LFT_PRETTIER_FILES-$(staged_prettier_files)}"
if [ -z "$FILES" ]; then
    exit 0
fi

if ! command -v npx >/dev/null 2>&1; then
    echo "⚠️  Warning: 'npx' not found in PATH, so Prettier formatting was NOT checked."
    exit 0
fi

echo "[Prettier] Checking staged JS/TS/CSS/JSON files..."
# shellcheck disable=SC2086
OUT=$(echo "$FILES" | xargs npx --yes "$PRETTIER_VERSION" --check 2>&1)
RC=$?

if [ "$RC" -eq 0 ]; then
    echo "✅ Prettier formatting check passed."
    exit 0
fi

# A real finding. Prettier says "Code style issues found" only when files parsed and differ from
# its output; anything else on a non-zero exit is the tool itself failing.
if echo "$OUT" | grep -q "Code style issues"; then
    echo "$OUT"
    echo "❌ Error: the staged files above are not Prettier-formatted."
    echo "Fix them with:"
    echo "  echo '$FILES' | xargs npx --yes $PRETTIER_VERSION --write"
    echo "then restage and commit again."
    exit 1
fi

echo "⚠️  Warning: Prettier could not run, so formatting was NOT checked:"
echo "$OUT" | tail -3
exit 0
