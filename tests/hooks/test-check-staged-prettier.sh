#!/usr/bin/env bash
# test-check-staged-prettier.sh — tests for scripts/check-staged-prettier.sh (#1447)
#
# The interesting behaviour is entirely in the failure paths. A formatting finding must BLOCK the
# commit; the tool being unavailable must NOT, because unformatted code is not irreversible the
# way a leaked secret is and a developer with no npm cache must still be able to commit.
#
# Getting that backwards in either direction is bad: block on a toolchain problem and people
# reach for --no-verify, which disables the secret scan too; pass on a real finding and the check
# is decorative.
#
# npx is stubbed. What is under test is this script's decisions, not Prettier.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/check-staged-prettier.sh"

[ -x "$TARGET" ] || { echo "FATAL: $TARGET is missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM
mkdir -p "$WORK/bin"

# stub_npx writes a fake npx whose behaviour is driven by NPX_MODE.
stub_npx() {
    cat > "$WORK/bin/npx" <<'STUB'
#!/usr/bin/env bash
case "${NPX_MODE:-ok}" in
    ok)      echo "Checking formatting..."; echo "All matched files use Prettier code style!"; exit 0 ;;
    issues)  echo "Checking formatting..."; echo "[warn] ui/x.json"; echo "[warn] Code style issues found in the above file."; exit 1 ;;
    broken)  echo "npm ERR! network request to https://registry.npmjs.org failed" >&2; exit 1 ;;
esac
STUB
    chmod +x "$WORK/bin/npx"
}
stub_npx

run() { LFT_PRETTIER_FILES="ui/x.json" PATH="$WORK/bin:$PATH" "$TARGET" >/dev/null 2>&1; }

echo "Testing scripts/check-staged-prettier.sh"
echo ""

# 1. Formatted files must not block.
NPX_MODE=ok run
[ $? -eq 0 ] && pass "formatted files exit 0" || fail "formatted files did not exit 0"

# 2. A real finding must block, or the check is decorative.
NPX_MODE=issues run
[ $? -eq 1 ] && pass "a formatting finding exits 1 and blocks the commit" \
             || fail "a formatting finding did not block"

# 3. The tool failing must NOT block. Blocking here teaches people to use --no-verify, which
#    disables the secret scan and the EDR guard too.
NPX_MODE=broken run
[ $? -eq 0 ] && pass "a broken toolchain exits 0 rather than blocking the commit" \
             || fail "a broken toolchain blocked the commit"

# 4. ...but it must say so. A silent pass is indistinguishable from a real one.
OUT=$(NPX_MODE=broken LFT_PRETTIER_FILES="ui/x.json" PATH="$WORK/bin:$PATH" "$TARGET" 2>&1)
if printf '%s' "$OUT" | grep -q "NOT checked"; then
    pass "a broken toolchain says the check did not run"
else
    fail "a broken toolchain passed silently: $OUT"
fi

# 5. No staged files in scope: nothing to do, and no npx invocation at all.
if LFT_PRETTIER_FILES="" PATH="$WORK/bin:$PATH" "$TARGET" >/dev/null 2>&1; then
    pass "no files in scope exits 0 without running anything"
else
    fail "empty file list did not exit 0"
fi

# 6. npx absent entirely (a machine with no node) must skip loudly, not block.
mkdir -p "$WORK/empty"
OUT=$(LFT_PRETTIER_FILES="ui/x.json" PATH="$WORK/empty:/usr/bin:/bin" "$TARGET" 2>&1)
RC=$?
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q "not found in PATH"; then
    pass "a machine without npx skips loudly rather than blocking"
else
    fail "missing npx: rc=$RC out=$OUT"
fi

# 7. The fix instruction has to name the actual files, or it is not actionable.
OUT=$(NPX_MODE=issues LFT_PRETTIER_FILES="ui/x.json" PATH="$WORK/bin:$PATH" "$TARGET" 2>&1)
if printf '%s' "$OUT" | grep -q -- "--write"; then
    pass "a finding tells the developer exactly how to fix it"
else
    fail "no --write instruction in the failure output: $OUT"
fi

# 8-9. The GLOB itself (#1550).
#
# Every case above injects LFT_PRETTIER_FILES, which bypasses staged_prettier_files() entirely --
# so the file-selection half of this script had no coverage at all, and that is exactly where the
# bug was: .cjs and .mjs were missing while CI runs `prettier --check .` over the whole tree. A
# .cjs file passed the hook and failed CI (#1548).
#
# These drive the real function against a real index, in a throwaway repo, with npx stubbed to
# record which paths it was handed.
GLOBREPO="$WORK/globrepo"
git init -q "$GLOBREPO"
git -C "$GLOBREPO" config user.email t@example.com
git -C "$GLOBREPO" config user.name Test
mkdir -p "$GLOBREPO/scripts" "$GLOBREPO/ui"
cat > "$WORK/bin/npx-record" <<'STUB'
#!/usr/bin/env bash
# Record every argument that looks like a path, so the test can assert what was selected.
for a in "$@"; do
    case "$a" in
        *.cjs|*.mjs|*.js|*.json|*.css|*.ts|*.tsx|*.jsx) echo "$a" >> "$RECORD" ;;
    esac
done
echo "All matched files use Prettier code style!"
exit 0
STUB
chmod +x "$WORK/bin/npx-record"

for f in scripts/a.cjs scripts/b.mjs scripts/c.js ui/d.json notes.md; do
    echo "x" > "$GLOBREPO/$f"
    git -C "$GLOBREPO" add "$f"
done

RECORD="$WORK/selected.txt"
: > "$RECORD"
cp "$WORK/bin/npx-record" "$WORK/bin/npx"
( cd "$GLOBREPO" && RECORD="$RECORD" PATH="$WORK/bin:$PATH" bash "$TARGET" >/dev/null 2>&1 )
cp "$WORK/bin/npx-record" /dev/null 2>/dev/null || true
stub_npx  # restore for anything after

MISSING=""
for want in scripts/a.cjs scripts/b.mjs scripts/c.js ui/d.json; do
    grep -qx "$want" "$RECORD" || MISSING="$MISSING $want"
done
if [ -z "$MISSING" ]; then
    pass "the glob selects .cjs and .mjs alongside the extensions that already worked"
else
    fail "not selected:$MISSING -- these pass the hook and fail CI, which runs prettier over the whole tree"
fi

# Markdown is held back on purpose (.prettierignore records why), so it must NOT be selected.
if grep -qx "notes.md" "$RECORD"; then
    fail "markdown was selected; .prettierignore holds it back deliberately"
else
    pass "markdown is still left alone"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
