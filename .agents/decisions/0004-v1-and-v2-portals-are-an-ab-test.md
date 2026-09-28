STATUS: Accepted 2026-09-16

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

`make check-portal-parity` (`scripts/check-portal-parity.cjs`), plus
`make check-access-mode-parity` and `make check-tunnel-grouping` for two specific surfaces.

**Known limit of that gate, stated so it is not mistaken for coverage:** it is a source scan, and a
source-grep gate cannot assert that a function is *called*. It has been green on defects it was
written to catch — needles satisfied by an interface field, a column definition, a `let`, and by a
function's own definition while its call site was commented out. Behavioural coverage lives in
`tests/e2e/ui/tests/`.

## Revisit when

The A/B test concludes and one arm is chosen. At that point this becomes a deprecation, and the
parity gates should be retired rather than left enforcing symmetry nobody wants.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
