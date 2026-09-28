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

The storage layer stops being the first obstacle. Central's database is a local SQLite file opened
with a single writer (`pkg/db/db.go`; `pkg/server/quota.go:49` reasons about it in as many words),
so a clustering proposal has to say what happens to that file before node count is even the
question.

**Stated as inference, not as settled fact:** that the single-writer file is *the* binding
constraint on clustering is not written down anywhere in this repo — it is my reading of the code.
If you are here because you want to cluster central, treat it as the first thing to verify, not as
a refusal.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
