#!/usr/bin/env bash
# test-hook-shim.sh — tests for scripts/install-hook-shim.sh (#1425)
#
# The property under test is that an edit to a hook SCRIPT takes effect without reinstalling.
# That is the whole point: hooks used to be copied, so every change was inert until each person
# re-ran `make install-hook` -- silently, and in the safe-looking direction, since a stale hook's
# output is indistinguishable from a current one.
#
# Also tested: a branch with no hook script must WARN, not go quiet. core.hooksPath was the
# obvious implementation and was rejected for exactly this -- pointed at a directory that does not
# exist on the current branch, git runs no hooks and says nothing, which trades a stale secret
# scan for no secret scan.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
INSTALLER="${REPO_ROOT}/scripts/install-hook-shim.sh"

[ -x "$INSTALLER" ] || { echo "FATAL: $INSTALLER missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# A throwaway repo, so nothing here can touch the real one.
REPO="$WORK/repo"
mkdir -p "$REPO/scripts"
cd "$REPO" || exit 1
git init -q .
git config user.email t@example.com
git config user.name t

printf '#!/usr/bin/env bash\necho "MARKER-ONE"\nexit 0\n' > scripts/pre-commit-hook.sh
printf '#!/usr/bin/env bash\nexit 0\n' > scripts/pre-push-hook.sh
chmod +x scripts/pre-commit-hook.sh scripts/pre-push-hook.sh

"$INSTALLER" >/dev/null 2>&1

echo "Testing scripts/install-hook-shim.sh"
echo ""

commit_output() {
    echo "$1" > "file-$1.txt"
    git add -A >/dev/null 2>&1
    git commit -m "$1" 2>&1
}

# Assertions capture output into a variable before grepping it, never `cmd | grep -q`. Under
# `set -o pipefail` grep exits on the first match, the closed pipe SIGPIPEs the command upstream,
# and the pipeline reports failure even though the match was found -- which is exactly how the
# first version of this file reported four failures against a shim that worked perfectly.

# 1. The shim runs the script at all.
OUT=$(commit_output one)
if printf '%s' "$OUT" | grep -q "MARKER-ONE"; then
    pass "the installed shim runs the repo's hook script"
else
    fail "the shim did not run the hook script"
fi

# 2. THE POINT: edit the script, do NOT reinstall, and the change must take effect.
printf '#!/usr/bin/env bash\necho "MARKER-TWO"\nexit 0\n' > scripts/pre-commit-hook.sh
chmod +x scripts/pre-commit-hook.sh
OUT=$(commit_output two)
if printf '%s' "$OUT" | grep -q "MARKER-TWO"; then
    pass "editing the hook script takes effect WITHOUT reinstalling"
else
    fail "the edit did not take effect -- the drift this fixes is back"
fi

# 3. A hook that fails must still block the commit.
printf '#!/usr/bin/env bash\necho "BLOCKING"\nexit 1\n' > scripts/pre-commit-hook.sh
chmod +x scripts/pre-commit-hook.sh
echo three > file-three.txt
git add -A >/dev/null 2>&1
if git commit -m three >/dev/null 2>&1; then
    fail "a failing hook did not block the commit"
else
    pass "a failing hook still blocks the commit"
fi

# 4. No script on this branch: warn loudly, and do not make the branch uncommittable.
rm -f scripts/pre-commit-hook.sh
OUT=$(commit_output four)
if printf '%s' "$OUT" | grep -q "NO pre-commit checks ran"; then
    pass "a missing hook script warns loudly rather than going silent"
else
    fail "a missing hook script was silent: $OUT"
fi
LOG=$(git log --oneline)
if printf '%s' "$LOG" | grep -q four; then
    pass "a branch without the hook script is still committable"
else
    fail "a missing hook script made the branch uncommittable"
fi

# 5. A leftover core.hooksPath would shadow .git/hooks entirely, so the installer must clear it --
#    otherwise hooks look installed and never run.
printf '#!/usr/bin/env bash\necho "MARKER-FIVE"\nexit 0\n' > scripts/pre-commit-hook.sh
chmod +x scripts/pre-commit-hook.sh
git config core.hooksPath scripts/nonexistent-hooks
"$INSTALLER" >/dev/null 2>&1
if [ -z "$(git config --get core.hooksPath || true)" ]; then
    pass "a leftover core.hooksPath is removed, so the shims are not shadowed"
else
    fail "core.hooksPath survived the install and would shadow the shims"
fi
OUT=$(commit_output five)
if printf '%s' "$OUT" | grep -q "MARKER-FIVE"; then
    pass "hooks run again after the shadowing config is cleared"
else
    fail "hooks did not run after clearing core.hooksPath"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
