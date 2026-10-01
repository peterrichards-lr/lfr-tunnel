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
binary as a subprocess, and `get_tunnel_status` reads the state files in `~/.lfr-tunnel/` rather
than any in-memory registry. Two consequences, one useful and one bounded:

* A tunnel started from the terminal **is** visible to the agent — `WriteState` is on the common run
  path, not the background-only one.
* The tunnel survives the MCP server *exiting*, but it is **not fully detached**. On Unix
  `osutil.BackgroundCommand` is a plain `exec.Command` with no `setsid`, so the tunnel stays in the
  MCP server's process group and an IDE that tears that group down takes the tunnel with it.

**Two tools talk to the inspector over HTTP, not to the tunnel.** `list_requests` and
`replay_request` resolve an inspector by scanning `~/.lfr-tunnel/` and taking the **first state file
in filename order** — that is the lexicographically-first subdomain, not the oldest or the most
recently started. There is no fallthrough: if that tunnel's inspector is unreachable, both tools
error out even when another tunnel has a healthy inspector.

---

## 2. CLI Execution Command

The MCP server is started by running the `mcp` subcommand:

```bash
lfr-tunnel mcp
```

This starts a persistent process that listens for JSON-RPC messages on standard input (`stdin`) and
writes responses to standard output (`stdout`). Log messages are written to standard error
(`stderr`) to prevent corrupting the standard output stream.

`mcp` is listed in `knownSubcommands` and dispatched on `os.Args[1]` in `cmd/lfr-tunnel/main.go`;
the server itself is `pkg/mcp/server.go`.

---

## 3. Tool Specifications

Five tools are advertised in the `tools/list` response.

### A. `get_tunnel_status`

Returns every active tunnel the client can see. It enumerates the state files in `~/.lfr-tunnel/`,
**deletes** any whose PID is no longer running, and enriches each survivor with a live query to its
inspector. It is not a read-only call: a stale state file is removed as a side effect.

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
        "live_status": { "status": "running" }
      }
    ]
  }
  ```

There is no `connected` field; an empty `active_tunnels` is the "not connected" answer. **Handle both
JSON empties.** When `~/.lfr-tunnel/` exists but holds no live tunnel — the normal case for anyone
who has ever run one, since the directory is created on first use and never removed —
`active_tunnels` is **`null`**. It is `[]` only when the directory does not exist at all.

`live_status` is whatever the inspector returned, and is **never empty**: an unreachable inspector
yields `{"status": "offline", "error": "<dial error>"}` and an unparseable response yields
`{"status": "error"}`. Both are synthesised locally, so do not read a `status` field here as
something the inspector asserted. The HTTP status code is not checked, so a 404 or 500 with a
non-JSON body also surfaces as `{"status": "error"}`.

---

### B. `start_tunnel`

Spawns a new background tunnel by re-invoking the client binary.

* **Arguments** (all optional):
  * `subdomain` (string): The requested subdomain prefix.
  * `ports` (string): **Comma-separated**, e.g. `"8080,3000"` — a string, not an array. Omitted, the
    client works down its normal discovery chain: Liferay Workspace detection first, then
    Docker/listener auto-discovery, then a fallback to port 8080.
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
* **Response** if it has not registered within 2 seconds:
  ```json
  {
    "status": "pending",
    "message": "Tunnel spawned in background; it had not registered within 2s. Call get_tunnel_status for its PID and public URLs."
  }
  ```

A **refusal is an error, not a `pending`.** If the client declines to start — most commonly
*"a background tunnel for subdomain X is already running"*, which is easy to hit because repeated
calls with no `subdomain` derive the same one — the tool returns an error carrying the client's own
message. It does not report a tunnel that was never started.

`pending` therefore means only that the spawn succeeded and registration had not finished inside
2s. Call `get_tunnel_status` to see how it settled. **`pending` carries no `pid`**, deliberately:
the only PID in hand at that point belongs to an intermediate process that has already exited, and
a dead number presented as the tunnel's is worse than none.

`subdomain` in a `success` response is the prefix the **gateway assigned**, which may differ from
the one requested.

> **Fixed in #2336.** From v1.15.0 until then, `success` was unreachable and `pending` was the only
> outcome: the server matched the state file against the PID of the process it spawned, while
> `-background` spawns a *further* process and that one writes the state file. The PIDs never
> matched.
>
> It now waits for the client and reads back the PID the client reports, which is exactly the one
> the state file will carry — so the tunnel is identified rather than guessed at. That matters
> beyond the original bug: identifying by "whichever tunnel appeared while we waited" would let a
> call that started nothing claim a tunnel somebody else had just started, and report success with
> their public URLs. On an older client, treat `start_tunnel` as fire-and-forget.

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

Retrieves the rolling request log from the inspector resolved as described in §1. This helps the
agent debug headers, payloads, and response statuses.

* **Arguments**:
  * `limit` (integer, optional): Maximum number of requests to return. Default `10`. Anything `<= 0`
    becomes `10`.
* **Response**:
  ```json
  {
    "requests": [ ]
  }
  ```

The objects in `requests` come from the inspector's `/api/state` history and this server passes them
through untouched — their shape is the inspector's to define, not this server's. An empty or absent
history yields **`null`**, not `[]`.

`limit` has no upper bound of its own, but **the inspector keeps only the last 100 requests**
(`MaxHistory`), so any `limit` above 100 returns 100. **Requires a running tunnel**: with none, the
call returns an error, not an empty list.

---

### E. `replay_request`

Asks the inspector to replay a previously logged request against the local target host.

* **Arguments**:
  * `request_id` (string, **required**): The ID of the request to replay.
* **Response**: the inspector's `/api/replay` response, decoded and re-encoded. The *content* is
  unchanged, but it must be a JSON **object** — an array or scalar body fails with "failed to decode
  replay response".

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
| `get_tunnel_status` returns `connected`, `server_url`, `subdomain`, `target_host`, `rate_limit`, `os`, `version` | returns `active_tunnels` and nothing else |
| `active_tunnels[]` entries are `{local_port, public_url}` | `{pid, subdomain, public_urls, ports, start_time, inspector_url, live_status}` |
| `start_tunnel` takes `ports` as an array of integers | takes a comma-separated **string** |
| `start_tunnel` takes `rate_limit` | not implemented; `target_host` is accepted instead, and was never documented *as an argument* |
| `stop_tunnel` takes no arguments | takes an optional `subdomain`; omitting it stops **all** tunnels |
| `list_requests` takes `filter_path`, caps `limit` at 50 | no `filter_path`; `limit` is uncapped but the history is 100 |

None of these are regressions — the tools were built this way from the start. They are recorded
rather than quietly corrected so that the next reader can tell the difference between a gap and a
bug, and so a request for `rate_limit` or `filter_path` is recognisable as a feature request.

The one genuine bug found while reconciling this page was **#2336** (`start_tunnel` could never
report success). It is fixed; the behaviour above is the fixed behaviour.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-01* | *Last Reviewed: 2026-10-01*
