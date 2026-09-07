#!/usr/bin/env bash
# Static guard against Go toolchain invocations that link or execute unsigned binaries outside
# the EDR whitelist (#1336).
set -euo pipefail

# WHAT THIS IS LOOKING FOR, AND WHY
#
# The Go toolchain writes every executable it links into GOTMPDIR and only then moves it to the
# -o path. GOTMPDIR -- not -o -- therefore decides where an unsigned binary first exists on
# disk, which is all an EDR watching temp directories cares about.
#
# This script previously exempted any line containing "-c -o", on the assumption that -o was the
# control. That exempted the repo's own pre-commit hook, which had GOTMPDIR unset and linked
# into /var/folders on every commit. The exemption was not merely useless: it whitelisted the
# most frequently executed unsafe command in the tree.
#
# A command is treated as guarded when the file establishing it exports GOTMPDIR, sets GOTMPDIR
# on the line itself, or routes through make. -o counts for nothing.
#
# This is the static half. `make edr-guard` is the behavioural half -- it asks the toolchain
# where it will actually link rather than reading source. Both are wanted: grep cannot tell
# whether GOTMPDIR reached a build, and the guard cannot see a command nobody ran.

FAILED=0

# Directories that never execute on the EDR-protected workstation. CI runners and container
# builds are ephemeral and unmonitored, so excluding them keeps the signal about local risk.
EXCLUDES=(
    --exclude-dir=.git
    --exclude-dir=node_modules
    --exclude-dir=ui-dist
    --exclude-dir=.github
    --exclude=check-edr-safety.sh
)

INCLUDES=(
    --include=Makefile
    --include=*.mk
    --include=*.sh
    --include=*.yml
    --include=*.yaml
    --include=*.md
    --include=*.py
    --include=*.cjs
    --include=*.go
)

# Prose describing the rule is not a breach of it, and this repo documents the rule in a lot of
# places. In Markdown, only a bare command line counts -- the shape found inside a fenced block
# that a reader would copy and run. A sentence mentioning `go run` in backticks does not.
is_documentation_prose() {
    local file="$1" code="$2"
    case "$file" in
        *.md)
            # Anything other than a line that *starts* with the command is prose.
            if ! printf '%s' "$code" | grep -qE '^[[:space:]]*(go|GOOS=[^ ]+ go|GOTMPDIR=[^ ]+ go) (test|run)\b'; then
                return 0
            fi
            ;;
    esac
    return 1
}

# A file that exports GOTMPDIR has established the control for the commands it then runs, so its
# own invocations are not flagged line by line. The Makefile is the case this exists for: it
# exports GOTMPDIR at the top and asserts it in edr-guard, which no line-local grep can see.
file_establishes_gotmpdir() {
    grep -qE '^[[:space:]]*(export[[:space:]]+)?GOTMPDIR[[:space:]]*[:?]?=' "$1" 2>/dev/null
}

is_exempt_line() {
    case "$1" in
        *GOTMPDIR*) return 0 ;;
        *make\ test*|*make\ deploy*|*make\ edr-guard*) return 0 ;;
    esac
    return 1
}

# How many files the include/exclude set actually reaches.
#
# scan() below ends its grep with `2>/dev/null || true`, which is correct for "no matches" and
# indistinguishable from "matched nothing because it looked nowhere" (#1779). Measured: run from
# an empty directory this script prints "EDR Safety Check Passed" and exits 0.
#
# That matters more here than in any other gate. This is the guard against the `go test` / `go run`
# pattern that has cost three full environment reinstalls, and a version of it that cannot fail
# is worse than none -- it reads as proof the tree is clean.
corpus_size() {
    # `|| true` is load-bearing. grep exits 1 when nothing matches, and this script runs under
    # `set -euo pipefail` -- so without it the script DIES here on an empty tree instead of
    # reaching the comparison below. That still produced a non-zero exit, but by accident and
    # with no message, which is a worse guard than none: the operator sees a silent failure and
    # no reason for it. Found by mutation-testing this guard and noticing the mutant was killed
    # for the wrong reason.
    grep -rl '' "${INCLUDES[@]}" "${EXCLUDES[@]}" . 2>/dev/null | wc -l | tr -d ' ' || true
}

