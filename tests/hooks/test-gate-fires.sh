#!/usr/bin/env bash
# test-gate-fires.sh — a deliberately broken input must make each gate exit non-zero (#1779)
#
# The sibling property to tests/hooks/test-gate-anti-vacuity.sh. That file asserts a gate
# refuses to report success having examined nothing; this one asserts it reports failure when
# there IS something wrong. A gate can hold either property without the other, and a gate
# holding neither is decoration that reads as coverage.
#
# Four gates in scripts/ had no test of any kind before this file:
#
#   check-theme-contrast.cjs     70 contrast checks across 4 themes, nothing proving one fails
#   check-i18n-keys.cjs          790 keys, nothing proving an undefined one is caught
#   check-commit-attribution.sh  the guard against a permanently unmergeable PR (#1384)
#   check_docs_review.py         only ever exercised incidentally, by test-nested-worktree-scope.sh
#
# check-docs-rendered.sh joined later (#2383) and left again (#2407): the built-site checks are
# now the template's scripts/check_docs_rendered.py, a verbatim shared copy whose fixtures and
# mutation tests live with it upstream (ai-agent-template tests/test_docs_site.py).
#
# check-required-contexts.sh had one (test-required-contexts-mirror.sh) covering check 5 only,
# so checks 1-4 -- including the one that catches a required context nobody emits -- were
# unproven.
#
# THE MESSAGE IS THE ASSERTION, not the exit code (github-workflow §5c). Every case below
# greps for text only the intended defect produces. "Exited non-zero" is shared by a syntax
# error, a missing binary, a bad path and a real finding, and this suite invokes real scripts
# against real fixtures, so all four are reachable. Where a case cannot reach its subject it
# reports HARNESS: rather than FAIL, so a broken fixture is never read as a working gate.
#
# Each case also carries its NARROWNESS CONTROL: an input the gate must still accept. A gate
# that refused everything would satisfy every assertion above while being useless.
#
# The fixtures are real: a genuine `git worktree add` for the gates that need a whole
# repository, and a genuine `git init` for the ones that read git config. Not a mock directory
# -- the thing under test is how these scripts behave against git's own on-disk shape.
#
# bash 3.2 compatible (macOS /bin/bash) -- no associative arrays, no mapfile. See AGENTS.md.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT" || exit 1

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
harness() { echo "  HARNESS: $1"; FAIL=$((FAIL + 1)); }

SANDBOX=$(mktemp -d)
WT_DIR=".lft-gate-fires-test-$$"
WT_PATH="$REPO_ROOT/$WT_DIR"

cleanup() {
    rm -rf "$SANDBOX"
    if [ -e "$WT_PATH" ]; then
        git worktree remove --force "$WT_PATH" >/dev/null 2>&1 || rm -rf "$WT_PATH"
    fi
    git worktree prune >/dev/null 2>&1
}
trap cleanup EXIT INT TERM

echo "Testing that every gate fires on a broken input..."

# ---------------------------------------------------------------------------
# Fixture A: a detached worktree, for the gates that need a whole repository.
#
# The gate SOURCES are copied in from the working tree afterwards. A worktree is checked out
# at HEAD, so without this the subject would be the last committed version of each script
# rather than the one being changed -- and a fix would appear to work before it was written.
# The corpus is the fixture; the script is production.
# ---------------------------------------------------------------------------
if ! git worktree add --detach "$WT_PATH" HEAD >/dev/null 2>&1; then
    harness "could not create the fixture worktree at $WT_DIR -- the corpus gates below were not tested"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi
