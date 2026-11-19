STATUS: Accepted 2026-11-19

# 0007. V2 is the portal; V1 is a frozen, admin-only fallback, and parity is no longer required

## Decision

Supersedes [0004](0004-v1-and-v2-portals-are-an-ab-test.md). The A/B test is over and V2 was
chosen. The owner, 2026-11-19: "I wonder if it is time to retire v1", and then "I would still like
v1 to be available for admins with the option in case anything has been missed."

- **V2 is the portal.** New features are built in V2 only. A gap found in V2 is fixed in V2, and is
  not a reason to touch V1.
- **V1 is not deleted. It is frozen**: left alone, and kept as a fallback in case something was
  missed. The parity rule in 0004 ends, and so does the V1 half of every gate that enforced it
  (#2529).
- **V1 is admin-only (#2528).** V2 is at `/portal`, and `/`, `/admin` and `/portalv2` redirect
  there. V1 is served at an unadvertised path set in the server YAML (`classic_portal_path`),
  behind an admin check on every V1 request ("we need an admin check on portal v1 for sure").
  Every system email, sign-in path and the CLI link to V2.
- **Deleting V1 is a later, separate decision.** Epic #2524.

## Cost

Until #2528 ships, a V2-only feature is invisible to everyone still landing on V1, which is
everyone who follows an email, a CLI link or a bookmark; #2522's old-client banner is the first
such feature. After it, an admin who falls back to V1 gets less than V2 offers, and the gap widens
every release. V1's Playwright specs keep running while V1 is served, so it still costs CI time.

## Enforced by

The freeze is enforced by nobody: it is convention and review, and nothing refuses an edit to
`pkg/server/static/dashboard.js`. What #2529 changed is that nothing forces one either. The gates
that remain guard V2's own behaviour, not symmetry: `scripts/check-v2-capabilities.cjs`,
`scripts/check-tunnel-grouping.cjs`, `scripts/check-access-mode-parity.cjs` (with the Inspector and
the server), `scripts/check-status-vocabulary.cjs` and `scripts/check-alert-vocabulary.cjs`. V1 is
still covered by its own specs in `tests/e2e/ui/tests/`.

## Revisit when

Admins stop needing the fallback (nothing has sent anyone back to V1 for a release cycle or two), or
keeping V1's specs green costs more than the safety net is worth. Then decide whether to delete V1.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-19* | *Last Reviewed: 2026-11-19*
