---
name: edge-sync
description: Architectural rules for state synchronization between the Control Plane and Edge Nodes. Activate this skill when modifying active tunnel states (e.g. kicks, rate limits).
---

# Edge Node Propagation & State Synchronization

- **Stateless Edge Nodes**: Regional Edge nodes (`lfr-tunneld` running with no DB) rely entirely on the Control Plane for authentication and validation. However, they maintain their own active memory `registry` of live tunnels.
- **State Changes**: ANY feature or API endpoint that modifies the active tunnel lease state in memory on the Control Plane (e.g., custom headers, rate limits, kicks) **MUST** include logic to propagate that state change to the specific Edge Node hosting the tunnel via the `edge_control_ws.go` WebSocket channel. Failure to do so will result in split-brain behavior where Edge nodes do not enforce the new policies.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
