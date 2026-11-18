#!/usr/bin/env bash
# test-install-in-place.sh — install.sh writes the client straight to its install path (#2505)
#
# The EDR exclusions cover <home>/liferay/lfr-tunnel/lfr-tunnel, and a freshly downloaded
# executable in /tmp is what gets quarantined before any move happens. install.sh used to
# `curl -o /tmp/lfr-tunnel`, then chmod and mv it. It now renames the current binary to .old,
# downloads to the install path itself, and puts the .old back if the download fails.
#
# RUN IN DOCKER, never on the host: running the installers on a developer machine is what the EDR
# rules forbid (.agents/skills/edr-constraints/SKILL.md). curl is a stub inside the container that
# records where it was told to write and serves a fake "binary" -- a two-line shell script -- or a
# failed, partial download. Nothing real is downloaded or run.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE="${REPO_ROOT}/pkg/server/static/install.sh"
IMAGE="${INSTALL_TEST_IMAGE:-bash:5}"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; }

echo "Testing that install.sh installs in place, inside $IMAGE..."

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    if [ "${LFT_HOOK_TEST_ALLOW_SKIP:-0}" = "1" ]; then
        skip "Docker unavailable, skip explicitly allowed"
        exit 0
    fi
    fail "Docker unavailable. The installer may only run inside a container, so skipping
    silently would leave #2505 untested. Set LFT_HOOK_TEST_ALLOW_SKIP=1 to allow it deliberately."
    exit 1
fi

# Under the repository, not mktemp's default: on macOS that is /var/folders, which a colima VM
# does not share, so the container would see an empty mount.
WORK="$(mktemp -d "${REPO_ROOT}/.install-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

sed "s|{{SERVER_URL}}|https://gw.example.test|g" "$TEMPLATE" > "$WORK/install.sh"

mkdir -p "$WORK/stub"
cat > "$WORK/stub/curl" <<'STUB'
#!/bin/sh
# Records the -o target, then writes a fake binary, or a partial file and a curl failure.
out=""
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift ;; esac
  shift
done
echo "$out" >> /work/curl-targets
if [ "${CURL_MODE:-ok}" = "fail" ]; then
  printf 'partial' > "$out"
  exit 22
fi
printf '#!/bin/sh\necho NEW\n' > "$out"
STUB
chmod +x "$WORK/stub/curl"

cat > "$WORK/inner.sh" <<'INNER'
#!/bin/sh
# Each case prints "OK <name>" or "BAD <name>: <why>".
export PATH="/work/stub:$PATH"
BIN=/home/u/liferay/lfr-tunnel/lfr-tunnel

reset() {
  rm -rf /home/u /tmp/lfr-tunnel* /work/curl-targets
  mkdir -p /home/u
  if [ "$1" = "old" ]; then
    mkdir -p "$(dirname "$BIN")"
    printf 'OLD' > "$BIN"
    chmod +x "$BIN"
  fi
}
run() { HOME=/home/u CURL_MODE="$1" sh /work/install.sh >/work/out 2>&1; }
check_no_staging() {
  case "$(ls /tmp)" in *lfr-tunnel*) echo "BAD $1: an executable was staged in /tmp"; return ;; esac
  if grep -qv "^$BIN\$" /work/curl-targets; then echo "BAD $1: curl wrote to $(cat /work/curl-targets)"; return; fi
  echo "OK $1: curl wrote only to the install path"
}

reset none; run ok; rc=$?
[ $rc -eq 0 ] && [ -x "$BIN" ] && grep -q NEW "$BIN" && echo "OK fresh install" || echo "BAD fresh install: rc=$rc $(cat /work/out)"
check_no_staging "fresh install"

reset old; run ok; rc=$?
[ $rc -eq 0 ] && grep -q NEW "$BIN" && echo "OK upgrade replaces the binary" || echo "BAD upgrade: rc=$rc $(cat /work/out)"
[ ! -e "$BIN.old" ] && echo "OK upgrade removes .old" || echo "BAD upgrade left $BIN.old"
check_no_staging "upgrade"

reset old; run fail; rc=$?
[ $rc -ne 0 ] && echo "OK a failed download fails the install" || echo "BAD a failed download exited 0"
[ "$(cat "$BIN" 2>/dev/null)" = "OLD" ] && echo "OK a failed download keeps the previous binary" || echo "BAD the previous binary was not restored: $(cat "$BIN" 2>/dev/null)"
[ ! -e "$BIN.old" ] && echo "OK a failed download leaves no .old" || echo "BAD a failed download left $BIN.old"

reset none; run fail; rc=$?
[ $rc -ne 0 ] && [ ! -e "$BIN" ] && echo "OK a failed fresh install leaves no partial binary" || echo "BAD a failed fresh install: rc=$rc, $(ls -la "$BIN" 2>&1)"
INNER

out=$(docker run --rm -v "$WORK":/work "$IMAGE" sh /work/inner.sh 2>&1)
rc=$?
if [ $rc -ne 0 ] && ! grep -qE '^(OK|BAD)' <<<"$out"; then
    fail "the container did not run: $out"
fi
while IFS= read -r line; do
    case "$line" in
        OK*) pass "${line#OK }" ;;
        BAD*) fail "${line#BAD }" ;;
    esac
done <<< "$out"

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
