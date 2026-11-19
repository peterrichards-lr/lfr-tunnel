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

No longer enforced. Superseded by [0007](0007-v2-is-the-portal-v1-is-a-frozen-admin-only-fallback.md)
on 2026-11-19: V2 was chosen, and #2529 deleted `make check-portal-parity`
(scripts/check-portal-parity.cjs, a closed, hand-written allow-list of capabilities, one marker per
arm) and stopped `make check-access-mode-parity` and `make check-tunnel-grouping` reading V1.
Those paths are left unquoted because the first no longer exists. Kept as the record of why the
two arms were built twice until then.

## Revisit when

The A/B test concludes and one arm is chosen. At that point this becomes a deprecation, and the
parity gates should be retired rather than left enforcing symmetry nobody wants.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-19* | *Last Reviewed: 2026-11-19*
