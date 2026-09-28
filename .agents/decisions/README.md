# Decisions

A decision record answers **why a choice was made, what it cost, and when to revisit it** — for
choices that have **no rule to attach them to**.

That last clause is the whole design. This directory exists because three kinds of knowledge were
being written to two homes, and one kind had none:

| Kind | Answers | Home |
|---|---|---|
| **Constraints** | what an agent must / must not do | `.agents/skills/<topic>/SKILL.md`, routed by [`/AGENTS.md`](../../AGENTS.md) |
| **State** | what is in flight right now | `.agent-state.md` (gitignored, local) |
| **Decisions** | why a choice was made, what it cost, when to revisit | **here** |

## The admission test

Before adding a record, apply it:

- Can the decision be stated as **"always do X"** or **"never do X"**? Then it is a **constraint**.
  It belongs in the skill that owns X, with its reasoning beside it. Do not write it here.
- Does it name something **currently in progress** — a branch, an open PR, a release being cut?
  Then it is **state**. It belongs in `.agent-state.md`.
- Is it a choice that closed off alternatives, carries an ongoing cost, and that someone with no
  context would reasonably reopen? **That is a decision. Write it here.**

If a record fails the test, the fix is to move it, not to widen the test.

**This directory does not compete with the skills, and must not start.** `/AGENTS.md` already puts
the "why" next to the rule and does it well — why `core.hooksPath` is rejected, why `golangci-lint`
is deliberately not installed, why `make ops-bin` replaced a bare `go build`. Those are constraints
with their reasoning attached, they are correctly filed, and pulling them in here would be the
one-rule-one-home violation (#1412) that gutted `CLAUDE.md`.

## Format

One decision per file, `NNNN-kebab-slug.md`, four-digit sequence, and keep it under about 40 lines.
A record that needs more than that is usually two decisions, or is restating a rule it should link to.

```markdown
STATUS: Accepted 2026-09-16

# NNNN. Title as a statement, not a question

## Decision
One sentence. Use the owner's own words where they exist, quoted.

## Cost
What this trades away. Not "considered alternatives" — the price actually being paid.

## Enforced by
The rule, gate, test, config key or CI check that makes this real, by path.
Or the literal string `unenforced by design`, which is a legitimate answer.

## Revisit when
The condition that reopens this. What would have to become true.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: YYYY-MM-DD* | *Last Reviewed: YYYY-MM-DD*
```

Five rules about the content:

1. **`STATUS:` is line 1**, before the heading, so a superseded record announces itself in the first
   line anyone reads — including in a `head -1` across the directory. One of:
   `Accepted <date>` · `Superseded by NNNN <date>` · `Reversed <date>`.
2. **Never restate a rule.** Link to `.agents/skills/<topic>/SKILL.md` or to `file.go:line`. A record
   that copies a rule is a second home for it, and the copy is the one that goes stale.
3. **Supersede, never delete.** Edit `STATUS:` in place and add the superseding number. `.agents/gemini.md`
   is the in-tree warning about what a decision log becomes without this — it now opens with
   "This is a historical record, not current guidance ... the completed-task lists in particular record
   decisions that have since been reversed."
4. **Cite issues, PRs and file paths — never `.agent-state.md`.** That file is gitignored, so a
   citation to it cannot be followed from a fresh clone or from another machine. Losing that
   rationale on clone is the problem this directory exists to fix; do not reintroduce it as a
   reference.
5. **`Revisit when` is what separates a decision from a gag order.** "Central is a deliberate SPOF,
   don't raise it" is an instruction to stop thinking. "…revisit when SQLite's single-writer file
   stops being the binding constraint" tells the next agent what to watch for.

## What keeps this honest

`tests/hooks/test-decisions-integrity.sh`, via `make test-hooks`. Prose does not fail (see
`github-workflow` §5b rule 6), and a prose-only decision log rots exactly the way `CLAUDE.md` did.
It asserts that every record has a valid `STATUS:`, that `Enforced by` is present and every path it
names resolves, and that a `Superseded by NNNN` points at a file that exists.

## Size

If this directory passes about **15 records**, the admission test is being applied too loosely.
Decisions of this kind are rare; a folder that fills up quickly has started absorbing constraints.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
