# Model Context Protocol (MCP) Server Integration

The `lfr-tunnel` client ships an MCP server. It lets an AI agent in Cursor, Windsurf or Google
Antigravity IDE drive a tunnel directly: read its status, start and stop it, and inspect or replay
the HTTP requests flowing through it.

It arrived in **v1.15.0** (#180) and has been in every release since. This page describes what the
`mcp` subcommand actually does today — see *Gaps* at the end for the parts of the original design
that were never built.

---

## 1. Architecture Overview

The MCP server runs inside the local `lfr-tunnel` client process. It uses a **stdio transport**
layer to exchange JSON-RPC 2.0 messages with the calling AI development environment.

```
┌─────────────────────────────────┐
│     AI Coding Assistant         │
│  (Cursor, Antigravity IDE, etc) │
└────────────────┬────────────────┘
                 │
                 │ stdio (JSON-RPC)
                 ▼
┌─────────────────────────────────┐
│       lfr-tunnel CLI            │
│  (Running in MCP Server Mode)   │
├─────────────────────────────────┤
│                                 │
│  ┌───────────────┐              │
│  │  MCP Server   │              │
│  └───────┬───────┘              │
│          │                      │
│          ▼                      │
│  ┌───────────────┐              │
│  │ Local Client  │              │
│  │  Controller   │              │
│  └───────┬───────┘              │
│          │                      │
│          ├──────────────┐       │
│          ▼              ▼       │
│    ┌───────────┐  ┌───────────┐ │
│    │ Config/DB │  │Interceptor│ │
│    └───────────┘  └───────────┘ │
└─────────────────────────────────┘
```

**The server does not hold the tunnel.** `start_tunnel` and `stop_tunnel` re-invoke the client
binary as a subprocess, and the tunnel's own state file is what `get_tunnel_status` reads back. So
the MCP process can be restarted without dropping a tunnel, and a tunnel started from the terminal
is visible to the agent.

**Two tools talk to the inspector over HTTP, not to the tunnel.** `list_requests` and
`replay_request` call `/api/state` and `/api/replay` on the inspector of the first active tunnel.
If no tunnel is running, or its inspector is not reachable, both return an error rather than an
empty result — see each tool below.

---

## 2. CLI Execution Command

The MCP server is started by running the `mcp` subcommand:

```bash
lfr-tunnel mcp
```

This starts a persistent process that listens for JSON-RPC messages on standard input (`stdin`) and
writes responses to standard output (`stdout`). Log messages are written to standard error
(`stderr`) to prevent corrupting the standard output stream.

Wired at `cmd/lfr-tunnel/main.go:117` (subcommand list) and `:1207` (dispatch); implemented in
`pkg/mcp/server.go`.

---

## 3. Tool Specifications

Five tools are advertised in the `tools/list` response.

### A. `get_tunnel_status`

Returns every active tunnel the client can see. It enumerates the state files on disk, drops any
whose PID is no longer running, and enriches each survivor with a live query to its inspector.

* **Arguments**: None
* **Response**:
  ```json
  {
    "active_tunnels": [
      {
        "pid": 51234,
        "subdomain": "peterrichards-se",
        "public_urls": [
          "https://peterrichards-se.lfr-demo.se",
          "https://peterrichards-se-3000.lfr-demo.se"
        ],
        "ports": [8080, 3000],
        "start_time": "2026-10-01T09:14:02Z",
        "inspector_url": "http://127.0.0.1:4040",
        "live_status": { }
      }
    ]
  }
  ```

`active_tunnels` is `[]` when nothing is running — that is the "not connected" answer; there is no
`connected` field. `live_status` carries whatever the inspector reported, and is empty if the
inspector did not answer.

---

### B. `start_tunnel`

Spawns a new background tunnel by re-invoking the client binary. Returns as soon as the child is
running, which may be before the tunnel has registered.

* **Arguments** (all optional):
  * `subdomain` (string): The requested subdomain prefix.
  * `ports` (string): **Comma-separated**, e.g. `"8080,3000"` — a string, not an array. Omitted,
    the client scans the local Liferay Workspace.
  * `target_host` (string): Local hostname or IP to route traffic to.
* **Response** once the tunnel has registered:
  ```json
  {
    "status": "success",
    "pid": 51234,
    "subdomain": "peterrichards-se",
    "public_urls": ["https://peterrichards-se.lfr-demo.se"]
  }
  ```
* **Response** if it has not registered yet:
  ```json
  {
    "status": "pending",
    "message": "Tunnel spawned in background, status unknown.",
    "pid": 51234
  }
  ```

`pending` is not a failure. Call `get_tunnel_status` to find out how it settled.

---

### C. `stop_tunnel`

Terminates background tunnels by invoking the client's `-stop`.

* **Arguments**:
  * `subdomain` (string, optional): The tunnel to stop. **If omitted, stops all active tunnels.**
* **Response**:
  ```json
  {
    "status": "success",
    "message": "Stopped tunnel peterrichards-se (pid 51234)"
  }
  ```

`message` is the client's own output, passed through verbatim. Treat it as human-readable text, not
as a schema.

---

### D. `list_requests`

Retrieves the rolling request log from the inspector of the first active tunnel. This helps the
agent debug headers, payloads, and response statuses.

* **Arguments**:
  * `limit` (integer, optional): Maximum number of requests to return. Default `10`. Anything `<= 0`
    becomes `10`. **There is no upper cap.**
* **Response**:
  ```json
  {
    "requests": [ ]
  }
  ```

The objects in `requests` come from the inspector's `/api/state` history and this server passes them
through untouched — their shape is the inspector's to define, not this server's. **Requires a
running tunnel**: with none, the call returns an error, not an empty list.

---

### E. `replay_request`

Asks the inspector to replay a previously logged request against the local target host.

* **Arguments**:
  * `request_id` (string, **required**): The ID of the request to replay.
* **Response**: whatever the inspector's `/api/replay` returned, passed through unchanged.

**Requires a running tunnel**, as above. A non-200 from the inspector surfaces as an error carrying
the inspector's status code and body.

---

## 4. MCP Server Registration

To register the `lfr-tunnel` MCP server in AI tools, add the following configuration:

### VS Code (Cursor / Windsurf / Antigravity IDE)
Add this to your `mcp` settings:

```json
{
  "mcpServers": {
    "lfr-tunnel": {
      "command": "lfr-tunnel",
      "args": ["mcp"]
    }
  }
}
```

`command` must resolve on your `PATH`. If you installed to `~/liferay/lfr-tunnel/` and that is not
on `PATH`, give the absolute path instead.

---

## 5. Gaps against the original design

This page was written as a design proposal and kept its proposal wording until #2334, long after the
server shipped. Several things it specified were never built, and an agent briefed from the old text
would have called them:

| Specified | Actually |
| --- | --- |
| `get_tunnel_status` returns `connected`, `server_url`, `target_host`, `rate_limit`, `os`, `version` | returns `active_tunnels` and nothing else |
| `active_tunnels[]` entries are `{local_port, public_url}` | `{pid, subdomain, public_urls, ports, start_time, inspector_url, live_status}` |
| `start_tunnel` takes `ports` as an array of integers | takes a comma-separated **string** |
| `start_tunnel` takes `rate_limit` | not implemented; `target_host` exists instead and was never documented |
| `stop_tunnel` takes no arguments | takes an optional `subdomain`; omitting it stops **all** tunnels |
| `list_requests` takes `filter_path`, caps `limit` at 50 | no `filter_path`, no cap |

None of these are regressions — the tools were built this way from the start. They are recorded
rather than quietly corrected so that the next reader can tell the difference between a gap and a
bug, and so a request for `rate_limit` or `filter_path` is recognisable as a feature request.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-01* | *Last Reviewed: 2026-10-01*
