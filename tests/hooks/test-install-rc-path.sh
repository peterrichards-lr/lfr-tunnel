#!/usr/bin/env bash
# test-install-rc-path.sh — install.sh puts the client on PATH on macOS and Linux (#2584).
#
# Before #2584 install.sh only printed a NOTE about PATH. The employee guide's next step,
# `lfr-tunnel -version` in a new terminal, then failed with "command not found", as did every step
# after it. This RUNS install.sh in a throwaway HOME, with a stand-in `curl` (as
# test-install-ldm-link.sh does), and asserts what it leaves in the shell rc files:
#
#   rc files that exist get exactly one PATH line, in the form pkg/client/install_env.go
#     re-points on a move (TestRepointRCLineRepointsTheInstallersPathLine pins the same literal)
#   a second install adds no duplicate
#   a directory already on PATH gets no line
#   on macOS, a missing ~/.zshrc is created (zsh is the default shell)
#
# Nothing is executed but install.sh: the stand-in curl writes placeholder text that is never run.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
INSTALLER="${REPO_ROOT}/pkg/server/static/install.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

sed 's|{{SERVER_URL}}|http://gateway.invalid|' "$INSTALLER" >"${WORK}/install.sh"
mkdir -p "${WORK}/shim"
cat >"${WORK}/shim/curl" <<'SHIM'
#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift; fi
  shift
done
[ -n "$out" ] && printf 'placeholder, never executed\n' >"$out"
SHIM
chmod +x "${WORK}/shim/curl"

# run_installer <home> [extra PATH prefix]: every install-dir override cleared, as a fresh machine.
run_installer() {
  env -u LFT_INSTALL_DIR -u LFR_TUNNEL_INSTALL_DIR \
    -u LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR -u LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR \
    -u LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR \
    HOME="$1" PATH="${2:+$2:}${WORK}/shim:${PATH}" sh "${WORK}/install.sh" >"$1.out" 2>&1
}

# The line install.sh must write for a given home: install_env.go's pathRCLine form.
want_line() { printf 'export PATH="%s/liferay/lfr-tunnel:$PATH"' "$1"; }

count_line() { grep -cxF "$2" "$1" 2>/dev/null || true; }

# 1. Existing rc files each get one PATH line, and the output says how to use it now.
H="${WORK}/existing"
mkdir -p "$H"
: >"${H}/.zshrc"
: >"${H}/.bashrc"
run_installer "$H"
LINE="$(want_line "$H")"
for rc in .zshrc .bashrc; do
  n="$(count_line "${H}/${rc}" "$LINE")"
  if [ "$n" = "1" ]; then
    pass "${rc} gets exactly one PATH line naming the install dir"
  else
    fail "${rc} has ${n:-0} copies of '${LINE}' (want 1): $(cat "${H}/${rc}")"
  fi
done
if grep -q "in THIS terminal now" "${H}.out"; then
  pass "the installer says how to use lfr-tunnel in the current terminal"
else
  fail "the installer's output does not say how to use lfr-tunnel now: $(tail -5 "${H}.out")"
fi

# 2. Installing again adds no duplicate.
run_installer "$H"
n="$(count_line "${H}/.zshrc" "$LINE")"
if [ "$n" = "1" ]; then
  pass "a second install adds no duplicate PATH line"
else
  fail "after a second install .zshrc has ${n} PATH lines (want 1)"
fi

# 3. A directory already on PATH gets no line.
H="${WORK}/on-path"
mkdir -p "$H"
: >"${H}/.bashrc"
run_installer "$H" "${H}/liferay/lfr-tunnel"
if [ "$(count_line "${H}/.bashrc" "$(want_line "$H")")" = "0" ]; then
  pass "no PATH line when the install dir is already on PATH"
else
  fail "a PATH line was written although the install dir was already on PATH"
fi

# 4. macOS: a missing ~/.zshrc is created and gets the line.
H="${WORK}/fresh-mac"
mkdir -p "$H"
run_installer "$H"
if [ "$(uname -s)" = "Darwin" ]; then
  if [ "$(count_line "${H}/.zshrc" "$(want_line "$H")")" = "1" ]; then
    pass "macOS: a missing ~/.zshrc is created with the PATH line"
  else
    fail "macOS: no ~/.zshrc with the PATH line on a machine that had none"
  fi
else
  if [ ! -e "${H}/.zshrc" ]; then
    pass "Linux: no ~/.zshrc is created for a user who has none"
  else
    fail "Linux: install.sh created a ~/.zshrc"
  fi
fi

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