cp "$REPO_ROOT"/scripts/*.cjs "$REPO_ROOT"/scripts/*.mjs "$WT_PATH/scripts/" 2>/dev/null

# ---------------------------------------------------------------------------
# 1. check-theme-contrast.cjs — a fill a white label cannot sit on.
#
# --danger-strong is .btn-danger's gradient start. Lightening it is the exact defect #1458
# shipped and #1514 repeated: the button renders, nothing errors, and the label is unreadable.
# ---------------------------------------------------------------------------
DARK="$WT_PATH/pkg/server/static/themes/dark.css"
if [ ! -f "$DARK" ]; then
    harness "no dark.css in the fixture worktree -- case 1 proves nothing"
else
    BASE_OUT=$(cd "$WT_PATH" && node scripts/check-theme-contrast.cjs 2>&1)
    if [ $? -ne 0 ]; then
        harness "check-theme-contrast.cjs already fails on the unmutated fixture, so a failure
        after mutating it would prove nothing: $(echo "$BASE_OUT" | head -2 | tr '\n' ' ')"
    else
        # Narrowness control, and it is the same run: an untouched palette must pass. Stated
        # explicitly because it passes both before and after any change to the gate -- it
        # bounds the assertion below rather than testing it.
        pass "check-theme-contrast.cjs passes an untouched palette (narrowness control)"

        sed 's/--danger-strong:[^;]*;/--danger-strong: #ffdddd;/' "$DARK" >"$DARK.mutated" &&
            mv "$DARK.mutated" "$DARK"
        if ! grep -q -- '--danger-strong: #ffdddd;' "$DARK"; then
            harness "the --danger-strong mutation did not apply to dark.css -- case 1 proves nothing"
        else
            OUT=$(cd "$WT_PATH" && node scripts/check-theme-contrast.cjs 2>&1)
            RC=$?
            if [ "$RC" -eq 0 ]; then
                fail "check-theme-contrast.cjs PASSED a near-white --danger-strong. A white label on
        it measures 1.26:1 against a 4.5:1 bar, which is the #1458 defect exactly."
            elif echo "$OUT" | grep -q 'white label on --danger-strong is'; then
                pass "check-theme-contrast.cjs fires on an illegible fill, naming the token and the ratio"
            else
                fail "check-theme-contrast.cjs exited $RC but did not name --danger-strong, so something
        other than the contrast measurement failed: $(echo "$OUT" | head -2 | tr '\n' ' ')"
            fi
        fi
    fi
fi

# ---------------------------------------------------------------------------
# 2. check-i18n-keys.cjs — a key used in markup with no entry in Language.properties.
#
# The defect is invisible in English: every call site carries an inline fallback, which is how
# 477 keys drifted out of the bundle before anything noticed (#1701).
# ---------------------------------------------------------------------------
SETUP="$WT_PATH/pkg/server/static/setup.html"
if [ ! -f "$SETUP" ]; then
    harness "no setup.html in the fixture worktree -- case 2 proves nothing"
else
    BASE_OUT=$(cd "$WT_PATH" && node scripts/check-i18n-keys.cjs 2>&1)
    if [ $? -ne 0 ]; then
        harness "check-i18n-keys.cjs already fails on the unmutated fixture: $(echo "$BASE_OUT" | head -2 | tr '\n' ' ')"
    else
        pass "check-i18n-keys.cjs passes the real bundle (narrowness control)"

        # A key name that cannot collide with a real one, and that Language.properties has no
        # entry for in any locale.
        sed 's|<body|<body data-i18n="lft_bogus_key_1779"|' "$SETUP" >"$SETUP.mutated" &&
            mv "$SETUP.mutated" "$SETUP"
        if ! grep -q 'lft_bogus_key_1779' "$SETUP"; then
            harness "the bogus-key mutation did not apply to setup.html -- case 2 proves nothing"
        else
            OUT=$(cd "$WT_PATH" && node scripts/check-i18n-keys.cjs 2>&1)
            RC=$?
            if [ "$RC" -eq 0 ]; then
                fail "check-i18n-keys.cjs PASSED a data-i18n key with no entry in Language.properties."
            elif echo "$OUT" | grep -q 'lft_bogus_key_1779'; then
                pass "check-i18n-keys.cjs fires on an undefined key, naming the key and its call site"
            else
                fail "check-i18n-keys.cjs exited $RC without naming lft_bogus_key_1779, so it failed for
        some other reason: $(echo "$OUT" | head -3 | tr '\n' ' ')"
            fi
        fi
    fi
fi

# ---------------------------------------------------------------------------
# 3. check-commit-attribution.sh — an address GitHub cannot map to an account.
#
# Fixture B: a real `git init`, with HOME and GIT_CONFIG_GLOBAL redirected. Without that the
# developer's own ~/.gitconfig supplies user.email and the "no email set" case cannot be
# reached at all -- verified: unsetting it locally simply fell through to the global value,
# and the case would have passed by reading a real address rather than no address (#1798 is
# the same lesson for the Go suite).
# ---------------------------------------------------------------------------
ATTR="$REPO_ROOT/scripts/check-commit-attribution.sh"
mkdir -p "$SANDBOX/home" "$SANDBOX/repo"
git init --quiet "$SANDBOX/repo" >/dev/null 2>&1
: >"$SANDBOX/gitconfig-empty"

run_attr() { # run_attr <email|-->
    cd "$SANDBOX/repo" || return 1
    if [ "$1" != "--" ]; then
        git config user.email "$1"
    else
        git config --unset user.email >/dev/null 2>&1
    fi
    OUT=$(HOME="$SANDBOX/home" GIT_CONFIG_GLOBAL="$SANDBOX/gitconfig-empty" \
        GIT_CONFIG_NOSYSTEM=1 bash "$ATTR" 2>&1)
    RC=$?
    cd "$REPO_ROOT" || return 1
}

if [ ! -f "$ATTR" ]; then
    harness "check-commit-attribution.sh not found -- case 3 proves nothing"
else
    run_attr 'nobody@example.invalid'
    if [ "$RC" -eq 0 ]; then
        fail "check-commit-attribution.sh ACCEPTED 'nobody@example.invalid'. An unattributed head
        commit needs an approving review master cannot supply, so the PR is unmergeable with
        every check green (#1384)."
    elif echo "$OUT" | grep -q "git user.email is 'nobody@example.invalid'"; then
        pass "check-commit-attribution.sh refuses a non-attributing address, naming it"
    else
        fail "check-commit-attribution.sh exited $RC without naming the address, so it failed for
        another reason: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    # The case the developer's own gitconfig hid.
    run_attr '--'
    if [ "$RC" -eq 0 ]; then
        fail "check-commit-attribution.sh ACCEPTED a commit with no user.email set at all"
    elif echo "$OUT" | grep -q 'No git user.email is set'; then
        pass "check-commit-attribution.sh refuses an unset user.email with its own message"
    else
        fail "check-commit-attribution.sh exited $RC on an unset email but said something else,
        so the empty-email branch was not the one that ran: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    # Narrowness controls. A hook that refused every address would satisfy both cases above
    # and make the repository uncommittable.
    run_attr '123456+someone@users.noreply.github.com'
    if [ "$RC" -eq 0 ]; then
        pass "check-commit-attribution.sh accepts a users.noreply.github.com address (narrowness control)"
    else
        fail "check-commit-attribution.sh REFUSED a users.noreply.github.com address, which GitHub
        issues itself and always attributes: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi

    cd "$SANDBOX/repo" && git config lft.attributableEmails 'allowed@example.invalid' && cd "$REPO_ROOT" || true
    run_attr 'allowed@example.invalid'
    if [ "$RC" -eq 0 ]; then
        pass "check-commit-attribution.sh honours the lft.attributableEmails allowlist (narrowness control)"
    else
        fail "check-commit-attribution.sh refused an explicitly allowlisted address, so the escape
        hatch is inert: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi
fi

# ---------------------------------------------------------------------------
# 4. check_docs_review.py — a markdown file with no review footer.
# ---------------------------------------------------------------------------
DOCS="$REPO_ROOT/scripts/check_docs_review.py"
printf '# no footer here\n\nNothing at the bottom.\n' >"$SANDBOX/nofooter.md"
printf '# fine\n\n---\n*Last Updated: %s* | *Last Reviewed: %s*\n' \
    "$(date +%Y-%m-%d)" "$(date +%Y-%m-%d)" >"$SANDBOX/withfooter.md"

OUT=$(python3 "$DOCS" --changed-files "$SANDBOX/nofooter.md" 2>&1)
RC=$?
if [ "$RC" -eq 0 ]; then
    fail "check_docs_review.py PASSED a markdown file with no review footer"
elif echo "$OUT" | grep -q 'MISSING-FOOTER'; then
    pass "check_docs_review.py fires on a footerless markdown file"
else
    fail "check_docs_review.py exited $RC but did not report MISSING-FOOTER, so a different
        branch ran: $(echo "$OUT" | head -2 | tr '\n' ' ')"
fi

OUT=$(python3 "$DOCS" --changed-files "$SANDBOX/withfooter.md" 2>&1)
if [ $? -eq 0 ]; then
    pass "check_docs_review.py accepts a file with a current footer (narrowness control)"
else
    fail "check_docs_review.py refused a file whose footer is today's date: $(echo "$OUT" | head -2 | tr '\n' ' ')"
fi

# ---------------------------------------------------------------------------
# 4b. check_docs_review.py --review-only — the CI gate on published pages' review age (#2389).
#
# AGE_CMD is the command the cases run, and the check below holds ci.yml to it: the step must
# run exactly this, inside the required docs-review-check job, with nothing that lets it be
# skipped or ignored. A grep for the string alone stayed green under `continue-on-error: true`.
# The fixture is a copy of the real docs/, with one page edited.
# ---------------------------------------------------------------------------
AGE_CMD="python3 scripts/check_docs_review.py --dir docs --review-only --max-review-days 180"
STEP_OK=$(python3 - "$AGE_CMD" 2>&1 <<'PY'
import re, sys
cmd, lines = sys.argv[1], open(".github/workflows/ci.yml", encoding="utf-8").read().splitlines()
at = [i for i, l in enumerate(lines) if l.strip() == "run: " + cmd]
if len(at) != 1:
    sys.exit(f"{len(at)} step(s) run it, expected 1")
job = next(l for l in reversed(lines[:at[0]]) if re.match(r"^  [\w-]+:\s*$", l)).strip()
if job != "docs-review-check:":
    sys.exit(f"it runs in job {job}, not docs-review-check")
start = next(i for i in range(at[0], -1, -1) if lines[i].lstrip().startswith("- "))
end = next((i for i in range(at[0] + 1, len(lines)) if lines[i].lstrip().startswith("- ")
            or re.match(r"^  \S", lines[i])), len(lines))
for l in lines[start:end]:
    if re.match(r"^\s*-?\s*(if|continue-on-error):", l):
        sys.exit(f"the step can be skipped or ignored: {l.strip()}")
print("ok")
PY
)
if [ "$STEP_OK" = "ok" ]; then
    pass "ci.yml runs the review-age command, unconditionally, in docs-review-check"
else
    fail "ci.yml's review-age step is not what the cases below test: $STEP_OK"
fi
# The gate reads docs/ because MkDocs does. If mkdocs.yml ever moves docs_dir, the gate would
# keep reading a folder the site no longer publishes.
if grep -qE '^docs_dir:' mkdocs.yml; then
    fail "mkdocs.yml sets docs_dir; the review-age step still reads --dir docs"
fi
make_docs() { # make_docs <dir>: a sandbox repo root holding a copy of the real docs/ and the checker
    mkdir -p "$1/scripts"
    cp -R "$REPO_ROOT/docs" "$1/docs"
    cp "$DOCS" "$1/scripts/"
}
days_ago() { python3 -c "import datetime,sys; print(datetime.date.today() - datetime.timedelta(days=int(sys.argv[1])))" "$1"; }
set_reviewed() { # set_reviewed <file> <replacement for the whole footer line, or "" to delete it>
    python3 - "$1" "$2" <<'PY'
import re, sys
path, line = sys.argv[1], sys.argv[2]
s = open(path, encoding="utf-8").read()
s, n = re.subn(r"^\*Last Updated:.*$\n?", line + "\n" if line else "", s, flags=re.M)
assert n == 1, f"{path}: expected one footer line, found {n}"
open(path, "w", encoding="utf-8").write(s)
PY
}
run_age() { # run_age <label> <dir> <expected tag> [page, default docs/mcp.md]
    local page="${4:-docs/mcp.md}"
    OUT=$(cd "$2" && $AGE_CMD 2>&1)
    RC=$?
    if [ "$RC" -eq 0 ]; then
        fail "review-age gate PASSED $1"
    elif grep -qF "[$3] $page" <<<"$OUT"; then
        pass "review-age gate fires on $1"
    else
        fail "review-age gate exited $RC on $1 but did not report $3 for $page:
        $(head -4 <<<"$OUT" | tr '\n' ' ')"
    fi
}
TODAY=$(days_ago 0)

make_docs "$SANDBOX/age-stale"
set_reviewed "$SANDBOX/age-stale/docs/mcp.md" "*Last Updated: $(days_ago 400)* | *Last Reviewed: $(days_ago 181)*"
run_age "a page last reviewed 181 days ago" "$SANDBOX/age-stale" STALE

make_docs "$SANDBOX/age-nofooter"
set_reviewed "$SANDBOX/age-nofooter/docs/mcp.md" ""
run_age "a page with its footer removed" "$SANDBOX/age-nofooter" MISSING-FOOTER

make_docs "$SANDBOX/age-baddate"
set_reviewed "$SANDBOX/age-baddate/docs/mcp.md" "*Last Updated: $TODAY* | *Last Reviewed: 2026-13-01*"
run_age "a page whose review date does not parse" "$SANDBOX/age-baddate" MISSING-FOOTER

make_docs "$SANDBOX/age-future"
set_reviewed "$SANDBOX/age-future/docs/mcp.md" "*Last Updated: $TODAY* | *Last Reviewed: $(days_ago -30)*"
run_age "a page reviewed 30 days in the future" "$SANDBOX/age-future" FUTURE-REVIEW

make_docs "$SANDBOX/age-future2"
set_reviewed "$SANDBOX/age-future2/docs/mcp.md" "*Last Updated: $TODAY* | *Last Reviewed: $(days_ago -2)*"
run_age "a page reviewed 2 days in the future" "$SANDBOX/age-future2" FUTURE-REVIEW

# A fresh footer-shaped example above a stale real footer: the example must not stand in for it.
for wrap in fence comment; do
    make_docs "$SANDBOX/age-$wrap"
    set_reviewed "$SANDBOX/age-$wrap/docs/mcp.md" "*Last Updated: $(days_ago 400)* | *Last Reviewed: $(days_ago 300)*"
    python3 - "$SANDBOX/age-$wrap/docs/mcp.md" "$wrap" "$TODAY" <<'PY'
import sys
path, wrap, today = sys.argv[1:]
ex = f"*Last Updated: {today}* | *Last Reviewed: {today}*"
ex = f"```\n{ex}\n```" if wrap == "fence" else f"<!-- {ex} -->"
s = open(path, encoding="utf-8").read().split("\n", 1)
open(path, "w", encoding="utf-8").write(s[0] + "\n\n" + ex + "\n" + s[1])
PY
    run_age "a stale footer under a fresh example in a $wrap" "$SANDBOX/age-$wrap" STALE
done

# Pages MkDocs publishes that a walk for lower-case .md alone would miss.
STALE_PAGE="# Old\n\n*Last Updated: $(days_ago 400)* | *Last Reviewed: $(days_ago 300)*\n"
make_docs "$SANDBOX/age-ext"
printf "$STALE_PAGE" >"$SANDBOX/age-ext/docs/old.markdown"
run_age "a stale page saved as .markdown" "$SANDBOX/age-ext" STALE docs/old.markdown
make_docs "$SANDBOX/age-upper"
printf "$STALE_PAGE" >"$SANDBOX/age-upper/docs/OLD.MD"
run_age "a stale page saved as .MD" "$SANDBOX/age-upper" STALE docs/OLD.MD
make_docs "$SANDBOX/age-link"
mkdir -p "$SANDBOX/linked" && printf "$STALE_PAGE" >"$SANDBOX/linked/old.md"
ln -s "$SANDBOX/linked" "$SANDBOX/age-link/docs/linked"
run_age "a stale page in a symlinked directory" "$SANDBOX/age-link" STALE docs/linked/old.md
make_docs "$SANDBOX/age-nm"
mkdir -p "$SANDBOX/age-nm/docs/node_modules" && printf "$STALE_PAGE" >"$SANDBOX/age-nm/docs/node_modules/old.md"
run_age "a stale page under node_modules/" "$SANDBOX/age-nm" STALE docs/node_modules/old.md

# Narrowness controls: the real pages; a page on the limit itself with an old Last Updated --
# only the review date is judged, and a page can be right and unchanged for years; and a page
# dated tomorrow, which is the one day of slack for a local-date footer checked in UTC.
make_docs "$SANDBOX/age-edge"
set_reviewed "$SANDBOX/age-edge/docs/mcp.md" "*Last Updated: $(days_ago 900)* | *Last Reviewed: $(days_ago 180)*"
make_docs "$SANDBOX/age-tomorrow"
set_reviewed "$SANDBOX/age-tomorrow/docs/mcp.md" "*Last Updated: $TODAY* | *Last Reviewed: $(days_ago -1)*"
for d in "$REPO_ROOT" "$SANDBOX/age-edge" "$SANDBOX/age-tomorrow"; do
    OUT=$(cd "$d" && $AGE_CMD 2>&1)
    if [ $? -eq 0 ]; then
        pass "review-age gate accepts ${d##*/} (narrowness control)"
    else
        fail "review-age gate refused ${d##*/}: $(head -4 <<<"$OUT" | tr '\n' ' ')"
    fi
