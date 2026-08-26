#!/bin/bash
#
# Keeps developer-facing shell scripts runnable on the shell developers actually have (#1395).
#
# macOS ships bash 3.2.57 as /bin/bash -- the last GPLv2 release, frozen in 2007. A script that
# uses `declare -A`, `mapfile`, `readarray` or `${var^^}` works in CI on ubuntu (bash 5) and dies
# on the maintainer's machine. That is how scripts/check-required-contexts.sh shipped in #1391
# exiting 1 with "line 33: Suite: unbound variable" before checking anything: a gate whose whole
# value is being run BEFORE pushing, that could not be run before pushing.
#
# Two layers, because neither alone is enough:
#
#   RUN   - actually execute the gate script under bash 3.2 AND bash 5 and require them to agree,
#           on a passing tree and on a deliberately broken one. `bash -n` cannot substitute:
#           `declare -A` is syntactically valid under 3.2 and only fails at run time. Verified.
#
#   SCAN  - grep the developer-facing scripts for bash-4-only constructs, so a NEW script cannot
#           reintroduce the problem without being noticed. The RUN layer only covers one file.
#
# Server-side and CI-only scripts are exempt and listed explicitly below with a reason: they run
# on Linux, where bash 4+ is a safe assumption, and forbidding it there would be pointless.

set -uo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT" || exit 1

OLD_BASH_IMAGE="${OLD_BASH_IMAGE:-bash:3.2}"
NEW_BASH_IMAGE="${NEW_BASH_IMAGE:-bash:5}"

PASS=0
FAIL=0
SKIP=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; SKIP=$((SKIP + 1)); }

# Which scripts MUST run on bash 3.2: anything a developer is expected to run locally, which
# in practice means anything the Makefile or a git hook invokes.
#
# DERIVED, not hand-maintained. A hand-written list is one more thing that falls behind the repo
# while still reporting OK, and it also couples unrelated PRs: a branch adding a script would
# have to edit a list living in another branch, and whichever merged second would fail. Deriving
# it means a new script is covered the moment it is wired into `make`.
#
# The corollary is that a script must be make-reachable to be covered. check-required-contexts.sh
# was not, which is why this change also adds a `check-contexts` target -- a pre-push gate with
# no convenient invocation is a gate nobody runs anyway.
EXEMPT_PREFIXES='scripts/common/
scripts/liferay/
tests/e2e/'
# Exempt because each only ever runs where bash 4+ is a given:
#   scripts/common/    -- the gateway VPS (Linux)
#   scripts/liferay/   -- edge nodes (Linux)
#   tests/e2e/         -- inside Linux containers

is_exempt() {
    while IFS= read -r prefix; do
        [ -n "$prefix" ] || continue
        case "$1" in "$prefix"*) return 0 ;; esac
    done <<EXEMPT
$EXEMPT_PREFIXES
EXEMPT
    return 1
}

referenced=$(grep -ohE '(\./)?(scripts|tests)/[A-Za-z0-9_/.-]+\.sh' \
    Makefile scripts/pre-commit-hook.sh scripts/pre-push-hook.sh 2>/dev/null \
    | sed 's|^\./||' | sort -u)

PORTABLE_SCRIPTS=""
while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    [ -f "$ref" ] || continue
    is_exempt "$ref" && continue
    PORTABLE_SCRIPTS="${PORTABLE_SCRIPTS}${ref}
"
done <<REFS
$referenced
REFS

# A derivation that silently finds nothing would report a clean pass over an empty set, which is
# the exact defect this whole file exists to prevent. So assert the floor: the set must be
# non-empty and must contain the script the derivation was extended for.
if [ -z "$PORTABLE_SCRIPTS" ]; then
    echo "ERROR: derived no portable scripts at all -- the derivation is broken, not the repo." >&2
    exit 1
fi
if ! printf '%s\n' "$PORTABLE_SCRIPTS" | grep -qxF 'scripts/check-required-contexts.sh'; then
    echo "ERROR: scripts/check-required-contexts.sh is not in the derived set. It must stay" >&2
    echo "       reachable from the Makefile (target: check-contexts) or this test silently" >&2
    echo "       stops covering the script it was written for." >&2
    exit 1
fi

echo "Derived portable set ($(printf '%s\n' "$PORTABLE_SCRIPTS" | grep -c .) scripts):"
printf '%s\n' "$PORTABLE_SCRIPTS" | grep . | sed 's/^/  /'
echo ""

# ------------------------------------------------------------------------------------------
# SCAN: no bash-4-only constructs in the scripts developers run
# ------------------------------------------------------------------------------------------
echo "Scanning developer-facing scripts for bash 4+ constructs:"

# `local -A` and `declare -A` are the associative-array builtins; mapfile/readarray are bash 4
# builtins; ${var^^}/${var,,} are bash 4 case modification.
BASH4_PATTERN='declare -A|local -A|mapfile|readarray|\$\{[A-Za-z_][A-Za-z_0-9]*\^\^|\$\{[A-Za-z_][A-Za-z_0-9]*,,'

