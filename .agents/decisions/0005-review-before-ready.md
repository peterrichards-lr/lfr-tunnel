STATUS: Accepted 2026-09-26

# 0005. Every PR is reviewed by a second agent before it is marked ready

## Decision

Owner's instruction, 2026-09-26:

> "Perhaps have an agent reviewing your work as you go to avoid issues."

Every PR opens as a **draft**, gets an adversarial reviewer agent, and is marked ready only once
that reviewer's findings are dealt with.

## Cost

A second agent and a second round on every PR, including small ones. Accepted because the defects
it catches are ones CI structurally cannot see. Four in its first week, all on green CI:

- **#2275** — a sixth code path to permanence (the *Reject* button in `AdminDemoteReservation`),
  in the PR whose entire subject was closing permanence leaks.
- **#2277** — the new config key was **inert on an `allowed` gateway**, the one configuration it was
  written for; every test for it ran under `disabled`.
- **#2278** — traffic suppression **destroyed** the alert rather than deferring it, so a genuinely
  dead tunnel was never reported. Reproduced at 120 heartbeats, zero alerts.
- **#2279** — V1 never loaded the policy on an MFA sign-in, so on a `force_mfa` gateway the option
  was unreachable for the whole session.

## Enforced by

`unenforced by design` — no gate can tell whether a review happened or was any good. The related
rule that *is* mechanical is `.github/workflows/issue-link-check.yml`, which fails a PR with no
closing reference.

One associated rule, learned by getting it wrong on #2279: **do not report CI as green off local
`make` gates.** Read the actual CI result.

## Revisit when

The reviewer stops finding things across a meaningful run of PRs — at which point the cost is real
and the return is not.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
