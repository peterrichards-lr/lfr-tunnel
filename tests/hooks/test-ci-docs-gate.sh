#!/usr/bin/env bash
# test-ci-docs-gate.sh — the documentation build must run on PRs that touch docs (#1725)
#
# `Deploy Documentation` (.github/workflows/docs.yml) runs only on a push to master and is not a
# required context. So when #1712 added a link mkdocs could not resolve, the PR merged green and
# every docs deploy from 27 August to 3 September failed somewhere nobody looks -- a week with no
# published documentation and no signal to anyone. #1722 fixed the link; this gate is what stops
# the next one costing another week.
#
# Asserted rather than observed, for the same reason as test-ci-hook-gate.sh: the failure mode is
# a job that never runs. There is no runtime symptom to catch -- a filter that silently evaluates
# false looks exactly like a tree with no documentation changes, and it fails in the
# green-looking direction.
#
# Kept to bash 3.2 (see AGENTS.md): no associative arrays, no mapfile, no ${var^^}.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CI="${REPO_ROOT}/.github/workflows/ci.yml"
DOCS_WF="${REPO_ROOT}/.github/workflows/docs.yml"

[ -f "$CI" ] || { echo "FATAL: $CI missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the Documentation Build CI gate..."

# 1. The job exists and emits the context name a reviewer will look for.
if grep -qF 'name: Documentation Build' "$CI"; then
  pass "ci.yml has a Documentation Build job"
else
  fail "no Documentation Build job in ci.yml -- a docs break would again only surface on master"
fi

# 2. It has to be gated on the docs filter, and on nothing broader that would make it cosmetic.
if grep -qE "if: needs\.changes\.outputs\.docs == 'true'" "$CI"; then
  pass "docs-build is gated on the docs filter"
else
  fail "docs-build is not gated on 'docs'"
fi

# 3. The build command must be the one the deploy actually runs. A PR check that approximates
#    the real build can pass while the real build fails, which is the entire defect being fixed
#    here rather than a smaller version of it. --strict is the load-bearing half: without it
#    mkdocs reports an unresolvable link as a warning and still exits 0.
CI_CMD=$(grep -F 'mkdocs build' "$CI" | sed 's/^ *//;s/^run: *//' | head -1)
if [ -z "$CI_CMD" ]; then
  fail "no mkdocs build command in ci.yml"
else
  case "$CI_CMD" in
    *--strict*) pass "the CI build runs mkdocs --strict" ;;
    *) fail "the CI build does not pass --strict -- a broken link would warn and still exit 0" ;;
  esac

  if [ -f "$DOCS_WF" ]; then
    DEPLOY_CMD=$(grep -F 'mkdocs build' "$DOCS_WF" | sed 's/^ *//;s/^run: *//' | head -1)
    if [ "$CI_CMD" = "$DEPLOY_CMD" ]; then
      pass "the PR build and the deploy build run the identical command"
    else
      fail "PR build '$CI_CMD' differs from deploy build '$DEPLOY_CMD' -- the PR check can pass while the deploy fails"
    fi
  fi
fi

# 4. The filter has to match what mkdocs actually reads, or the gate above never fires.
#    mkdocs.yml matters on its own: `nav:` names files, so a rename there breaks the build with
#    no docs/ change at all.
DOCS_PATTERN=$(grep -B 2 'DOCS=true' "$CI" | grep -oE "grep -qE '[^']+'" | head -1 | sed "s/grep -qE '//;s/'$//")
if [ -z "$DOCS_PATTERN" ]; then
  fail "could not find the docs filter pattern in $CI"
else
  for path in "docs/README.md" "docs/server/edge_setup_guide.md" "mkdocs.yml" "requirements-dev.txt"; do
    if echo "$path" | grep -qE "$DOCS_PATTERN"; then
      pass "docs filter matches '$path'"
    else
      fail "docs filter does NOT match '$path' -- a change there would skip the docs build"
    fi
  done

  # A path it must NOT match, or the filter is "always true" and proves nothing.
  if echo "pkg/server/proxy.go" | grep -qE "$DOCS_PATTERN"; then
    fail "docs filter matches pkg/server/proxy.go -- it is too broad to mean anything"
  else
    pass "docs filter ignores unrelated paths"
  fi
fi

# 5. Declared and emitted. Miss either and the gate reads an empty string, is always false, and
#    the job never runs -- silently.
if grep -qE '^\s+docs: \$\{\{ steps\.filter\.outputs\.docs \}\}' "$CI"; then
  pass "the changes job declares a docs output"
else
  fail "the changes job does not declare 'docs' -- the gate would always be false"
fi

if grep -qE 'echo "docs=\$DOCS"' "$CI"; then
  pass "the docs output is written to GITHUB_OUTPUT"
else
  fail "docs is never written to GITHUB_OUTPUT -- the gate would always be false"
fi

# 6. Fail open. A force-push or an unresolvable diff base must still build the docs, not skip
#    them on exactly the runs where least is known about what changed.
if sed -n '/run_everything() {/,/^          }/p' "$CI" | grep -q 'docs=true'; then
  pass "run_everything sets docs=true"
else
  fail "run_everything does not set docs=true -- an unresolvable diff would skip the docs build"
fi

# 7. In ci-gate's needs:. A job outside the gate is ungated while the gate still reports green,
#    which would leave this whole file guarding nothing. check-required-contexts.sh enforces it
#    too; asserted here as well so `make test-hooks` fails on its own.
GATE_NEEDS=$(awk '
    /^  ci-gate:/ { in_gate = 1; next }
    in_gate && /^  [a-zA-Z0-9_-]+:/ { exit }
    in_gate && /^    needs:/ { in_needs = 1; next }
    in_needs && /^      - / { sub(/^      - /, ""); print; next }
    in_needs && /^    [a-zA-Z]/ { in_needs = 0 }
' "$CI")
if printf '%s\n' "$GATE_NEEDS" | grep -qxF 'docs-build'; then
  pass "docs-build is in ci-gate's needs:"
else
  fail "docs-build is missing from ci-gate's needs: -- it would be ungated while CI Gate reports green"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