done

# ---------------------------------------------------------------------------
# 5. check-required-contexts.sh, check 1 — a required context no job emits.
#
# The failure mode is silent and permanent: the context never gets a check run, so it stays
# pending and the PR can never merge (#1380/#1386). The gate reads its workflow paths relative
# to the working directory, so a sandbox containing empty workflow files reproduces it without
# touching the real .github/.
# ---------------------------------------------------------------------------
CTX="$REPO_ROOT/scripts/check-required-contexts.sh"
mkdir -p "$SANDBOX/ci/.github/workflows"
: >"$SANDBOX/ci/.github/workflows/ci.yml"
: >"$SANDBOX/ci/.github/workflows/e2e-sso.yml"
: >"$SANDBOX/ci/.github/workflows/issue-link-check.yml"

OUT=$(cd "$SANDBOX/ci" && bash "$CTX" --offline 2>&1)
RC=$?
cd "$REPO_ROOT" || exit 1
if [ "$RC" -eq 0 ]; then
    fail "check-required-contexts.sh PASSED against workflow files that emit no jobs at all.
        Every required context would stay pending forever."
elif echo "$OUT" | grep -q "required context 'CI Gate' is not produced by any job"; then
    pass "check-required-contexts.sh fires when a required context has no job emitting it"
else
    fail "check-required-contexts.sh exited $RC without reporting an unemitted context, so
        check 1 was not what failed: $(echo "$OUT" | head -3 | tr '\n' ' ')"
