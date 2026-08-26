#!/bin/bash
#
# Tests scripts/check-edr-safety.sh (#1402).
#
# The guard forbids Go toolchain invocations that link or execute outside the EDR whitelist. It
# had two blind spots at once, and only fixing both helps:
#
#   1. It did not scan *.go at all.
#   2. Its patterns are the literal "go run" / "go test". Go source never contains those --
#      it spells them as separate string literals, RunCommand("go", "run", ...). So adding
#      *.go to the includes changes NOTHING on its own. Measured:
#
#        --include=*.go alone,     against the real defect -> rc=0  (passes, useless)
#        includes + Go-exec pattern                        -> rc=1  (caught)
#
# That second point is why this test exists rather than a note in the commit message: the
# obvious fix looks sufficient and is not, so the next person to touch this needs a check that
# fails, not a claim.
#
# Fixtures are built beside the repo rather than in a system temp dir: the guard greps `.`
# recursively from its working directory, so a fixture inside the repo would be picked up by the
# real run, and /private/tmp is not visible to Docker on macOS (a lesson from #1377) -- keeping
# every fixture in one predictable place avoids relearning that per test.

set -uo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT" || exit 1

GUARD="scripts/check-edr-safety.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

FIXTURE_BASE="$(dirname "$REPO_ROOT")/.lft-edr-guard-fixture-$$"
cleanup() { rm -rf "$FIXTURE_BASE"; }
trap cleanup EXIT

# $1 label, $2 expected exit status, $3 file name to plant, $4 file contents
run_case() {
    local label="$1" want="$2" name="$3" body="$4" dir got
    dir="$FIXTURE_BASE/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/scripts"
    cp "$GUARD" "$dir/scripts/check-edr-safety.sh"
    printf '%s\n' "$body" > "$dir/$name"

    ( cd "$dir" && ./scripts/check-edr-safety.sh >/dev/null 2>&1 )
    got=$?

    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && ./scripts/check-edr-safety.sh 2>&1 | sed 's/^/        /' )
    fi
}

echo "EDR guard cases:"

# The defect from #1402, in the form it actually appeared in pkg/ops/sign.go.
run_case "go run spawned from Go source is caught" 1 "spawn.go" \
'package main

import "os/exec"

func main() {
	_ = exec.Command("go", "run", "scripts/helper.go", "a", "b").Run()
}'

# The same shape via this repo'"'"'s own wrapper, which is how it was really written.
run_case "RunCommand(\"go\", \"run\", ...) is caught" 1 "wrapper.go" \
'package ops

func sign() {
	err = RunCommand("go", "run", minisignHelper, checksumsPath, checksumsPath+".minisig")
	_ = err
}'

# go test spawned the same way.
run_case "go test spawned from Go source is caught" 1 "spawntest.go" \
'package main

import "os/exec"

func main() {
	_ = exec.Command("go", "test", "./...").Run()
}'

# A comment is a description, not an invocation -- the guard already skips these, and it must
# keep doing so or every file documenting the rule becomes a failure.
run_case "a comment mentioning it is not flagged" 0 "comment.go" \
'package main

// Never use exec.Command("go", "run", ...) here -- see CLAUDE.md.
func main() {}'

# Ordinary Go with no toolchain invocation.
run_case "clean Go source passes" 0 "clean.go" \
'package main

import "fmt"

func main() { fmt.Println("hello") }'

# A shell script invocation must still be caught: the Go pattern is additive, and quietly
# losing the original coverage while adding new coverage would be the worse outcome.
run_case "shell go run is still caught" 1 "build.sh" \
'#!/bin/bash
go run ./cmd/thing'

# And a shell invocation that sets GOTMPDIR on the line is still exempt.
run_case "shell go run with GOTMPDIR is still exempt" 0 "guarded.sh" \
'#!/bin/bash
GOTMPDIR=/private/tmp go run ./cmd/thing'

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
