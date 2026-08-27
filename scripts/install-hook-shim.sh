#!/usr/bin/env bash
# install-hook-shim.sh — install git hooks that EXEC the repo scripts instead of copying them
#
# `make install-hook` used to copy scripts/pre-commit-hook.sh into .git/hooks/pre-commit, so every
# edit was inert until each person re-ran it. Silently, and in the safe-looking direction: commits
# succeeded, and a stale hook's output is indistinguishable from a current one unless you know
# which checks should be listed. The attribution guard added in #1384 ran for nobody who had not
# reinstalled since (#1425).
#
# The shim is two lines that exec the script in the working tree, so an edit takes effect on the
# next commit and there is nothing left to keep in sync.
#
# WHY NOT core.hooksPath, which #1425 recommended and git provides for exactly this: it is
# resolved against the working tree, so on a branch that predates the hooks directory git finds
# nothing, runs NO hooks, and says NOTHING. Verified: a commit with core.hooksPath pointing at a
# missing directory succeeds silently, with no warning. For a repo where older branches are
# checked out routinely that trades a stale secret scan for no secret scan, which is worse. The
# shim lives in .git/hooks, which is per-clone and survives branch switches, so it can notice the
# script is missing and say so.
set -euo pipefail

HOOKS_DIR=$(git rev-parse --path-format=absolute --git-path hooks)
mkdir -p "$HOOKS_DIR"

install_shim() {
    local hook="$1" script="$2"
    cat > "$HOOKS_DIR/$hook" <<SHIM
#!/bin/sh
# Installed by 'make install-hook'. Do not edit: this execs the script in the working tree, so
# the checks live in $script and edits take effect immediately (#1425).
TOP=\$(git rev-parse --show-toplevel) || exit 0
SCRIPT="\$TOP/$script"
if [ ! -x "\$SCRIPT" ]; then
    # Loud, and not fatal. A branch cut before this script existed must still be committable --
    # but silence here would be the exact failure this shim was written to remove.
    echo "WARNING: \$SCRIPT not found on this branch, so NO $hook checks ran." >&2
    exit 0
fi
exec "\$SCRIPT" "\$@"
SHIM
    chmod +x "$HOOKS_DIR/$hook"
    echo "  $hook -> $script"
}

# core.hooksPath overrides .git/hooks entirely, so a value left over from an earlier attempt would
# shadow what is installed here and hooks would appear installed while never running.
if existing=$(git config --get core.hooksPath 2>/dev/null) && [ -n "$existing" ]; then
    git config --unset core.hooksPath
    echo "  removed core.hooksPath=$existing, which would have shadowed these hooks"
fi

echo "Installing hook shims into $HOOKS_DIR..."
install_shim pre-commit scripts/pre-commit-hook.sh
install_shim pre-push scripts/pre-push-hook.sh
echo "Hooks now exec the scripts in the working tree -- editing one takes effect immediately."