fi

OUT=$(bash "$CTX" --offline 2>&1)
if [ $? -eq 0 ]; then
    pass "check-required-contexts.sh passes against the real workflows (narrowness control)"
else
    fail "check-required-contexts.sh fails on a clean checkout: $(echo "$OUT" | head -3 | tr '\n' ' ')"
fi

# ---------------------------------------------------------------------------
# 6. check-docs-routing.py — a landing page without the community-project notice above its
#    first `##` heading (#2385). It reads README.md, docs/ and mkdocs.yml from the working
#    directory, so the fixture is a copy of the real three, then one mutation each.
# ---------------------------------------------------------------------------
ROUTING="$REPO_ROOT/scripts/check-docs-routing.py"
make_landing() { # make_landing <dir>
    mkdir -p "$1"
    cp -R "$REPO_ROOT/docs" "$REPO_ROOT/README.md" "$REPO_ROOT/mkdocs.yml" "$1/"
}
run_routing() { # run_routing <label> <dir>
    OUT=$(cd "$2" && python3 "$ROUTING" 2>&1)
    RC=$?
    if [ "$RC" -eq 0 ]; then
        fail "check-docs-routing.py PASSED $1"
    elif grep -q 'NO-NOTICE' <<<"$OUT"; then
        pass "check-docs-routing.py fires on $1"
    else
        fail "check-docs-routing.py exited $RC on $1 without reporting NO-NOTICE: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi
}