scan_failures=0
while IFS= read -r script; do
    [ -n "$script" ] || continue
    if [ ! -f "$script" ]; then
        fail "SCAN: $script is listed as portable but does not exist -- update this test"
        scan_failures=$((scan_failures + 1))
        continue
    fi
    # Two exclusions, both about mentions rather than uses:
    #   - comment lines, because check-required-contexts.sh legitimately explains in a comment
    #     which constructs it avoids
    #   - the BASH4_PATTERN assignment, because this file is itself in the portable set and its
    #     pattern string necessarily contains every literal it searches for. Excluding the one
    #     line rather than exempting the whole file, so a real bash 4 use here is still caught.
    if grep -vE '^[[:space:]]*#' "$script" | grep -v 'BASH4_PATTERN=' | grep -qE "$BASH4_PATTERN"; then
        fail "SCAN: $script uses a bash 4+ construct but must run on bash 3.2"
        grep -vE '^[[:space:]]*#' "$script" | grep -v 'BASH4_PATTERN=' \
            | grep -nE "$BASH4_PATTERN" | sed 's/^/        /'
        scan_failures=$((scan_failures + 1))
    fi
done <<PORTABLE
$PORTABLE_SCRIPTS
PORTABLE

[ "$scan_failures" -eq 0 ] && pass "SCAN: no bash 4+ constructs in the portable set"

# ------------------------------------------------------------------------------------------
# RUN: execute the gate script under both bash versions and require agreement
# ------------------------------------------------------------------------------------------
echo ""
echo "Running scripts/check-required-contexts.sh under both bash versions:"

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    if [ "${LFT_HOOK_TEST_ALLOW_SKIP:-0}" = "1" ]; then
        skip "RUN: Docker unavailable, skip explicitly allowed"
    else
        fail "RUN: Docker unavailable. bash 3.2 is only reachable through a container here, so
        skipping silently would leave the portability claim untested. Set
        LFT_HOOK_TEST_ALLOW_SKIP=1 to allow the skip deliberately."
    fi
else
    # $1 label, $2 expected exit status, $3 working directory
    run_both() {
        local label="$1" want="$2" dir="$3" rc32 rc5
        docker run --rm -v "$dir":/w -w /w "$OLD_BASH_IMAGE" \
            bash ./scripts/check-required-contexts.sh >/dev/null 2>&1
        rc32=$?
        docker run --rm -v "$dir":/w -w /w "$NEW_BASH_IMAGE" \
            bash ./scripts/check-required-contexts.sh >/dev/null 2>&1
        rc5=$?

        if [ "$rc32" -ne "$rc5" ]; then
            fail "$label: bash 3.2 exited $rc32 but bash 5 exited $rc5 -- they must agree"
            return
        fi
        if [ "$rc32" -ne "$want" ]; then
            fail "$label: both exited $rc32, expected $want"
            return
        fi
        pass "$label (both exited $rc32)"
    }

    run_both "clean tree passes" 0 "$REPO_ROOT"

    # A broken tree must FAIL on both. Without this the test would pass just as happily if the
    # script had been reduced to `echo OK; exit 0` -- and a gate that cannot fail is the exact
    # defect #1380 and #1386 were about.
    #
    # Docker cannot see a system temp dir on macOS (/private/tmp is not shared, and an unshared
    # bind mount presents as an EMPTY directory, which looks like a passing tree). So the fixture
    # goes beside the repo, where the daemon can already see it.
    FIXTURE="$(dirname "$REPO_ROOT")/.lft-portability-fixture-$$"
    rm -rf "$FIXTURE"
    mkdir -p "$FIXTURE/.github/workflows"
    cp scripts/check-required-contexts.sh "$FIXTURE/scripts-tmp" 2>/dev/null
    mkdir -p "$FIXTURE/scripts"
    mv "$FIXTURE/scripts-tmp" "$FIXTURE/scripts/check-required-contexts.sh"
    cp .github/workflows/ci.yml .github/workflows/e2e-sso.yml \
       .github/workflows/issue-link-check.yml "$FIXTURE/.github/workflows/" 2>/dev/null

    mounted=$(docker run --rm -v "$FIXTURE":/probe --entrypoint sh "$NEW_BASH_IMAGE" \
        -c 'ls -a /probe | wc -l' 2>/dev/null)
    if [ "${mounted:-0}" -le 2 ]; then
        fail "RUN: Docker cannot see $FIXTURE (bind mount came through empty)"
    else
        # Break check 4: remove a job from ci-gate's needs.
        grep -v '^      - shellcheck$' "$FIXTURE/.github/workflows/ci.yml" > "$FIXTURE/ci.tmp" \
            && mv "$FIXTURE/ci.tmp" "$FIXTURE/.github/workflows/ci.yml"
        run_both "broken tree fails" 1 "$FIXTURE"
    fi
    rm -rf "$FIXTURE"
fi

echo ""
echo "passed: $PASS  failed: $FAIL  skipped: $SKIP"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