# A floor, deliberately well below the real figure: this distinguishes "the tree" from "nothing",
# and is not a number anyone should have to maintain as files come and go.
MIN_SCANNED="${LFT_EDR_MIN_FILES:-50}"
SCANNED="$(corpus_size)"
if [ "$SCANNED" -lt "$MIN_SCANNED" ]; then
    echo "EDR SAFETY CHECK FAILED: only $SCANNED files matched the scan set (expected at least $MIN_SCANNED)."
    echo "Nothing was examined, so a pass here would mean nothing. Run this from the repository root."
    exit 1
fi

scan() {
    local pattern="$1" label="$2" advice="$3"
    local hits
    hits="$(grep -rnE "$pattern" "${INCLUDES[@]}" "${EXCLUDES[@]}" . 2>/dev/null || true)"

    [ -n "$hits" ] || return 0

    while IFS= read -r hit; do
        [ -n "$hit" ] || continue
        local file="${hit%%:*}"
        local rest="${hit#*:}"
        local code="${rest#*:}"
        local trimmed
        trimmed="$(printf '%s' "$code" | sed 's/^[[:space:]]*//')"

        # Comments describe; they do not execute.
        case "$trimmed" in
            \#*|//*) continue ;;
        esac

        is_documentation_prose "$file" "$code" && continue
        is_exempt_line "$code" && continue
        file_establishes_gotmpdir "$file" && continue

        if [ "$FAILED" -eq 0 ]; then
            echo "EDR SAFETY CHECK FAILED"
            echo
        fi
        echo "  [$label] $hit"
        echo "      $advice"
        FAILED=1
    done <<< "$hits"
}

echo "Scanning for Go toolchain invocations that link or execute outside the EDR whitelist..."

# go test -c compiles a test binary. Safe only when GOTMPDIR is set, whatever -o says.
scan '(^|[^[:alnum:]_-])go test' \
     'go test' \
     'Use "make test" (it exports GOTMPDIR and asserts it). -o alone does not keep the linked binary out of the default temp dir.'

# go run links AND executes from the work directory, which is strictly worse than go test -c.
scan '(^|[^[:alnum:]_-])go run ' \
     'go run' \
     'Build with "go build -o <path>" and run the built binary. go run executes it from inside GOTMPDIR.'

# The same two commands, but spawned FROM Go source. This needs its own pattern rather than
# riding on the two above: Go never contains the literal "go run". It spells it as separate
# string literals -- exec.Command("go", "run", ...) -- so widening the includes to *.go changes
# nothing on its own. That was the actual gap in #1402: `pkg/ops/sign.go` shelled out to
# `go run scripts/minisign_helper.go` on every `sign`, on the documented local path, and this
# guard could not see it in either dimension.
#
# Matches the argument pair with optional whitespace, which covers exec.Command, exec.CommandContext
# and this repo's RunCommand / RunCommandWithEnv / RunCommandCaptureOutput wrappers alike.
scan '"go"[[:space:]]*,[[:space:]]*"(run|test)"' \
     'go toolchain spawned from Go source' \
     'Do the work in-process, or build to a path and exec that. A subprocess "go run" links and executes inside GOTMPDIR just as a shell one does.'

if [ "$FAILED" -ne 0 ]; then
    echo
    echo "GOTMPDIR is the control, not -o: the toolchain links inside GOTMPDIR and only then"
    echo "moves the result to the -o path. See 'make edr-guard' for the behavioural check."
    exit 1
fi

echo "EDR Safety Check Passed: no unguarded 'go test' or 'go run' invocations found."
