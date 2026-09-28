STATUS: Accepted 2026-09-16

# 0002. Central is a deliberate single point of failure

## Decision

The control plane runs as one node. Its unavailability is accepted rather than designed around,
and it is not to be raised as a defect unprompted.

## Cost

When central is unreachable, configuration that central stores cannot be changed — edges go on
enforcing what they were last told, so tunnels keep serving, but no write lands. That failure mode
is the subject of a standing rule in `.agents/skills/edge-sync/SKILL.md` ("Configuration central
stores is configuration central must CHANGE", #2121), including its sharpest consequence: an edge
answers `401` about a credential that is perfectly good, because `validatePAT` refuses on
`s.db == nil` before reading it.

## Enforced by

`unenforced by design` — this is an accepted trade-off, not a rule an agent can violate. The
*consequences* are enforced: see the edge-sync skill above for what a control-plane outage must
disable rather than clear.

## Revisit when

**SQLite's single-writer file stops being the binding constraint.** That, not the node count, is
what blocks a central cluster — so a proposal to cluster central that does not first address the
storage layer is answering the wrong question.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
