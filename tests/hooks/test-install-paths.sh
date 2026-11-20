#!/usr/bin/env bash
# test-install-paths.sh — the install path must match the EDR exclusion agreed with S1 (#1591)
#
#   install dir:  <home>/liferay/lfr-tunnel/
#   exclusions:   /Users/*/liferay/lfr-tunnel/lfr-tunnel           (macOS)
#                 /home/*/liferay/lfr-tunnel/lfr-tunnel            (Linux)
#                 C:\Users\*\liferay\lfr-tunnel\lfr-tunnel.exe    (Windows)
#
# The exclusions are path wildcards: only the username is wild, so the final two directory
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
#
#    ASSERTED ON THE PATH, NOT ON ONE SPELLING OF IT (#2329). This used to grep for the literal
#    `filepath.Join(home, "liferay", "lfr-tunnel", "lfr-tunnel")`. #2324 factored that into
#    named helpers while resolving the identical path, and the grep went red over characters --
#    SKILL section 5b rule 2, "search for the shape, not the symbol", in a gate written to
#    enforce path agreement. Worse, the hooks filter did not name pkg/client/, so Hook Tests
#    skipped the PR that changed it AND the release that shipped past it.
#
#    So: resolve the two parts the path is actually made of, and compare the result. A rename of
#    the helper is invisible here; a change to either part is not.
SI="${REPO_ROOT}/pkg/client/service_installer.go"

