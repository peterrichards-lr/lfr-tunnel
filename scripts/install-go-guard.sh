#!/bin/sh
# install-go-guard.sh -- install scripts/edr-go-wrapper.sh as `go` on PATH (#1860).
#
# Refuses rather than installs a guard that would not actually be reached: a shim sitting behind
# the real toolchain on PATH is indistinguishable from no shim at all, which is the failure mode
# AGENTS.md rejected core.hooksPath for.
set -u

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC="$REPO_ROOT/scripts/edr-go-wrapper.sh"
DEST_DIR=${LFT_GO_GUARD_DIR:-$HOME/.local/bin}
DEST="$DEST_DIR/go"

[ -f "$SRC" ] || { echo "install-go-guard: $SRC is missing." >&2; exit 1; }

mkdir -p "$DEST_DIR" || { echo "install-go-guard: cannot create $DEST_DIR." >&2; exit 1; }

# Where does the real toolchain live, and would our shim come first? Compare PATH positions
# rather than trusting that a conventional directory is early.
real_pos=0
dest_pos=0
pos=0
OLDIFS=$IFS
IFS=:
for dir in $PATH; do
    pos=$((pos + 1))
    [ -n "$dir" ] || continue
    if [ "$dir" = "$DEST_DIR" ] && [ "$dest_pos" -eq 0 ]; then
        dest_pos=$pos
    fi
    if [ -x "$dir/go" ] && [ "$real_pos" -eq 0 ] &&
        ! head -40 "$dir/go" 2>/dev/null | grep -q lfr-tunnel-edr-go-wrapper; then
        real_pos=$pos
    fi
done
IFS=$OLDIFS

if [ "$dest_pos" -eq 0 ]; then
    echo "install-go-guard: REFUSED -- $DEST_DIR is not on PATH, so the shim would never run." >&2
    echo "  Add it to PATH, or set LFT_GO_GUARD_DIR to a directory that is on it." >&2
    exit 1
fi
if [ "$real_pos" -ne 0 ] && [ "$dest_pos" -gt "$real_pos" ]; then
    echo "install-go-guard: REFUSED -- $DEST_DIR is position $dest_pos on PATH but the real" >&2
    echo "  toolchain is at position $real_pos, so the shim would be shadowed and silently" >&2
    echo "  guard nothing. Move $DEST_DIR earlier on PATH, then re-run." >&2
    exit 1
fi

cp "$SRC" "$DEST" && chmod +x "$DEST" || {
    echo "install-go-guard: failed to write $DEST." >&2
    exit 1
}

echo "Installed the go EDR guard:"
echo "  $DEST  (position $dest_pos on PATH; real toolchain at $real_pos)"
echo ""
echo "Verify with:"
echo "  go env GOTMPDIR      # expect the whitelist, not empty"
echo "  go build -work -o /tmp/probe ./cmd/lfr-tunnel-ops   # expect WORK= inside the whitelist"
echo ""
echo "This is machine-local defence in depth. CI and a fresh clone have no shim -- the durable"
echo "fix is #1859, which makes the toolchain safe without one."
