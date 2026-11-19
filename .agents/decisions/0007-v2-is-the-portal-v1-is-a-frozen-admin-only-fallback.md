STATUS: Accepted 2026-11-19

# 0007. V2 is the portal; V1 is a frozen, admin-only fallback, and parity is no longer required

## Decision

Supersedes [0004](0004-v1-and-v2-portals-are-an-ab-test.md). The A/B test is over and V2 was
chosen. The owner, 2026-11-19: "I wonder if it is time to retire v1", and then "I would still like
v1 to be available for admins with the option in case anything has been missed."

- **V2 is the portal.** New features are built in V2 only. A gap found in V2 is fixed in V2, and is
  not a reason to touch V1.
- **V1 is not deleted. It is frozen**: kept reachable for admins only, as a fallback in case
  something was missed, and otherwise left alone. The server checks for an admin session on every
  V1 request ("we need an admin check on portal v1 for sure"), at a path set in the server YAML
  rather than a guessable one. That routing is #2528, not this record.
- **The parity rule in 0004 ends**, and so do the gates that enforced V1/V2 symmetry (#2529).
- **Deleting V1 is a later, separate decision.** Epic #2524.

## Cost

A capability V2 gains is missing from the fallback, so an admin who falls back to V1 gets less than
V2 offers, and V1 drifts further behind with every release. V1's Playwright specs keep running while
V1 is served, so it still costs CI time and still fails a build if it breaks.

## Enforced by

`make check-portal-parity` was deleted (#2529). `scripts/check-tunnel-grouping.cjs` and
`scripts/check-access-mode-parity.cjs` stay, but no longer read V1: what they hold is V2's own
behaviour and V2's agreement with the Inspector and the server, not symmetry. V1 behaviour stays
covered by its specs in `tests/e2e/ui/tests/`. The freeze itself is unenforced by design: nothing
refuses an edit to `pkg/server/static/dashboard.js`.

## Revisit when

Admins stop needing the fallback (nothing has sent anyone back to V1 for a release cycle or two), or
keeping V1's specs green costs more than the safety net is worth. Then decide whether to delete V1.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-19* | *Last Reviewed: 2026-11-19*
