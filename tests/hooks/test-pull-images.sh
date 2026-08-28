#!/usr/bin/env bash
# test-pull-images.sh — tests for scripts/common/pull-images-with-retry.sh (#1530)
#
# The script exists to absorb a transient registry error, so the interesting cases are the ones
# that never happen on a good day: a 504 on the first attempt, a permanent failure, a Dockerfile
# whose FROM names an earlier stage rather than an image.
#
# The one that would be easiest to get wrong and hardest to notice is coverage: images come from
# TWO places -- compose `image:` services and `FROM` lines in the Dockerfiles those services
# build -- and a loop over one of them looks like a fix while leaving half the pulls unprotected.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/common/pull-images-with-retry.sh"

[ -x "$TARGET" ] || { echo "FATAL: $TARGET missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# A fake repo laid out like the real one: compose files two directories below the root, so the
# script's context resolution is exercised rather than bypassed.
mkdir -p "$WORK/repo/tests/e2e" "$WORK/repo/cmd/thing" "$WORK/bin"
cat > "$WORK/repo/tests/e2e/compose.yml" <<'YML'
services:
  proxy:
    image: nginx:alpine
  mail:
    image: axllent/mailpit
  app:
    build:
      context: ../..
      dockerfile: cmd/thing/Dockerfile
YML
cat > "$WORK/repo/cmd/thing/Dockerfile" <<'DF'
FROM node:20-alpine AS ui-builder
RUN echo hi
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
COPY --from=ui-builder /x /y
FROM builder
FROM alpine:latest
DF

# Stubbed docker: records each pull, and can be told to fail the first N attempts per image.
cat > "$WORK/bin/docker" <<'STUB'
#!/usr/bin/env bash
[ "$1" = "pull" ] || exit 0
shift; [ "$1" = "-q" ] && shift
img="$1"
echo "$img" >> "$PULL_LOG"
n=$(grep -c "^$img$" "$PULL_LOG")
if [ -n "${FAIL_FIRST:-}" ] && [ "$n" -le "$FAIL_FIRST" ]; then exit 1; fi
[ -n "${FAIL_ALWAYS:-}" ] && exit 1
exit 0
STUB
chmod +x "$WORK/bin/docker"
printf '#!/usr/bin/env bash\nexit 0\n' > "$WORK/bin/sleep"
chmod +x "$WORK/bin/sleep"

run() { PULL_LOG="$WORK/pulled.txt" PATH="$WORK/bin:$PATH" "$@" bash "$TARGET" "$WORK/repo/tests/e2e/compose.yml" 2>&1; }
reset() { : > "$WORK/pulled.txt"; }

echo "Testing scripts/common/pull-images-with-retry.sh"
echo ""

# 1. Coverage: both sources, or the fix is half a fix.
reset
run env >/dev/null
missing=""
for img in nginx:alpine axllent/mailpit node:20-alpine golang:1.26-alpine alpine:latest; do
    grep -qx "$img" "$WORK/pulled.txt" || missing="$missing $img"
done
if [ -z "$missing" ]; then
    pass "pulls compose image: services AND the Dockerfile's FROM images"
else
    fail "not pulled:$missing"
fi

# 2. `FROM builder` names an earlier stage. Pulling it would send docker looking for an image
#    called "builder" on every single run.
if grep -qx "builder" "$WORK/pulled.txt" || grep -qx "ui-builder" "$WORK/pulled.txt"; then
    fail "tried to pull a build stage as though it were an image"
else
    pass "a FROM naming an earlier stage is not pulled"
fi

# 3. --platform=$BUILDPLATFORM precedes the image name and must not be mistaken for it.
if grep -q 'platform' "$WORK/pulled.txt"; then
    fail "the --platform flag leaked into an image name"
else
    pass "--platform is stripped from the image name"
fi

# 4. The whole point: a first-attempt failure is retried, not fatal.
reset
out=$(run env FAIL_FIRST=1)
if [ "$(grep -c '^nginx:alpine$' "$WORK/pulled.txt")" -ge 2 ] && printf '%s' "$out" | grep -q "retrying"; then
    pass "a transient failure is retried"
else
    fail "no retry happened: $out"
fi

# 5. A permanent failure must NOT fail the suite. The build that follows is the authority and
#    reports a better error; a mis-parsed image name must not be able to stop the run.
reset
out=$(run env FAIL_ALWAYS=1)
rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "Could not pre-pull"; then
    pass "a permanent failure is reported but does not fail the run"
else
    fail "permanent failure handling wrong: rc=$rc"
fi

# 6. No docker at all (a machine that only runs the unit suite) must be harmless.
reset
out=$(PULL_LOG="$WORK/pulled.txt" PATH="$WORK/empty:/usr/bin:/bin" bash "$TARGET" "$WORK/repo/tests/e2e/compose.yml" 2>&1)
if [ $? -eq 0 ]; then
    pass "a machine without docker is not failed"
else
    fail "missing docker failed the run: $out"
fi

# 7. A compose file that does not exist is skipped, not fatal.
out=$(PATH="$WORK/bin:$PATH" PULL_LOG="$WORK/pulled.txt" bash "$TARGET" "$WORK/nope.yml" 2>&1)
if [ $? -eq 0 ] && printf '%s' "$out" | grep -q "not found"; then
    pass "a missing compose file is reported and skipped"
else
    fail "missing compose file: $out"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
