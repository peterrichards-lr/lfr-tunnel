#!/bin/sh
# edr-go-wrapper.sh -- a `go` shim that keeps the toolchain inside the EDR whitelist (#1860).
#
# lfr-tunnel-edr-go-wrapper
#
# GOTMPDIR, not -o, decides where an unsigned binary first exists on disk (#1337). The Makefile
# pins it (`export GOTMPDIR := $(LFT_TEST_DIR)`), but nothing outside make inherits that: a bare
# `go build`, and `lfr-tunnel-ops build` through exec.Command, both linked into /var/folders.
#
# On 2026-09-09 10:55 SentinelOne quarantined this project's freshly built Mach-O binaries and
# took 61 tracked gate scripts, the three .git/hooks shims and two node bin directories with them
# as remediation collateral (#1859). Measured before and after installing this shim, same command:
#
#   before:  WORK=/var/folders/62/.../T/go-build1726362403
#   after:   WORK=/private/tmp/go-build3661106187
#
# Install with `make install-go-guard`, which puts it on PATH ahead of the real toolchain.
#
# Why a PATH shim and not an alias or a rename, all measured rather than assumed:
#   * An alias cannot work. Agent shells are non-interactive so .zshrc is never read, and `make`
#     sets no SHELL, so recipes run under /bin/sh which never reads it either. `ops build` uses
#     exec.Command, which resolves via PATH, not via any shell alias.
#   * Renaming the toolchain silently self-heals: /opt/homebrew/bin/go is a brew symlink, so the
#     next `brew upgrade go` restores it and leaves a guard that has stopped existing while
#     looking identical to one that works. That is why AGENTS.md rejected core.hooksPath.
#   * A PATH shim is honoured by /bin/sh, by make recipes and by exec.Command alike.
#
# It FIXES rather than BLOCKS, because the failure mode was ignorance, not defiance -- and because
# `deploy` -> `make ui-dist` genuinely needs `go build`. Blocking it would break releases.
#
# This is machine-local defence in depth. It is NOT a substitute for the repo-side fix in #1859:
# CI, a fresh clone and anyone else's laptop have no shim.
set -u

MARKER=lfr-tunnel-edr-go-wrapper

# Hard backstop against the shim resolving to itself. The marker scan below needs `head` and
# `grep`; on a PATH that lacks them the scan silently finds nothing to skip, the shim picks
# itself as the toolchain and exec's itself forever. Found by tests/hooks/test-go-guard.sh, which
# hung on exactly that -- one spinning process, since exec replaces rather than forks.
#
# A depth counter rather than a boolean: `go generate` legitimately runs commands that invoke
# `go` again, so a one-shot "already active" flag would break it. Real nesting is shallow;
# recursion is not.
LFT_GO_GUARD_DEPTH=$((${LFT_GO_GUARD_DEPTH:-0} + 1))
export LFT_GO_GUARD_DEPTH
if [ "$LFT_GO_GUARD_DEPTH" -gt 4 ]; then
    echo "go guard: REFUSED -- recursion detected (depth $LFT_GO_GUARD_DEPTH)." >&2
    echo "  The shim resolved to itself. Check PATH, or set LFT_GO_REAL to the real toolchain." >&2
    exit 1
fi

# Same derivation the Makefile uses, and overridable by the same variable, so the two controls
# cannot disagree about where the whitelist is.
if [ -n "${LFT_TEST_DIR:-}" ]; then
    WHITELIST=$LFT_TEST_DIR
elif [ -d /private/tmp ]; then
    WHITELIST=/private/tmp
else
    WHITELIST=/tmp
fi

# Find the real toolchain: the first `go` on PATH that is not one of our own shims. Derived rather
# than hardcoded -- /opt/homebrew/bin/go is Apple Silicon Homebrew only; Intel brew is
# /usr/local/bin/go, Linux is often /usr/local/go/bin/go, and asdf/mise differ again.
# LFT_GO_REAL overrides it, which is also what makes this script testable.
REAL=${LFT_GO_REAL:-}
if [ -z "$REAL" ]; then
    OLDIFS=$IFS
    IFS=:
    for dir in $PATH; do
        [ -n "$dir" ] || continue
        candidate="$dir/go"
        [ -x "$candidate" ] || continue
        # Skip our own shims, however they were installed -- by copy, by symlink, or several
        # times over. A path comparison would miss a symlinked install and recurse forever.
        # Absolute paths on purpose: resolving these through PATH is what let a minimal PATH
        # defeat the check entirely. Fall back to a PATH lookup only if they are absent.
        _head=/usr/bin/head
        _grep=/usr/bin/grep
        [ -x "$_head" ] || _head=head
        [ -x "$_grep" ] || _grep=grep
        if "$_head" -40 "$candidate" 2>/dev/null | "$_grep" -q "$MARKER"; then
            continue
        fi
        REAL=$candidate
        break
    done
    IFS=$OLDIFS
fi

if [ -z "$REAL" ] || [ ! -x "$REAL" ]; then
    echo "go guard: REFUSED -- no real Go toolchain found on PATH behind this shim." >&2
    echo "  Set LFT_GO_REAL to its path, or fix PATH. Do not delete the guard to get moving:" >&2
    echo "  see .agents/skills/edr-constraints/SKILL.md (three incidents, three reinstalls)." >&2
    exit 1
fi

# `go run` and bare `go test` link a binary AND execute it from a temp path under a name nobody
# chose. The whitelist is an exact path, not a directory tree, so pinning GOTMPDIR does not make
# those safe. `go test -c` only compiles -- and Makefile:154 depends on it, so refusing every
# `go test` would break `make test` outright.
subcmd=${1:-}
has_c=no
for arg in "$@"; do
    if [ "$arg" = "-c" ]; then
        has_c=yes
        break
    fi
done

case "$subcmd" in
    run)
        echo "go guard: REFUSED -- 'go run' links and executes outside the whitelist (#1333, #1402)." >&2
        echo "  Build it, then run the binary:  go build -o bin/<name> ./cmd/<name>" >&2
        exit 1
        ;;
    test)
        if [ "$has_c" = no ]; then
            echo "go guard: REFUSED -- bare 'go test' executes an unsigned binary from a temp path." >&2
            echo "  Use 'make test'. It compiles to \$LFT_TEST_DIR and runs it there." >&2
            echo "  'go test -c' is allowed: it compiles without executing." >&2
            exit 1
        fi
        ;;
esac

# Anything that can link gets the whitelist pinned, and says so when it had to change something.
# Fail closed: if the whitelist cannot be ensured, refuse rather than link somewhere unwatched.
case "$subcmd" in
    build|test|install|vet|tool|generate|work)
        if [ ! -d "$WHITELIST" ] && ! mkdir -p "$WHITELIST" 2>/dev/null; then
            echo "go guard: REFUSED -- cannot ensure $WHITELIST; refusing to link outside it." >&2
            exit 1
        fi
        if [ "${GOTMPDIR:-}" != "$WHITELIST" ]; then
            echo "go guard: pinning GOTMPDIR=$WHITELIST (was '${GOTMPDIR:-unset}')" >&2
        fi
        ;;
esac

GOTMPDIR=$WHITELIST
export GOTMPDIR
exec "$REAL" "$@"
