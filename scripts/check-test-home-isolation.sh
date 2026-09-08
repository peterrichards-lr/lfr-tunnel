#!/usr/bin/env bash
# check-test-home-isolation.sh -- a test that resolves the user's home must not read the real one.
#
# TestLoadClientConfig_TokenFile and TestInsecurePermissionWarning asserted the client's token
# resolution ladder by calling LoadClientConfig(""), which reads ~/.lfr-tunnel/config.yaml. On a
# machine with one, an inline auth_token: short-circuits the ladder and both fail; in CI, whose
# home is empty, both pass. The tests were right and the environment was not theirs (#1798).
#
# It fails in the worse direction: green where it proves least. So the fix is structural -- a
# TestMain per package rather than a t.Setenv per test -- and this is what stops a fifth package
# joining the class without one.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

# Calls that resolve a path through the home directory. UserHomeDir is the root of all of them;
# the other two are the pkg/config wrappers a test is far likelier to call directly.
HOME_REACHING='os\.UserHomeDir\(\)|ResolveDefaultConfigPath\(\)|(Load|Save)ClientConfig\(""\)'

# Anti-vacuity floor (#1779): a scan that examined nothing must not report success. Four
# packages qualify today; the floor sits below that so an honest removal does not break the
# build, but a glob that matches nothing does. Overridable so it can be exercised in place.
MIN_PACKAGES="${LFT_HOME_ISOLATION_MIN_PACKAGES:-2}"

# bash 3.2 on macOS has no mapfile, and this runs from a pre-push hook there.
packages=$(
    grep -rlE "$HOME_REACHING" --include='*_test.go' . 2>/dev/null |
        grep -v '/testmain_home_test\.go$' |
        xargs -n1 dirname 2>/dev/null |
        sort -u || true
)

count=$(printf '%s' "$packages" | grep -c . || true)

if [ "$count" -lt "$MIN_PACKAGES" ]; then
    echo "❌ Only $count package(s) matched the home-reaching patterns, expected at least $MIN_PACKAGES."
    echo "   Either the patterns have gone stale or this ran somewhere with no source in it."
    echo "   A check that examined nothing must not report success."
    exit 1
fi

failed=0
for pkg in $packages; do
    main="$pkg/testmain_home_test.go"
    if [ ! -f "$main" ]; then
        echo "❌ $pkg has tests that resolve the user's home but no $main"
        failed=1
        continue
    fi
    if ! grep -q 'testhome.Isolate()' "$main"; then
        echo "❌ $main exists but never calls testhome.Isolate()"
        failed=1
    fi
    if ! grep -q 'func TestPackageTestsCannotReachTheRealHome' "$main"; then
        echo "❌ $main has no TestPackageTestsCannotReachTheRealHome -- without it, deleting the"
        echo "   TestMain would be silent in CI, where the real home is empty either way"
        failed=1
    fi
done

if [ "$failed" -ne 0 ]; then
    echo
    echo "Copy internal/testhome's TestMain into the package(s) named above; pkg/config has one."
    exit 1
fi

echo "✅ All $count packages whose tests resolve the user's home isolate it."
