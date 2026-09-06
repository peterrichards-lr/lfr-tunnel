#!/usr/bin/env bash
# test-ci-platform-matrix.sh — the macOS/Windows matrix must run for the code that ships there
# (#1773)
#
# `platform_sensitive` in ci.yml decides whether a pull request runs Test Suite on macos-latest
# and windows-latest, or only on ubuntu-latest. It listed pkg/client and pkg/gui as "the code
# that actually ships to all three" and stopped there, which left out pkg/config -- so #1769
# added ~ expansion, runtime.GOOS branching and CRLF handling to it and both non-Linux legs
# reported SUCCESS in five seconds having run no test. The Windows break was found on master,
# and took #1775 plus a follow-up to clear.
#
# Asserted rather than observed, for the same reason as test-ci-hook-gate.sh and
# test-ci-docs-gate.sh: the failure mode is a job that never runs. There is nothing to catch at
# runtime -- a filter that quietly evaluates false is indistinguishable from a tree with no
# platform-sensitive changes, and it fails green.
#
# The fix cannot demonstrate itself, either. Editing ci.yml matches `^\.github/workflows/`, so
# any PR that widens this pattern sets platform_sensitive=true for itself and runs the full
# matrix whatever the pattern says. A green matrix on that PR proves nothing. This file is the
# proof instead.
#
# Kept to bash 3.2 (see AGENTS.md): no associative arrays, no mapfile, no ${var^^}.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CI="${REPO_ROOT}/.github/workflows/ci.yml"

