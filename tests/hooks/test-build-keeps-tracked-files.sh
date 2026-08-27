#!/usr/bin/env bash
# test-build-keeps-tracked-files.sh — the build must not delete files git tracks (#1511)
#
# `make build` does `rm -rf pkg/server/ui-dist` and then copies ui/dist over it. ui/dist has no
# .gitkeep, so every build deleted a TRACKED file and left it staged for whoever next ran
# `git commit -a`.
#
# Losing that file is not cosmetic. .gitignore:81-83 records why it is tracked: without it a
# fresh clone has no pkg/server/ui-dist/ at all, and `//go:embed ui-dist/*` stops compiling for
# everyone -- reporting `pattern ui-dist/*: no matching files found`, which points at the embed
# rather than at the commit that removed the directory.
#
# CI cannot catch it. Every job that builds creates the directory itself first
# (`mkdir -p pkg/server/ui-dist && touch .../index.html`, the #1196 dummy), so a PR deleting the
# marker goes green, merges, and only breaks for whoever next clones fresh.
#
# So this is checked by reading the Makefile rather than by running a build: the failure is
# invisible on any machine that already has the directory, which is every machine that has built
# once. Reading the recipe catches it at the moment it is written.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MAKEFILE="${REPO_ROOT}/Makefile"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing that make build preserves tracked files"
echo ""

# The build recipe: from `build:` to the next line that starts at column zero and is not blank.
BUILD_RECIPE=$(awk '/^build:/{f=1;next} f && /^[^\t[:space:]]/{exit} f{print}' "$MAKEFILE")

if [ -z "$BUILD_RECIPE" ]; then
    fail "could not find the build target in $MAKEFILE -- if it was renamed, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# Every directory the recipe wipes. Only paths under the repo matter; `rm -rf bin` is fine
# because nothing tracked lives there.
WIPED=$(printf '%s\n' "$BUILD_RECIPE" | sed -n 's/^[[:space:]]*rm -rf \([A-Za-z0-9._/-]*\).*/\1/p')

CHECKED=0
for dir in $WIPED; do
    [ -d "${REPO_ROOT}/${dir}" ] || continue

    # What git tracks under it. Nothing tracked means nothing to lose.
    TRACKED=$(cd "$REPO_ROOT" && git ls-files "$dir")
    [ -n "$TRACKED" ] || continue

    for f in $TRACKED; do
        CHECKED=$((CHECKED + 1))
        base=$(basename "$f")
        # The recipe must put it back. Either explicitly by path, or by name via a touch/cp.
        if printf '%s\n' "$BUILD_RECIPE" | grep -v '^[[:space:]]*@\?#' | grep -q "$base"; then
            pass "$f is deleted by 'rm -rf $dir' and restored by the recipe"
        else
            fail "$f is tracked, is deleted by 'rm -rf $dir', and nothing in the build recipe restores it.
        Every build would leave it staged for deletion, and losing it breaks //go:embed on a
        fresh clone -- which CI cannot catch, because each job creates that directory itself."
        fi
    done
done

if [ "$CHECKED" -eq 0 ]; then
    fail "found no tracked files under any directory the build wipes.
        That should not happen while pkg/server/ui-dist/.gitkeep is tracked (see .gitignore),
        so this guard has most likely stopped looking in the right place."
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