make_landing "$SANDBOX/land-missing"
python3 - "$REPO_ROOT/README.md" "$SANDBOX/land-missing/README.md" <<'PY'
import sys
src, dst = sys.argv[1:]
lines = open(src, encoding="utf-8").read().split("\n")
i = next(n for n, l in enumerate(lines) if "**Community project**" in l)
del lines[i - 1:i + 1]                         # the alert marker and its notice line
open(dst, "w", encoding="utf-8").write("\n".join(lines))
PY
run_routing "a README without the notice" "$SANDBOX/land-missing"

make_landing "$SANDBOX/land-below"
python3 - "$SANDBOX/land-below/docs/README.md" <<'PY'
import sys
path = sys.argv[1]
lines = open(path, encoding="utf-8").read().split("\n")
i = next(n for n, l in enumerate(lines) if "Community project" in l)
notice = lines[i - 1:i + 1]                   # the [!NOTE] marker and its notice line
rest = lines[:i - 1] + lines[i + 1:]
first = next(i for i, l in enumerate(rest) if l.startswith("## "))
rest[first + 1:first + 1] = [""] + notice
open(path, "w", encoding="utf-8").write("\n".join(rest))
PY
run_routing "a docs home whose notice is below its first heading" "$SANDBOX/land-below"

# move_notice <file> <where>: before-h1 | before-description | below-rule | plain | unalerted | important
move_notice() {
    python3 - "$1" "$2" <<'PY'
import sys
path, where = sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().split("\n")
i = next(n for n, l in enumerate(lines) if "**Community project**" in l)
notice = lines[i - 1:i + 1]                    # the [!NOTE] marker and its notice line
assert notice[0] == "> [!NOTE]", notice
if where == "plain":
    lines[i - 1:i + 1] = [notice[1][2:]]       # no marker, no "> ": a paragraph, not a callout
elif where == "unalerted":
    del lines[i - 1]                           # the pre-#2393 plain quote: no [!NOTE] marker
elif where == "important":
    lines[i - 1] = "> [!IMPORTANT]"            # the pre-#2393 hub alert type
else:
    del lines[i - 1:i + 1]
    if lines[i - 2] == "":
        del lines[i - 2]
    if where == "before-h1":
        lines[0:0] = notice + [""]
    elif where == "before-description":
        lines[1:1] = [""] + notice
    elif where == "below-rule":
        r = next(n for n, l in enumerate(lines) if l == "---")
        lines[r + 1:r + 1] = [""] + notice
open(path, "w", encoding="utf-8").write("\n".join(lines))
PY
}
for where in before-h1 before-description below-rule plain unalerted; do
    make_landing "$SANDBOX/land-$where"
    move_notice "$SANDBOX/land-$where/README.md" "$where"
    run_routing "a README notice moved $where" "$SANDBOX/land-$where"
