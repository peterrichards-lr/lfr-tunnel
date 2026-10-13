#!/usr/bin/env python3
"""Fail when a skill is not routed from AGENTS.md, or the routing points at nothing (#2363).

AGENTS.md is the entry point every agent reads, and it routes each skill under .agents/skills/
by hand, twice: a row in the Preflight table ("about to ... load first") and a bullet under
"Rules, by topic". Both are prose, and prose does not fail (github-workflow §5b rule 6), so
this asserts what AGENTS.md's rules-integrity section only states:

  1. every directory under .agents/skills/ has a SKILL.md;
  2. its frontmatter `name` matches the directory and its `description` is non-empty -- the
     description is what Claude Code reads, through the .claude/skills symlink, to decide
     whether to load the skill at all;
  3. every skill is the TARGET of a live link -- not inside an HTML comment or a code fence --
     in BOTH the Preflight section and the Rules, by topic section;
  4. every relative link in AGENTS.md and CLAUDE.md resolves (skill links included);
  5. .claude/skills is a symlink resolving to .agents/skills (#1433) -- without it no skill
     appears in Claude Code's skill list and AGENTS.md is the only route to any of them;
  6. at least LFT_SKILLS_MIN skills were found, so a broken glob cannot pass over nothing.

The index is checked rather than generated, deliberately. AGENTS.md routes by what an agent is
ABOUT TO DO, with the reasoning attached, and no frontmatter field produces that.

Stdlib only. Usage: check-skill-routing.py [--root DIR]
"""
import os
import re
import sys

SKILLS_DIR = os.path.join(".agents", "skills")
# A skill link TARGET, exactly: .agents/skills/<name>/SKILL.md, optionally ./-prefixed or with an
# #anchor. Matched against link targets only, never against raw text -- the Rules section's link
# text IS the skill path, so matching text would let a broken href pass as routed (#2363 review).
TARGET = re.compile(r"^(?:\./)?\.agents/skills/([A-Za-z0-9_.-]+)/SKILL\.md(?:#[^\s]*)?$")
INLINE = re.compile(r"\]\(\s*<?([^)\s>]+)>?(?:\s+\"[^\"]*\")?\s*\)")
REFDEF = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?(?:\s.*)?$", re.M)


def live(text):
    """Markdown with HTML comments and fenced code blocks removed: what a reader sees as links.

    A commented-out Preflight row, or a path quoted in a fence, is not routing anything.
    """
    text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
    return re.sub(r"(?ms)^ {0,3}(```|~~~).*?^ {0,3}\1[^\n]*$", "", text)


def link_targets(text):
    """(target, line number) for every inline and reference-definition link in live markdown."""
    text = live(text)
    found = []
    for rx in (INLINE, REFDEF):
        for m in rx.finditer(text):
            found.append((m.group(1), text.count("\n", 0, m.start()) + 1))
    return found


def skill_targets(text):
    """Skill names whose SKILL.md is the target of a live link in `text`."""
    return {m.group(1) for m in (TARGET.match(t) for t, _ in link_targets(text)) if m}


def section(text, heading):
    """The body of the `## <heading>` section, up to the next `## `; None if it is absent."""
    m = re.search(r"^## " + re.escape(heading) + r".*$", text, re.M)
    if not m:
        return None
    rest = text[m.end():]
    nxt = re.search(r"^## ", rest, re.M)
    return rest[: nxt.start()] if nxt else rest


def frontmatter(path):
    """`key: value` pairs from a leading --- block. Folded/literal values (>, |) are joined.

    None when there is no block, or it is not properly closed. "Closed" is not just "a later
    --- exists": every SKILL.md ends with a `---` rule above its timestamp footer, so a block
    missing its closing delimiter would otherwise run to the footer and parse as valid. Inside
    the block every line must be a key, an indented continuation, a comment or blank; the first
    line that is none of those -- prose -- shows the block was never closed where it ends.
    """
    with open(path, encoding="utf-8-sig") as f:  # -sig: a leading BOM is not a missing block
        lines = f.read().splitlines()
    if not lines or lines[0].strip() != "---":
        return None
    out, key = {}, None
    for line in lines[1:]:
        if line.strip() == "---":
            return out
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_-]*):\s*(.*)$", line)
        if m:
            key, val = m.group(1), m.group(2).strip()
            if not val.startswith(("\"", "'")):
                val = re.sub(r"\s+#.*$", "", val)  # an inline YAML comment is not part of a value
            if val in (">", "|", ">-", "|-", "~", "null", "Null", "NULL"):
                val = ""
            out[key] = val.strip("\"'").strip()
        elif key and (line.startswith((" ", "\t")) or line.startswith("- ")):
            # Indented continuation, or an unindented block-sequence item (`- Read`), which is
            # valid YAML for list fields such as allowed-tools.
            out[key] = (out[key] + " " + line.strip()).strip()
        elif line.strip() == "" or line.lstrip().startswith("#"):
            continue
        else:
            return None  # prose inside the block: its closing --- is missing
    return None  # never closed


