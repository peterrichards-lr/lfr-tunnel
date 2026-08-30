#!/usr/bin/env bash
# test-install-paths.sh — the install path must match the EDR exclusion agreed with S1 (#1591)
#
#   install dir:  <home>/liferay/lfr-tunnel/
#   exclusions:   */liferay/lfr-tunnel/lfr-tunnel        (macOS, Linux)
#                 *\liferay\lfr-tunnel\lfr-tunnel.exe    (Windows)
#
# The exclusions are path wildcards: only the leading portion is wild, so the final two directory
# segments and the binary name are matched literally. A client installed anywhere else is not
# excluded, and SentinelOne quarantines it.
#
# This checks every place that decides the path AND the document InfoSec is given, because the
# failure being guarded against is not one wrong value -- it is the four sources disagreeing.
# Before #1591 they did: the installers used ~/runningpoc/bin while docs/infosec.md told InfoSec
# to exclude /usr/local/bin/lfr-tunnel, an exclusion that could never have matched.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

UNIX_DIR='liferay/lfr-tunnel'
WIN_DIR='liferay\\lfr-tunnel'

echo "Checking the install path matches the agreed EDR exclusion..."

# 1. The shell installer's fallback, used whenever the gateway has not substituted the template.
if grep -qE "DEFAULT_INSTALL_DIR=\"\\\$\{HOME\}/${UNIX_DIR}\"" "${REPO_ROOT}/pkg/server/static/install.sh"; then
  pass "install.sh falls back to \$HOME/${UNIX_DIR}"
else
  fail "install.sh does not fall back to \$HOME/${UNIX_DIR} -- the binary would land outside the exclusion"
fi

# 2. The binary name, which the exclusion matches literally.
if grep -qE 'INSTALL_PATH="\$\{INSTALL_DIR\}/lfr-tunnel"' "${REPO_ROOT}/pkg/server/static/install.sh"; then
  pass "install.sh names the binary lfr-tunnel"
else
  fail "install.sh no longer names the binary lfr-tunnel"
fi

# 3. The PowerShell installer.
if grep -q "DefaultInstallDir = \"\$Home\\\\${WIN_DIR}\"" "${REPO_ROOT}/pkg/server/static/install.ps1"; then
  pass "install.ps1 falls back to \$Home\\${WIN_DIR}"
else
  fail "install.ps1 does not fall back to \$Home\\${WIN_DIR}"
fi

if grep -q 'Join-Path $InstallDir "lfr-tunnel.exe"' "${REPO_ROOT}/pkg/server/static/install.ps1"; then
  pass "install.ps1 names the binary lfr-tunnel.exe"
else
  fail "install.ps1 no longer names the binary lfr-tunnel.exe"
fi

# 4. Tilde expansion. The gateway templates its default in as "~/liferay/lfr-tunnel", and a
#    tilde inside a quoted shell variable is NOT expanded -- `mkdir -p "$INSTALL_DIR"` would
#    create a directory literally named "~" wherever the installer was run from, putting the
#    client in the working directory instead of the home folder.
if grep -q 'INSTALL_DIR="${HOME}/${INSTALL_DIR#\\~/}"' "${REPO_ROOT}/pkg/server/static/install.sh"; then
  pass "install.sh expands a leading tilde"
else
  fail "install.sh does not expand a leading tilde -- a templated ~ would create a literal '~' directory"
fi

# Exercised rather than only grepped for, because the quoting is what makes it work or not.
# shellcheck disable=SC2088  # literal tildes throughout, deliberately: this mirrors install.sh's
# own patterns and feeds them the unexpanded value the gateway actually sends.
probe_expand() {
  INSTALL_DIR="$1"
  case "$INSTALL_DIR" in
    "~") INSTALL_DIR="$HOME" ;;
    "~/"*) INSTALL_DIR="${HOME}/${INSTALL_DIR#\~/}" ;;
  esac
  printf '%s' "$INSTALL_DIR"
}
# shellcheck disable=SC2088  # the unexpanded tilde is the input under test.
if [ "$(probe_expand '~/liferay/lfr-tunnel')" = "${HOME}/liferay/lfr-tunnel" ]; then
  pass "a templated ~/liferay/lfr-tunnel resolves into the home folder"
else
  fail "tilde expansion produced $(probe_expand '~/liferay/lfr-tunnel'), not ${HOME}/liferay/lfr-tunnel"
fi
if [ "$(probe_expand '/opt/custom')" = "/opt/custom" ]; then
  pass "an absolute override is left alone"
else
  fail "tilde expansion mangled an absolute path"
fi

# 5. Windows gets a Windows-shaped default, so the installer needs no separator normalisation --
#    logic that could not be exercised anywhere but Windows.
if grep -qF '`~\liferay\lfr-tunnel`' "${REPO_ROOT}/pkg/server/server.go"; then
  pass "the gateway sends Windows a backslash path"
else
  fail "the gateway no longer sends Windows a backslash path -- install.ps1 would need to normalise separators"
fi

# 6. What the gateway advertises. Production sets no install_dir in server-config.yaml, so these
#    defaults are what users actually get.
if ! grep -q 'runningpoc' "${REPO_ROOT}/pkg/server/server.go"; then
  pass "server.go carries no stale runningpoc default"
else
  fail "server.go still defaults somewhere to runningpoc"
fi

# The three POSIX platforms share one value; Windows deliberately differs, checked above.
missing_posix=""
for plat in macos_arm64 macos_amd64 linux_amd64; do
  if ! grep -qE "\"${plat}\":[[:space:]]+\"~/liferay/lfr-tunnel\"" "${REPO_ROOT}/pkg/server/server.go"; then
    missing_posix="${missing_posix} ${plat}"
  fi
done
if [ -z "$missing_posix" ]; then
  pass "server.go advertises ~/liferay/lfr-tunnel for macOS and Linux"
else
  fail "server.go does not advertise ~/liferay/lfr-tunnel for:${missing_posix}"
fi

# 7. The service installer. A service pointed elsewhere runs an unexcluded binary even when the
#    interactive client is fine, which is the harder version of this bug to notice.
if grep -q 'filepath.Join(home, "liferay", "lfr-tunnel", "lfr-tunnel")' "${REPO_ROOT}/pkg/client/service_installer.go"; then
  pass "the service installer resolves the same path"
else
  fail "the service installer resolves a different path from the installers"
fi

# 8. The document InfoSec is handed. This is the one that was wrong before, and being wrong here
#    means the exclusion someone applies does not match the binary anyone installs.
INFOSEC="${REPO_ROOT}/docs/infosec.md"
if grep -q '\*/liferay/lfr-tunnel/lfr-tunnel' "$INFOSEC" &&
   grep -q '\*\\liferay\\lfr-tunnel\\lfr-tunnel.exe' "$INFOSEC"; then
  pass "docs/infosec.md states both agreed exclusions"
else
  fail "docs/infosec.md does not state the agreed exclusions"
fi

if ! grep -qE '/usr/local/bin/lfr-tunnel|LOCALAPPDATA' "$INFOSEC"; then
  pass "docs/infosec.md no longer states paths the installer never uses"
else
  fail "docs/infosec.md still documents an exclusion that cannot match the installed binary"
fi

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