done
make_landing "$SANDBOX/land-important"
move_notice "$SANDBOX/land-important/docs/README.md" important
run_routing "a docs home notice still written as [!IMPORTANT]" "$SANDBOX/land-important"

# The README's closing section must hold docs/about.md's body verbatim (#2387).
run_drift() { # run_drift <label> <dir>
    OUT=$(cd "$2" && python3 "$ROUTING" 2>&1)
    RC=$?
    if [ "$RC" -eq 0 ]; then
        fail "check-docs-routing.py PASSED $1"
    elif grep -q 'DISCLAIMER-DRIFT' <<<"$OUT"; then
        pass "check-docs-routing.py fires on $1"
    else
        fail "check-docs-routing.py exited $RC on $1 without reporting DISCLAIMER-DRIFT: $(echo "$OUT" | head -2 | tr '\n' ' ')"
    fi
}

make_landing "$SANDBOX/drift-missing"
python3 - "$SANDBOX/drift-missing/README.md" <<'PY'
import sys
path = sys.argv[1]
s = open(path, encoding="utf-8").read()
head, foot = s.split("<!-- markdownlint-disable MD049 -->")
head = head[:head.index("\n---\n\n## About this project")] + "\n\n"
open(path, "w", encoding="utf-8").write(head + "<!-- markdownlint-disable MD049 -->" + foot)
PY
run_drift "a README without the closing disclaimer" "$SANDBOX/drift-missing"

