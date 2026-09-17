---
name: edge-sync
description: Architectural rules for state synchronization between the Control Plane and Edge Nodes. Activate this skill when modifying active tunnel states (e.g. kicks, rate limits).
---

# Edge Node Propagation & State Synchronization

- **Stateless Edge Nodes**: Regional Edge nodes (`lfr-tunneld` running with no DB) rely entirely on the Control Plane for authentication and validation. However, they maintain their own active memory `registry` of live tunnels.
- **State Changes**: ANY feature or API endpoint that modifies the active tunnel lease state in memory on the Control Plane (e.g., custom headers, rate limits, kicks) **MUST** include logic to propagate that state change to the specific Edge Node hosting the tunnel via the `edge_control_ws.go` WebSocket channel. Failure to do so will result in split-brain behavior where Edge nodes do not enforce the new policies.
- **The channel runs both ways, and the upward direction is the one that gets forgotten.** Anything an Edge node MEASURES rather than enforces -- bandwidth, counters, anything accumulated in its memory registry -- has nowhere to live: an Edge has no database. It **MUST** be reported to the Control Plane on the same `edge_control_ws.go` connection, not on a second one, and it **MUST** be reported as a DELTA that is taken exactly once (`Registry.TakeByteDeltas`), so a reconnecting node cannot replay it. Edge bandwidth went unrecorded for the entire life of the edge fleet because nothing did this (#1958), and it read as light usage rather than as missing measurement.
- **The same rule covers evidence, not just measurements (#1991).** A diagnostics collection
  request reaches an edge-served client only because central forwards it down this channel and the
  client's acknowledgement comes back up it -- and the ack has to, because the audit log lives on
  central and an ack an edge cannot relay is an ack that never happened. Two directions, one
  connection. Note what did NOT ride it: the collected bundle is up to 6 MB against a 128 KB read
  limit, so it goes over HTTP to `/api/internal/edge-diagnostics` with the edge token, the same
  way `/api/internal/edge-audit-log` already does. **Size decides the transport; the control
  channel decides the protocol.**
- **Every Edge is powered off nightly (00:00-08:00 local).** Anything held only in an Edge's memory is gone at that point unless it was flushed. A new accumulator needs a flush on the graceful stop AND on the two "I am going away" signals -- the drain announcement (`handleLocalDrain`) and the scheduled-shutdown warning -- and a stated bound on what an ungraceful stop still loses.
- **Telemetry must never be able to drop a tunnel.** The control channel also carries kicks, schedules and blacklist pushes. Parse inbound frames best-effort and keep the connection on a failure, and keep frames well inside the read limit -- gorilla closes a connection that exceeds it.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-17* | *Last Reviewed: 2026-09-17*
