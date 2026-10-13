#!/usr/bin/env bash
# test-skill-routing.sh — tests scripts/check-skill-routing.py (#2363)
#
# The gate asserts what AGENTS.md's rules-integrity section only states: every skill is routed
# from both of AGENTS.md's routing sections, its frontmatter is valid, every link to a skill
# resolves, and the .claude/skills discovery symlink exists. Each case below breaks exactly one
# of those in a copy of the real tree and requires the gate to name THAT problem -- an assertion
# on the exit code alone would pass a case that failed for some other reason.
#
# The first case runs the gate against the repository itself, which is what makes `make
# test-hooks` (and so the Hook Tests job, on every PR) enforce it.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-skill-routing.py"

[ -f "$GATE" ] || { echo "FATAL: $GATE missing"; exit 1; }
# The gate the cases RUN. Overridable so a control can run every case against an older or
# deliberately broken gate, while fixtures are still built with the real one.
UNDER_TEST="${LFT_GATE_UNDER_TEST:-$GATE}"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
TREE="$WORK/tree"

# fresh_tree — a copy of the real routing surface: AGENTS.md, CLAUDE.md, the skills, the symlink.
fresh_tree() {
    rm -rf "$TREE"
    mkdir -p "$TREE/.agents" "$TREE/.claude"
    cp "$REPO_ROOT/AGENTS.md" "$REPO_ROOT/CLAUDE.md" "$TREE/"
    cp -R "$REPO_ROOT/.agents/skills" "$TREE/.agents/skills"
    ln -s ../.agents/skills "$TREE/.claude/skills"
    # Everything else AGENTS.md and CLAUDE.md link to, derived with the gate's own parser so a
    # new link in either file cannot make every case fail for the wrong reason.
    python3 - "$GATE" "$REPO_ROOT" "$TREE" <<'PY'
import importlib.util, os, re, shutil, sys
gate, src, dst = sys.argv[1:4]
spec = importlib.util.spec_from_file_location("gate", gate)
g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)
for doc in ("AGENTS.md", "CLAUDE.md"):
    for target, _ in g.link_targets(open(os.path.join(src, doc), encoding="utf-8").read()):
        if re.match(r"^([a-z][a-z0-9+.-]*:|#|/)", target, re.I):
            continue
        rel = target.split("#", 1)[0]
        s, d = os.path.join(src, rel), os.path.join(dst, rel)
        if not rel or os.path.exists(d) or not os.path.exists(s):
            continue
        os.makedirs(os.path.dirname(d) or dst, exist_ok=True)
        (shutil.copytree if os.path.isdir(s) else shutil.copy)(s, d)
PY
}

# edit <file> <python expression over s> — rewrite a fixture file in place.
edit() {
    python3 - "$1" "$2" <<'PY'
import sys
p, expr = sys.argv[1], sys.argv[2]
s = open(p).read()
new = eval(expr, {"s": s})
assert new != s, "fixture edit changed nothing -- the case would test the unbroken tree"
open(p, "w").write(new)
PY
}

# run_case <description> <expect: ok|refused> [needle]
run_case() {
    local desc="$1" expect="$2" needle="${3:-}" out rc
    out=$(python3 "$UNDER_TEST" --root "$TREE" 2>&1)
    rc=$?
    if [ "$expect" = ok ]; then
        if [ "$rc" -eq 0 ]; then pass "$desc"; return; fi
        fail "$desc -- expected a pass, got exit $rc"
    else
        if [ "$rc" -eq 1 ] && grep -qF "$needle" <<<"$out"; then pass "$desc"; return; fi
        fail "$desc -- expected exit 1 naming '$needle', got exit $rc"
    fi
    printf '%s\n' "$out" | sed 's/^/        /'
}

echo "Testing scripts/check-skill-routing.py"
echo ""

# --- the repository itself ---------------------------------------------------------------
out=$(cd "$REPO_ROOT" && python3 "$GATE" 2>&1); rc=$?
if [ "$rc" -eq 0 ]; then pass "the repository's own skills are fully routed"
else fail "the repository's own skills are not fully routed (exit $rc)"; printf '%s\n' "$out" | sed 's/^/        /'; fi

fresh_tree
run_case "an unmodified copy of the tree passes" ok

# --- 1. a skill directory with no SKILL.md -------------------------------------------------
fresh_tree; mkdir "$TREE/.agents/skills/ghost"
run_case "a skill directory without SKILL.md is refused" refused ".agents/skills/ghost/ has no SKILL.md"

# --- 2. frontmatter ------------------------------------------------------------------------
fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" 's.replace("name: edge-sync", "name: edge-sync-old", 1)'
run_case "a name that does not match its directory is refused" refused "frontmatter name is 'edge-sync-old'"

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" '__import__("re").sub(r"(?m)^description:.*$", "description:", s, count=1)'
run_case "an empty description is refused" refused "edge-sync/SKILL.md: description is missing or empty"

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" '__import__("re").sub(r"(?m)^description:.*$", "description: >\n  Folded across\n  two lines.", s, count=1)'
run_case "a folded (>) description is read, not taken as empty" ok

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" '"---\n" + s[4:].replace("\n---\n", "\n", 1)'
run_case "a SKILL.md without a closed frontmatter block is refused" refused "edge-sync/SKILL.md has no closed --- frontmatter block"

# --- 3. routed from both sections ----------------------------------------------------------
# Drop edge-sync's link from ONE section only, so the case cannot pass on the other.
fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + s[i:j].replace("(.agents/skills/edge-sync/SKILL.md)", "(.agents/skills/edr-constraints/SKILL.md)") + s[j:])(s.index("## Preflight"), s.index("## Rules, by topic"))'
run_case "a skill missing from Preflight is refused" refused "skill 'edge-sync' is not linked from AGENTS.md '## Preflight'"

fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + s[i:j].replace(".agents/skills/edge-sync/SKILL.md", ".agents/skills/edr-constraints/SKILL.md") + s[j:])(s.index("## Rules, by topic"), s.index("## Groundedness"))'
run_case "a skill missing from Rules, by topic is refused" refused "skill 'edge-sync' is not linked from AGENTS.md '## Rules, by topic'"

# --- 4. dangling links ---------------------------------------------------------------------
fresh_tree; printf '\nSee [old](.agents/skills/retired-skill/SKILL.md).\n' >> "$TREE/AGENTS.md"
run_case "an AGENTS.md link to a missing skill is refused" refused "links .agents/skills/retired-skill/SKILL.md, which does not exist"

fresh_tree; printf '\nSee [old](.agents/skills/retired-skill/SKILL.md).\n' >> "$TREE/CLAUDE.md"
run_case "a CLAUDE.md link to a missing skill is refused" refused "CLAUDE.md:"

# --- 5. the discovery symlink --------------------------------------------------------------
fresh_tree; rm "$TREE/.claude/skills"
run_case "a missing .claude/skills symlink is refused" refused ".claude/skills is not a symlink"

fresh_tree; rm "$TREE/.claude/skills"; mkdir -p "$TREE/elsewhere"; ln -s ../elsewhere "$TREE/.claude/skills"
run_case "a .claude/skills symlink pointing elsewhere is refused" refused ".claude/skills resolves to"

# --- review of #2364: forms that used to pass on a broken tree -----------------------------
# The Rules section's link TEXT is the skill path, so only the href is broken here. Matching
# raw text instead of link targets let every one of these through.
fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + s[i:j].replace("](.agents/skills/edge-sync/SKILL.md)", "](.agents/skills/edge-sync/SKIL.md)") + s[j:])(s.index("## Rules, by topic"), s.index("## Groundedness"))'
run_case "a typo'd href under correct link text is refused" refused "links .agents/skills/edge-sync/SKIL.md, which does not exist"

fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + s[i:j].replace("](.agents/skills/edge-sync/SKILL.md)", "](.agents/skill/edge-sync/SKILL.md)") + s[j:])(s.index("## Rules, by topic"), s.index("## Groundedness"))'
run_case "an href into a misspelt directory is refused" refused "links .agents/skill/edge-sync/SKILL.md, which does not exist"

# A commented-out or fenced row is not routing anything.
fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + __import__("re").sub(r"(?m)^(\|[^\n]*\(\.agents/skills/edge-sync/SKILL\.md\)[^\n]*)$", r"<!-- \1 -->", s[i:j]) + s[j:])(s.index("## Preflight"), s.index("## Rules, by topic"))'
run_case "a commented-out Preflight row does not count as routed" refused "skill 'edge-sync' is not linked from AGENTS.md '## Preflight'"

fresh_tree; edit "$TREE/AGENTS.md" '(lambda i, j: s[:i] + __import__("re").sub(r"(?m)^(\|[^\n]*\(\.agents/skills/edge-sync/SKILL\.md\)[^\n]*)$", "```\n\\1\n```", s[i:j]) + s[j:])(s.index("## Preflight"), s.index("## Rules, by topic"))'
run_case "a Preflight row inside a code fence does not count as routed" refused "skill 'edge-sync' is not linked from AGENTS.md '## Preflight'"

fresh_tree; rm -rf "$TREE/.agents/skills/zombie"; ln -s /nonexistent "$TREE/.agents/skills/zombie"
run_case "a skill directory that is a dangling symlink is refused" refused ".agents/skills/zombie is a symlink to nothing"

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" '__import__("re").sub(r"(?m)^description:.*$", "description: null", s, count=1)'
run_case "a null description counts as empty" refused "edge-sync/SKILL.md: description is missing or empty"

# --- review of #2364: valid forms that used to be refused ----------------------------------
fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" 's.replace("name: edge-sync\n", "name: edge-sync\nallowed-tools:\n- Read\n- Grep\n", 1)'
run_case "an unindented YAML list in frontmatter is accepted" ok

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" 's.replace("name: edge-sync\n", "name: edge-sync  # matches the directory\n", 1)'
run_case "an inline comment after a value is not part of the value" ok

fresh_tree; edit "$TREE/.agents/skills/edge-sync/SKILL.md" '"﻿" + s'
run_case "a leading byte-order mark is not a missing frontmatter block" ok

fresh_tree
out=$(LFT_SKILLS_MIN=six python3 "$UNDER_TEST" --root "$TREE" 2>&1); rc=$?
if [ "$rc" -eq 2 ] && grep -qF "LFT_SKILLS_MIN must be an integer" <<<"$out"; then pass "a non-integer floor is a configuration error (exit 2), not a traceback"
else fail "a non-integer floor gave exit $rc"; printf '%s\n' "$out" | sed 's/^/        /'; fi

# --- 6. anti-vacuity floor -----------------------------------------------------------------
fresh_tree
out=$(LFT_SKILLS_MIN=99 python3 "$UNDER_TEST" --root "$TREE" 2>&1); rc=$?
if [ "$rc" -eq 1 ] && grep -qF "below the floor of 99" <<<"$out"; then pass "fewer skills than the floor is refused"
else fail "fewer skills than the floor was not refused (exit $rc)"; printf '%s\n' "$out" | sed 's/^/        /'; fi

echo ""
echo "  passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
