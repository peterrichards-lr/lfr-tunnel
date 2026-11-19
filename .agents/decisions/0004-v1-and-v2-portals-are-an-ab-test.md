STATUS: Superseded by 0007 2026-11-19

# 0004. The V1 and V2 portals are an A/B test, so capability parity is mandatory

## Decision

The two portal arms are a live A/B test of the same product, not a legacy arm and a successor.
Functionality must be identical in both. **A capability present in only one arm is a defect, not a
roadmap item** — and is not to be triaged as "V1 is old".

## Cost

Every user-facing change costs twice: it must be built in a Go/HTML/vanilla-JS arm and a modern
bundled arm, and verified in both. Several defects have been exactly this asymmetry — see #2259 and
#2266, where one arm rendered an option the other could not reach.

## Enforced by

Retired by [0007](0007-v2-is-the-portal-v1-is-a-frozen-admin-only-fallback.md) on 2026-11-19
(#2529): the gate below became the V2-only `scripts/check-v2-capabilities.cjs`, and the other two
stopped reading V1. The original text follows unchanged, except that the deleted script's path is
no longer backticked, because `tests/hooks/test-decisions-integrity.sh` resolves every backticked
path in every record.

`make check-portal-parity` (scripts/check-portal-parity.cjs), plus
`make check-access-mode-parity` and `make check-tunnel-grouping` for two specific surfaces.

**Known limit of that gate, stated so it is not mistaken for coverage:**
scripts/check-portal-parity.cjs:44 is a **closed, hand-written allow-list** — `CAPABILITIES`, one
`{files, needle}` pair per arm per capability. A capability nobody added to that array is invisible
to it, which is exactly the defect class this record says is enforced. **Adding a capability means
adding an entry**, or the gate reports parity it never checked. Its boundary cases live in
`tests/hooks/test-gate-scope-boundaries.sh`; behavioural coverage is in `tests/e2e/ui/tests/`.

## Revisit when

The A/B test concludes and one arm is chosen. At that point this becomes a deprecation, and the
parity gates should be retired rather than left enforcing symmetry nobody wants.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-19* | *Last Reviewed: 2026-11-19*
