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
- **Configuration central stores is configuration central must CHANGE (#2121).** A write that
  lands in central's database goes to central, always -- never to whichever gateway happens to
  hold the lease. An Edge has no database, so `validatePAT` refuses the token on `s.db == nil`
  before it is even read, and the caller is told `401 Unauthorized` about a credential that is
  perfectly good. That misdirection cost a live session to diagnose: registration on the same
  Edge, with the same token, succeeds -- because registration is the one path that was taught to
  forward (`handleEdgeRegisterProxy`).
  - **Do not fix this by forwarding more.** Proxying control-plane calls through an Edge would
    have it relaying users' personal access tokens on paths that have no need to see them, and
    every endpoint added later would inherit that by default. The client already knows central's
    address -- it is told it by the gateway and reports status to it every tick -- so it can
    simply ask central itself.
  - **Unreachable means "cannot be changed", not "is now unset".** The Edge goes on enforcing what
    it was last told: access control lives on the live lease (`SetAccessControlsForSubdomain`),
    not fetched per request. So an outage must DISABLE the control that writes it, never clear
    the value and never fail on submit -- and the message has to say the tunnel is still
    protected, or a disabled panel reads as an open tunnel.
  - **A temporary refusal must not outrank a permanent one.** A custom domain can never be edited
    from the client Inspector; reporting that as a control-plane outage tells someone to wait for
    something that will not help.
  - **"Not asked yet" is not "down".** A freshly started client has probed nothing. Disabling on
    that basis flashes the control off and on at every launch, so carry the two states separately.
- **Telemetry must never be able to drop a tunnel.** The control channel also carries kicks, schedules and blacklist pushes. Parse inbound frames best-effort and keep the connection on a failure, and keep frames well inside the read limit -- gorilla closes a connection that exceeds it.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-21* | *Last Reviewed: 2026-09-21*
