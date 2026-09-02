#!/usr/bin/env bash
# test-install-script.sh — the installer must download from the gateway that served it (#1684)
#
# The guard was written as `[ "$SERVER_URL" = "{{SERVER_URL}}" ]` to detect an unsubstituted
# placeholder. The gateway substitutes with strings.ReplaceAll, which replaced the right-hand
# side too -- so both sides held the gateway URL, the guard was always true, and every install
# fell through to a hardcoded apex that 404s. A user had to sed the script by hand to install.
#
# Reading the template does not reveal this: it only appears once the substitution has happened.
# So this renders the script the way the gateway does and RUNS the guard.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE="${REPO_ROOT}/pkg/server/static/install.sh"
PS1_TEMPLATE="${REPO_ROOT}/pkg/server/static/install.ps1"

[ -f "$TEMPLATE" ] || { echo "FATAL: $TEMPLATE missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the install script's server URL handling..."

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Render exactly as the gateway does: replace EVERY occurrence, which is the behaviour that
# broke the old guard.
GATEWAY="https://gw.example.test"
# sed, not python3: this runs in whatever image CI provides, and a test that needs an
# interpreter it was not promised fails for a reason unrelated to the thing under test.
# `|` as the delimiter because the replacement is a URL.
sed "s|{{SERVER_URL}}|${GATEWAY}|g" "$TEMPLATE" > "$WORK/rendered.sh"

# Run only the prologue -- the part that decides SERVER_URL -- and print what it chose. Running
# the whole script would try to download and install.
sed -n '1,/^BINARY=/p' "$WORK/rendered.sh" | sed 's/^BINARY=.*//' > "$WORK/prologue.sh"
echo 'echo "CHOSE=$SERVER_URL"' >> "$WORK/prologue.sh"

out=$(sh "$WORK/prologue.sh" 2>&1)
rc=$?

if [ "$rc" -ne 0 ]; then
  fail "the rendered script exited $rc before choosing a URL: $out"
elif echo "$out" | grep -q "CHOSE=$GATEWAY"; then
  pass "a substituted script downloads from the gateway that served it"
else
  fail "the substituted script chose the wrong URL: $out"
fi

# The specific regression: it must not silently rewrite to some other host.
if echo "$out" | grep -qi "lfr-demo.se"; then
  fail "the rendered script fell back to a hardcoded host -- this is #1684"
else
  pass "no hardcoded host is substituted in"
fi

# An UNRENDERED template must refuse, not guess. This is the case the guard exists for, and it
# has to keep working now that it matches on braces instead.
cp "$TEMPLATE" "$WORK/raw.sh"
sed -n '1,/^BINARY=/p' "$WORK/raw.sh" | sed 's/^BINARY=.*//' > "$WORK/rawprologue.sh"
echo 'echo "CHOSE=$SERVER_URL"' >> "$WORK/rawprologue.sh"
raw_out=$(sh "$WORK/rawprologue.sh" 2>&1)
raw_rc=$?

if [ "$raw_rc" -ne 0 ]; then
  pass "an unsubstituted template refuses to run (exit $raw_rc)"
else
  fail "an unsubstituted template ran anyway and chose: $raw_out"
fi
if echo "$raw_out" | grep -q "your-gateway"; then
  pass "the refusal tells the user how to fetch a working script"
else
  fail "the refusal does not say what to do instead: $raw_out"
fi

# No hardcoded deployment hostname anywhere in either template. This is the root cause the
# fallback embodied, and it is easy to reintroduce while "fixing" something else.
for f in "$TEMPLATE" "$PS1_TEMPLATE"; do
  name="$(basename "$f")"
  [ -f "$f" ] || continue
  # Comments explain the history and may mention the shape; only real assignments matter.
  if grep -vE '^\s*(#|\s*$)' "$f" | grep -qiE '(SERVER_URL|ServerUrl)\s*=\s*"https://[a-z0-9.-]+"'; then
    fail "$name assigns a hardcoded server URL -- a deployment's hostname must not live in shared source"
  else
    pass "$name has no hardcoded server URL"
  fi
done

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