[ -f "$CI" ] || { echo "FATAL: $CI missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the platform matrix (platform_sensitive) CI filter..."

# The pattern itself. Taken from the line that sets PLATFORM_SENSITIVE=true so this reads the
# filter CI actually applies, not a copy of it kept here that could drift into agreeing with
# itself while disagreeing with ci.yml.
PATTERN=$(grep -B 1 'PLATFORM_SENSITIVE=true' "$CI" | grep -oE "grep -qE '[^']+'" | head -1 | sed "s/grep -qE '//;s/'$//")
if [ -z "$PATTERN" ]; then
  fail "could not find the platform_sensitive filter pattern in $CI"
  echo
  echo "  ${PASS} passed, ${FAIL} failed"
  exit 1
fi
pass "found the platform_sensitive pattern"

# 1. Paths that MUST match. pkg/config first: it is the regression this file exists for.
#    version.go is named as well as config.go because a release bump touches only version.go,
#    and that file is compiled into all three binaries like any other.
MUST_MATCH="
pkg/config/config.go
pkg/config/version.go
pkg/config/metadata.go
cmd/lfr-tunnel/main.go
pkg/client/client.go
pkg/gui/gui.go
pkg/mcp/server.go
pkg/osutil/exec_unix.go
pkg/proxyutil/forwarded.go
cmd/lfr-tunneld/reload_windows.go
cmd/lfr-tunneld/reload_unix.go
Makefile
go.mod
go.sum
.github/workflows/ci.yml
scripts/check-required-contexts.sh
"
for path in $MUST_MATCH; do
  if echo "$path" | grep -qE "$PATTERN"; then
    pass "matches '$path'"
  else
    fail "does NOT match '$path' -- a change there would skip macOS and Windows entirely"
  fi
done

# 2. Paths that must NOT match, or the filter is "always true" and gates nothing. pkg/server is
#    here as a boundary, not as an accident: it is the Linux-only gateway and the busiest tree in
#    the repo, and listing it would put the full matrix on nearly every PR. If a later change
#    decides that trade differently, this line is where to say so out loud.
MUST_NOT_MATCH="
pkg/server/api.go
pkg/db/store.go
pkg/ops/deploy.go
docs/README.md
README.md
ui/src/App.tsx
"
for path in $MUST_NOT_MATCH; do
  if echo "$path" | grep -qE "$PATTERN"; then
    fail "matches '$path' -- the filter is too broad to mean anything"
  else
    pass "ignores '$path'"
  fi
done

# 3. cmd/lfr-tunnel must not swallow its neighbours. `^cmd/lfr-tunnel` without the trailing
#    slash would also match cmd/lfr-tunneld and cmd/lfr-tunnel-ops, and the difference is one
#    character.
if echo "cmd/lfr-tunneld/main.go" | grep -qE "$PATTERN"; then
  fail "cmd/lfr-tunneld/main.go matches -- the cmd/lfr-tunnel rule is missing its trailing slash"
else
  pass "cmd/lfr-tunnel does not swallow cmd/lfr-tunneld"
fi

# 4. The derived half, and the reason this test does not go stale. The enumeration in ci.yml was
#    written by eye once and was wrong; re-deriving it means a package joining the client binary
#    cannot join it silently. `go list -deps` is the same view the compiler has.
#
#    Per-OS-suffixed files are skipped when picking a representative, so a package cannot pass
#    on the strength of the `_windows.go` rule while its ordinary files stay unmatched. _test.go
#    files are skipped too: the representative should be a file the shipped binary is built from.
if ! command -v go >/dev/null 2>&1; then
  fail "go is not on PATH -- cannot re-derive the client's package closure"
else
  DEPS=$(cd "$REPO_ROOT" && go list -deps ./cmd/lfr-tunnel 2>/dev/null | grep '^lfr-tunnel/')
  DEP_COUNT=$(printf '%s\n' "$DEPS" | grep -c . )
  if [ "$DEP_COUNT" -lt 2 ]; then
    fail "go list -deps ./cmd/lfr-tunnel returned $DEP_COUNT packages -- derivation is broken, not clean"
  else
    pass "derived $DEP_COUNT in-repo packages from the client binary"
    for pkg in $DEPS; do
      dir="${pkg#lfr-tunnel/}"
      rep=""
      for f in "$REPO_ROOT/$dir"/*.go; do
        [ -f "$f" ] || continue
        base=$(basename "$f")
        case "$base" in
          *_windows.go | *_darwin.go | *_linux.go | *_unix.go | *_test.go) continue ;;
        esac
        rep="$dir/$base"
        break
      done
      if [ -z "$rep" ]; then
        # Every file in the package is per-OS by name, so the suffix rule already covers it.
        pass "$dir is per-OS by construction (no OS-neutral file to check)"
      elif echo "$rep" | grep -qE "$PATTERN"; then
        pass "$dir is covered (via $rep)"
      else
        fail "$dir is built into the client binary but platform_sensitive does not match '$rep' -- add ^$dir/ to the pattern in $CI"
      fi
    done
  fi
fi

# 5. Fail open. A force-push or an unresolvable diff base must run the full matrix, not skip it
#    on exactly the runs where least is known about what changed.
if sed -n '/run_everything() {/,/^          }/p' "$CI" | grep -q 'platform_sensitive=true'; then
  pass "run_everything sets platform_sensitive=true"
else
  fail "run_everything does not set platform_sensitive=true -- an unresolvable diff would skip macOS and Windows"
fi

# 6. Non-PR events always get the full matrix. This is what keeps the gap pre-merge rather than
#    letting an unproven platform regression reach a release.
if grep -qE 'if \[ "\$\{\{ github\.event_name \}\}" != "pull_request" \] \|\| echo "\$CHANGED" \| grep -qE' "$CI"; then
  pass "non-pull_request events bypass the filter and run everything"
else
  fail "the != pull_request escape hatch is gone -- a master push or tag could now skip the matrix"
fi

# 7. Declared, emitted, and actually consumed. Miss any one and the gate reads an empty string,
#    is always false, and the non-Linux legs no-op -- silently, in the green direction.
if grep -qE '^\s+platform_sensitive: \$\{\{ steps\.filter\.outputs\.platform_sensitive \}\}' "$CI"; then
  pass "the changes job declares a platform_sensitive output"
else
  fail "the changes job does not declare 'platform_sensitive' -- the gate would always be false"
fi

if grep -qE 'echo "platform_sensitive=\$PLATFORM_SENSITIVE"' "$CI"; then
  pass "platform_sensitive is written to GITHUB_OUTPUT"
else
  fail "platform_sensitive is never written to GITHUB_OUTPUT -- the gate would always be false"
fi

if grep -qE "needs\.changes\.outputs\.platform_sensitive == 'true'" "$CI"; then
  pass "the Test Suite matrix consumes platform_sensitive"
else
  fail "nothing reads needs.changes.outputs.platform_sensitive -- the filter is computed and discarded"
fi

# 8. It must stay a STEP-level condition on the matrix job. A job-level `if:` on a matrix job
#    stops the matrix expanding, the per-OS required contexts are never created, and the PR
#    blocks forever (#1380).
if awk '/^  test:/ { in_test = 1 } in_test && /^    if:/ { print; exit }' "$CI" | grep -q 'platform_sensitive'; then
  fail "platform_sensitive is a job-level if: on the matrix job -- #1380: the contexts never report and the PR cannot merge"
else
  pass "platform_sensitive gates steps, not the matrix job itself"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
