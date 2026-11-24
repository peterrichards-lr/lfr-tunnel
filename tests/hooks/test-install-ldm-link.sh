#!/usr/bin/env bash
# test-install-ldm-link.sh — install.sh removes the retired ~/.ldm/bin link it used to make, and
# only that (#2579).
#
# test-install-paths.sh checks the installers' TEXT: that no `ln -s` remains and that a removal
# block exists. This RUNS install.sh, in a throwaway HOME with a stand-in `curl`, so what it does to
# a real home directory is what is asserted:
#
#   the link an earlier install made (naming this install)   -> removed
#   a link naming nothing (its target is gone)                -> removed
#   a link to the user's own build elsewhere                  -> kept
#   a regular file the user put there                         -> kept
#   no link at all                                            -> none created
#
# Nothing is executed but install.sh itself: the stand-in curl writes a few bytes of text where the
# binary would go, and install.sh never runs what it downloads.
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

# The served script has {{SERVER_URL}} filled in by the gateway; without it the script stops at once.
sed 's|{{SERVER_URL}}|http://gateway.invalid|' "$INSTALLER" >"${WORK}/install.sh"

# A curl that never touches the network: writes placeholder bytes to the -o path.
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

# Runs the installer with HOME=$1. Every install-dir override is cleared: a developer machine may
# set one (LFT_INSTALL_DIR), and the installer must use its default under the sandbox HOME.
run_installer() {
  env -u LFT_INSTALL_DIR -u LFR_TUNNEL_INSTALL_DIR \
    -u LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR -u LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR \
    -u LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR \
    HOME="$1" PATH="${WORK}/shim:${PATH}" sh "${WORK}/install.sh" >"$1.out" 2>&1
}

# new_home <name>: an empty home with the ~/.ldm/bin directory LDM would have.
new_home() {
  local h="${WORK}/$1"
  mkdir -p "${h}/.ldm/bin"
  printf '%s' "$h"
}

# 1. The installer's own link, naming this install.
H=$(new_home installer-link)
mkdir -p "${H}/liferay/lfr-tunnel"
printf 'old\n' >"${H}/liferay/lfr-tunnel/lfr-tunnel"
ln -s "${H}/liferay/lfr-tunnel/lfr-tunnel" "${H}/.ldm/bin/lfr-tunnel"
if run_installer "$H" && [ -s "${H}/liferay/lfr-tunnel/lfr-tunnel" ]; then
  if [ ! -e "${H}/.ldm/bin/lfr-tunnel" ] && [ ! -L "${H}/.ldm/bin/lfr-tunnel" ]; then
    pass "the link an earlier install made is removed"
  else
    fail "the link an earlier install made is still there"
  fi
else
  fail "install.sh failed in the sandbox: $(tail -3 "${H}.out" 2>/dev/null)"
fi

# 2. A link naming nothing.
H=$(new_home dangling)
ln -s "${H}/gone/lfr-tunnel" "${H}/.ldm/bin/lfr-tunnel"
run_installer "$H"
if [ ! -L "${H}/.ldm/bin/lfr-tunnel" ]; then
  pass "a link naming nothing is removed"
else
  fail "a dangling link was left in place"
fi

# 3. A link to the user's own build: theirs.
H=$(new_home users-link)
mkdir -p "${H}/src"
printf 'mine\n' >"${H}/src/lfr-tunnel"
ln -s "${H}/src/lfr-tunnel" "${H}/.ldm/bin/lfr-tunnel"
run_installer "$H"
if [ "$(readlink "${H}/.ldm/bin/lfr-tunnel" 2>/dev/null)" = "${H}/src/lfr-tunnel" ]; then
  pass "a link to the user's own build is kept"
else
  fail "a link to the user's own build was removed or changed"
fi

# 4. A regular file: not something the installer made.
H=$(new_home regular-file)
printf 'users-own\n' >"${H}/.ldm/bin/lfr-tunnel"
run_installer "$H"
if [ -f "${H}/.ldm/bin/lfr-tunnel" ] && [ "$(cat "${H}/.ldm/bin/lfr-tunnel")" = "users-own" ]; then
  pass "a regular file at the link's path is kept"
else
  fail "a regular file at the link's path was removed or changed"
fi

# 5. No link: none created.
H="${WORK}/fresh"
mkdir -p "$H"
run_installer "$H"
if [ ! -e "${H}/.ldm/bin/lfr-tunnel" ] && [ ! -L "${H}/.ldm/bin/lfr-tunnel" ]; then
  pass "a fresh install creates no ~/.ldm/bin link"
else
  fail "a fresh install created ~/.ldm/bin/lfr-tunnel (#2579 retired it)"
fi
# Control for the sandbox itself: the install did land, so a missing link means "not created",
# not "the installer never ran".
if [ -s "${H}/liferay/lfr-tunnel/lfr-tunnel" ]; then
  pass "the sandboxed install wrote the client to ~/liferay/lfr-tunnel (the run was real)"
else
  fail "the sandboxed install wrote nothing to ~/liferay/lfr-tunnel: $(tail -3 "${H}.out" 2>/dev/null)"
fi

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
