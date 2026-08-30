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

# 4. What the gateway advertises. Production sets no install_dir in server-config.yaml, so these
#    defaults are what users actually get.
if ! grep -q 'runningpoc' "${REPO_ROOT}/pkg/server/server.go"; then
  pass "server.go carries no stale runningpoc default"
else
  fail "server.go still defaults somewhere to runningpoc"
fi

if [ "$(grep -c '"~/liferay/lfr-tunnel"' "${REPO_ROOT}/pkg/server/server.go")" -ge 4 ]; then
  pass "server.go advertises ~/liferay/lfr-tunnel for every platform"
else
  fail "server.go does not advertise ~/liferay/lfr-tunnel for all four platforms"
fi

# 5. The service installer. A service pointed elsewhere runs an unexcluded binary even when the
#    interactive client is fine, which is the harder version of this bug to notice.
if grep -q 'filepath.Join(home, "liferay", "lfr-tunnel", "lfr-tunnel")' "${REPO_ROOT}/pkg/client/service_installer.go"; then
  pass "the service installer resolves the same path"
else
  fail "the service installer resolves a different path from the installers"
fi

# 6. The document InfoSec is handed. This is the one that was wrong before, and being wrong here
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