# The binary's basename, which is also the directory under ~/liferay and the leaf of the EDR
# exclusion wildcard. Written as a constant since #2324 precisely because those three must agree.
si_name="$(sed -n 's/^const clientBinaryName = "\([^"]*\)".*/\1/p' "$SI" | head -1)"
[ -n "$si_name" ] || si_name="$(sed -n 's/.*filepath\.Join(home, "liferay", "\([^"]*\)".*/\1/p' "$SI" | head -1)"

# The directory it joins under the home folder, accepting the binary name through either
# spelling -- the constant, or the literal it replaced. grep -E rather than sed: BSD sed has no
# \| alternation in a basic regex, and this suite runs on macOS as well as CI.
si_dir=""
if grep -qE "filepath\.Join\(home, \"liferay\", (clientBinaryName|\"${si_name}\")" "$SI"; then
  si_dir="liferay"
fi

si_path="${si_dir}/${si_name}"
if [ "$si_path" = "liferay/lfr-tunnel" ]; then
  pass "the service installer resolves the same path (~/${si_path}/${si_name})"
else
  fail "the service installer resolves ~/${si_path:-<unparsed>} -- the installers use ~/liferay/lfr-tunnel, and only that is inside the EDR exclusion"
fi

# 8. The document InfoSec is handed. This is the one that was wrong before, and being wrong here
#    means the exclusion someone applies does not match the binary anyone installs.
INFOSEC="${REPO_ROOT}/docs/infosec.md"
# Whole lines, so a rule cannot pass by containing the right tail behind the wrong head.
missing_rules=""
for rule in '/Users/*/liferay/lfr-tunnel/lfr-tunnel' '/home/*/liferay/lfr-tunnel/lfr-tunnel' \
            'C:\Users\*\liferay\lfr-tunnel\lfr-tunnel.exe'; do
  grep -qxF "$rule" "$INFOSEC" || missing_rules="${missing_rules} ${rule}"
done
if [ -z "$missing_rules" ]; then
  pass "docs/infosec.md states the exclusion for the binary on every OS"
else
  fail "docs/infosec.md does not state the exclusion rule(s):${missing_rules}"
fi
# The EDR console rejects an exclusion path that starts with a wildcard, and on Windows a leading
# `*\` spans the drive and Users as well as the username (#2507 review). Every rule names its root.
leading_wild="$(grep -nE '^\*[\\/]' "$INFOSEC")"
if [ -z "$leading_wild" ]; then
  pass "no exclusion rule in docs/infosec.md starts with a wildcard"
else
  fail "docs/infosec.md has an exclusion rule starting with a wildcard, which the console rejects: ${leading_wild}"
fi

# Lowercase after the root (#2542). The installers create `liferay`, and InfoSec could not confirm
# whether the EDR matches macOS paths case-sensitively, so a rule spelled `Liferay` may match
# nothing. The exact-line checks above cannot see this: they pass as long as the lowercase rule is
# present, even beside a mixed-case one. So scan every rule line, not just the expected ones.
rule_lines="$(grep -nE '^(/Users/|/home/|C:\\Users\\)' "$INFOSEC")"
if [ "$(printf '%s\n' "$rule_lines" | grep -c .)" -lt 6 ]; then
  fail "found fewer than 6 exclusion rule lines in docs/infosec.md -- the case check below would scan too little"
fi
mixed_case="$(printf '%s\n' "$rule_lines" | sed -E 's#^([0-9]+:)(/Users/|/home/|C:\\Users\\)#\1#' | grep -E '^[0-9]+:.*[A-Z]')"
if [ -z "$mixed_case" ]; then
  pass "every exclusion rule in docs/infosec.md is lowercase after its root"
else
  fail "docs/infosec.md has an exclusion rule with uppercase after its root, which may not match the lowercase path the installers create: ${mixed_case}"
fi

if ! grep -qE '/usr/local/bin/lfr-tunnel|LOCALAPPDATA' "$INFOSEC"; then
  pass "docs/infosec.md no longer states paths the installer never uses"
else
  fail "docs/infosec.md still documents an exclusion that cannot match the installed binary"
fi

# 9. One table (#2507). docs/infosec.md section 3 is the single source; the installers must create
#    what it lists, and SECURITY.md and the SE guide must point at it rather than restate paths.
#    Each source disagreeing with the others is the failure this file exists for.
for row in '`~/liferay/lfr-tunnel/lfr-tunnel`' '`%USERPROFILE%\liferay\lfr-tunnel\lfr-tunnel.exe`' \
           '`~/.ldm/bin/lfr-tunnel`' '`%USERPROFILE%\.ldm\bin\lfr-tunnel.exe`'; do
  if grep -qF "$row" "$INFOSEC"; then
    pass "docs/infosec.md's install table lists ${row}"
  else
    fail "docs/infosec.md's install table does not list ${row}"
  fi
done
for rule in '/Users/*/.ldm/bin/lfr-tunnel' '/home/*/.ldm/bin/lfr-tunnel' 'C:\Users\*\.ldm\bin\lfr-tunnel.exe'; do
  if grep -qxF "$rule" "$INFOSEC"; then
    pass "docs/infosec.md excludes the LDM link: ${rule}"
  else
    fail "docs/infosec.md has no exclusion rule for the LDM link ${rule} -- the engines need it as well as the binary"
  fi
done
# Decision 0006: no package-manager exclusion is requested for Liferay.
if grep -qE '^/opt/' "$INFOSEC"; then
  fail "docs/infosec.md still lists a package-manager exclusion rule (decision 0006 drops them)"
else
  pass "docs/infosec.md lists no package-manager exclusion rule"
fi
# What the installers create must be what the table lists.
if grep -qF 'ln -sf "$INSTALL_PATH" "${HOME}/.ldm/bin/lfr-tunnel"' "${REPO_ROOT}/pkg/server/static/install.sh"; then
  pass "install.sh creates the LDM link the table lists"
else
  fail "install.sh no longer creates ~/.ldm/bin/lfr-tunnel, which the table and the exclusions name"
fi
if grep -qF '$LdmBinDir = "$Home\.ldm\bin"' "${REPO_ROOT}/pkg/server/static/install.ps1" &&
   grep -qF '$LdmTarget = "$LdmBinDir\lfr-tunnel.exe"' "${REPO_ROOT}/pkg/server/static/install.ps1"; then
  pass "install.ps1 creates the LDM link the table lists"
else
  fail "install.ps1 no longer creates %USERPROFILE%\\.ldm\\bin\\lfr-tunnel.exe, which the table and the exclusions name"
fi
SECURITY_MD="${REPO_ROOT}/SECURITY.md"
if grep -qE '/usr/local/bin/lfr-tunnel|LOCALAPPDATA|gh attestation verify' "$SECURITY_MD"; then
  fail "SECURITY.md still states an install path or a verification step that does not exist"
else
  pass "SECURITY.md states no install path the installers never use"
fi
if grep -qF 'docs/infosec.md#3-standard-execution-paths--edr-exclusions' "$SECURITY_MD"; then
  pass "SECURITY.md points at the single table"
else
  fail "SECURITY.md does not link to docs/infosec.md section 3"
fi
SE_GUIDE="${REPO_ROOT}/docs/liferay-se-guide.md"
if grep -qE 'export `?LFT_INSTALL_DIR' "$SE_GUIDE"; then
  fail "the SE guide tells SEs to set LFT_INSTALL_DIR, which moves the client out of the exclusion"
else
  pass "the SE guide does not advertise LFT_INSTALL_DIR"
fi
if grep -qF 'infosec.md#3-standard-execution-paths--edr-exclusions' "$SE_GUIDE"; then
  pass "the SE guide points at the single table"
else
  fail "the SE guide does not link to docs/infosec.md section 3"
fi

# 10. No doc recommends a package manager without the EDR caveat (decision 0006, #2507 review).
#     Homebrew and Scoop stay a supported route outside Liferay, so mentioning them is fine. What
#     is not: a doc that shows a `brew install` or `scoop install` of the client without sending
#     EDR-managed machines to the native installer (a link to infosec.md section 3), or that
#     calls package managers "Recommended". The path checks above cannot see either.
CAVEAT='infosec.md#3-standard-execution-paths--edr-exclusions'
pm_docs="$(grep -rlE --include='*.md' '(brew|scoop) install lfr-tunnel' "${REPO_ROOT}/docs" "${REPO_ROOT}/README.md" "${REPO_ROOT}/SECURITY.md" 2>/dev/null)"
if [ -z "$pm_docs" ]; then
  fail "no doc shows a Homebrew or Scoop install -- this check found nothing to check, so its search is broken"
fi
while IFS= read -r doc; do
  [ -n "$doc" ] || continue
  rel="${doc#"${REPO_ROOT}"/}"
  if grep -qF "$CAVEAT" "$doc"; then
    pass "${rel} shows a package-manager install and links the EDR caveat"
  else
    fail "${rel} shows a Homebrew/Scoop install without sending EDR-managed machines to the native installer (link ${CAVEAT})"
  fi
done <<< "$pm_docs"
recommended="$(grep -rniE --include='*.md' 'recommended[^.]*(package manager|homebrew|scoop)|(package manager|homebrew|scoop)[^.]*recommended' "${REPO_ROOT}/docs" "${REPO_ROOT}/README.md" 2>/dev/null)"
if [ -z "$recommended" ]; then
  pass "no doc calls a package manager the recommended install route"
else
  fail "a doc calls a package manager the recommended install route, which decision 0006 rules out on EDR-managed machines: ${recommended}"
fi

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