make_landing "$SANDBOX/drift-about"
sed 's/with the maintainer accountable for what ships/and reviewed by the maintainer/' \
    "$REPO_ROOT/docs/about.md" >"$SANDBOX/drift-about/docs/about.md"
run_drift "an About page reworded without the README" "$SANDBOX/drift-about"

make_landing "$SANDBOX/drift-moved"
python3 - "$SANDBOX/drift-moved/README.md" <<'PY'
import sys
path = sys.argv[1]
s = open(path, encoding="utf-8").read()
start = s.index("\n---\n\n## About this project")
end = s.index("<!-- markdownlint-disable MD049 -->")
section = s[start:end].rstrip() + "\n"
s = s[:start] + "\n\n" + s[end:]
s = s.replace("\n---\n\n## License", section + "\n---\n\n## License", 1)
open(path, "w", encoding="utf-8").write(s)
PY
run_drift "a closing disclaimer moved above License" "$SANDBOX/drift-moved"

# disclaimer_edit <file> <how>: wrap the About section's paragraphs in a comment, append a
# contradicting paragraph, or reverse them.
disclaimer_edit() {
    python3 - "$1" "$2" <<'PY'
import sys
path, how = sys.argv[1], sys.argv[2]
s = open(path, encoding="utf-8").read()
head, foot = s.split("<!-- markdownlint-disable MD049 -->")
pre, section = head.split("## About this project\n", 1)
paras = [p for p in section.strip().split("\n\n")]
if how == "comment":
    paras = ["<!--"] + paras + ["-->"]
elif how == "extra":
    paras = paras + ["Actually, this is an official, supported Liferay product."]
elif how == "reorder":
    paras = paras[::-1]
section = "\n\n".join(paras) + "\n\n\n"
open(path, "w", encoding="utf-8").write(pre + "## About this project\n\n" + section
                                        + "<!-- markdownlint-disable MD049 -->" + foot)
PY
}
for how in comment extra reorder; do
    make_landing "$SANDBOX/drift-$how"
    disclaimer_edit "$SANDBOX/drift-$how/README.md" "$how"
    run_drift "a closing disclaimer edited: $how" "$SANDBOX/drift-$how"
done

make_landing "$SANDBOX/land-good"
OUT=$(cd "$SANDBOX/land-good" && python3 "$ROUTING" 2>&1)
if [ $? -eq 0 ]; then
    pass "check-docs-routing.py accepts the real landing pages (narrowness control)"
else
    fail "check-docs-routing.py refused the real landing pages: $(echo "$OUT" | head -3 | tr '\n' ' ')"
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