def main(argv):
    root = "."
    if len(argv) == 3 and argv[1] == "--root":
        root = argv[2]
    elif len(argv) != 1:
        print("usage: check-skill-routing.py [--root DIR]", file=sys.stderr)
        return 2

    errors = []
    skills_path = os.path.join(root, SKILLS_DIR)
    if not os.path.isdir(skills_path):
        print(f"ERROR: {SKILLS_DIR}/ does not exist under {root}", file=sys.stderr)
        return 1

    entries = [d for d in os.listdir(skills_path) if not d.startswith(".")]
    skills = sorted(d for d in entries if os.path.isdir(os.path.join(skills_path, d)))
    for d in sorted(entries):
        p = os.path.join(skills_path, d)
        if os.path.islink(p) and not os.path.exists(p):
            errors.append(f"{SKILLS_DIR}/{d} is a symlink to nothing -- a skill that was moved or "
                          "deleted without removing its link")

    # 1-2. Each skill directory, and its frontmatter.
    for name in skills:
        md = os.path.join(skills_path, name, "SKILL.md")
        if not os.path.isfile(md):
            errors.append(f"{SKILLS_DIR}/{name}/ has no SKILL.md")
            continue
        fm = frontmatter(md)
        if fm is None:
            errors.append(f"{SKILLS_DIR}/{name}/SKILL.md has no closed --- frontmatter block")
            continue
        if fm.get("name") != name:
            errors.append(f"{SKILLS_DIR}/{name}/SKILL.md: frontmatter name is "
                          f"{fm.get('name')!r}, but the directory is {name!r}")
        if not fm.get("description"):
            errors.append(f"{SKILLS_DIR}/{name}/SKILL.md: description is missing or empty -- "
                          "it is the text Claude Code reads to decide whether to load the skill")

    # 3. Routed from both AGENTS.md sections.
    agents_md = os.path.join(root, "AGENTS.md")
    if not os.path.isfile(agents_md):
        errors.append("AGENTS.md does not exist")
        agents = ""
    else:
        with open(agents_md, encoding="utf-8") as f:
            agents = f.read()
    for heading in ("Preflight", "Rules, by topic"):
        body = section(agents, heading)
        if body is None:
            errors.append(f"AGENTS.md has no '## {heading}' section to route skills from")
            continue
        routed = skill_targets(body)
        for name in skills:
            if name not in routed:
                errors.append(f"skill '{name}' is not linked from AGENTS.md '## {heading}' -- "
                              f"add .agents/skills/{name}/SKILL.md there")

    # 4. Every relative link in the entry-point files resolves -- not only well-formed skill
    #    paths: a typo'd href (`.agents/skill/x/SKILL.md`, `.../SKIL.md`) is exactly the link
    #    that no longer looks like a skill link, and it routes an agent to nothing.
    for doc in ("AGENTS.md", "CLAUDE.md"):
        p = os.path.join(root, doc)
        if not os.path.isfile(p):
            continue
        with open(p, encoding="utf-8") as f:
            text = f.read()
        for target, lineno in link_targets(text):
            if re.match(r"^([a-z][a-z0-9+.-]*:|#|/)", target, re.I):
                continue  # a URL, an in-page anchor, or site-absolute: not a repository path
            path = target.split("#", 1)[0]
            if path and not os.path.exists(os.path.join(root, path)):
                errors.append(f"{doc}:{lineno} links {target}, which does not exist")

    # 5. The discovery symlink.
    link = os.path.join(root, ".claude", "skills")
    if not os.path.islink(link):
        errors.append(".claude/skills is not a symlink -- Claude Code lists project skills only "
                      "from there (#1433); recreate it: ln -s ../.agents/skills .claude/skills")
    elif os.path.realpath(link) != os.path.realpath(skills_path):
        errors.append(f".claude/skills resolves to {os.path.realpath(link)}, "
                      f"not {SKILLS_DIR}")

    if errors:
        for e in errors:
            print(f"ERROR: {e}", file=sys.stderr)
        return 1

    # 6. Anti-vacuity, last so it never pre-empts a specific finding (the #1779 ordering).
    try:
        min_skills = int(os.environ.get("LFT_SKILLS_MIN", "6"))
    except ValueError:
        print("ERROR: LFT_SKILLS_MIN must be an integer", file=sys.stderr)
        return 2
    if len(skills) < min_skills:
        print(f"ERROR: found {len(skills)} skill(s) under {SKILLS_DIR}/, below the floor of "
              f"{min_skills}. If one was removed deliberately, lower LFT_SKILLS_MIN in the same "
              "change.", file=sys.stderr)
        return 1

    print(f"✅ {len(skills)} skill(s): each has valid frontmatter, is routed from both AGENTS.md "
          "sections, every link to one resolves, and .claude/skills points at them.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
