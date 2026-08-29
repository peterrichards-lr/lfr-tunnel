#!/usr/bin/env bash
# test-compile-check.sh — tests for the compile-check step in `make test` (#1556)
#
# `make test` iterates `go list -f '{{if .TestGoFiles}}...'`, so a package with no _test.go files
# never reaches `go test -c` and its compile errors are never surfaced. Before this step existed,
# `make test` exited 0 and printed PASS on a tree where `go build ./...` exited 1.
#
# The interesting property is not that a compile error fails -- it is that it fails for a package
# NOBODY TESTS. A check that only covers tested packages looks identical on a good day.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MAKEFILE="${REPO_ROOT}/Makefile"

[ -f "$MAKEFILE" ] || { echo "FATAL: $MAKEFILE missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the compile-check step..."

# 1. The wiring. Asserted on the Makefile rather than by running it, because running the real
#    suite here would take minutes and this only needs to know the dependency exists.
if grep -qE '^test: .*compile-check' "$MAKEFILE"; then
  pass "make test depends on compile-check"
else
  fail "make test no longer depends on compile-check -- an untested package can stop compiling unnoticed"
fi

if grep -qE '^compile-check: .*edr-guard' "$MAKEFILE"; then
  pass "compile-check runs behind edr-guard"
else
  fail "compile-check must depend on edr-guard: go build links inside GOTMPDIR"
fi

if grep -A4 '^compile-check:' "$MAKEFILE" | grep -qE 'go build \./\.\.\.'; then
  pass "compile-check builds every package"
else
  fail "compile-check no longer runs 'go build ./...'"
fi

# 2. The behaviour the wiring is for, proved against a throwaway module rather than by breaking
#    the real tree: does `go build ./...` actually report a package that has no tests?
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

mkdir -p "$WORK/tested" "$WORK/untested"
cat > "$WORK/go.mod" <<'MOD'
module compilecheckprobe

go 1.22
MOD
cat > "$WORK/tested/tested.go" <<'GO'
package tested

func Add(a, b int) int { return a + b }
GO
cat > "$WORK/tested/tested_test.go" <<'GO'
package tested

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
GO
cat > "$WORK/untested/untested.go" <<'GO'
package untested

func Broken() int { return noSuchSymbol() }
GO

listed=$(cd "$WORK" && go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | grep -c . )
if [ "$listed" = "1" ]; then
  pass "the test loop's package list skips the untested package (which is why the gap existed)"
else
  fail "expected exactly 1 package with tests in the probe module, got $listed"
fi

(cd "$WORK" && go build ./... >/dev/null 2>&1)
if [ $? -ne 0 ]; then
  pass "go build ./... reports the untested package that does not compile"
else
  fail "go build ./... did not fail on a package that does not compile"
fi

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
