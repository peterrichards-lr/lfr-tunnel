# Model Context Protocol (MCP) Server Integration

This document outlines the proposed design and specification for the Model Context Protocol (MCP) server integration in the `lfr-tunnel` client CLI. Adding MCP support allows AI agents (in Cursor, Windsurf, or Google Antigravity IDE) to interact directly with local tunnels, automate reconfigurations, and inspect request logs.

---

## 1. Architecture Overview

The MCP server runs inside the local `lfr-tunnel` client process. It uses a **stdio transport** layer to exchange JSON-RPC 2.0 messages with the calling AI development environment.

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

---

## 2. CLI Execution Command

The MCP server is started by running the following subcommand:

```bash
lfr-tunnel mcp
```

This starts a persistent process that listens for JSON-RPC messages on standard input (`stdin`) and writes responses to standard output (`stdout`). Log messages are written to standard error (`stderr`) to prevent corrupting the standard output stream.

---

## 3. Tool Specifications

The MCP server exposes the following tools to the Model:

### A. `get_tunnel_status`
Returns the status of the current `lfr-tunnel` instance, active leases, configuration parameters, and the gateway URL.

* **Arguments**: None
* **Response Schema**:
  ```json
  {
    "connected": true,
    "server_url": "https://tunnel.lfr-demo.se",
    "subdomain": "peterrichards-se",
    "target_host": "localhost",
    "active_tunnels": [
      {
        "local_port": 8080,
        "public_url": "https://peterrichards-se.lfr-demo.se"
      },
      {
        "local_port": 3000,
        "public_url": "https://peterrichards-se-3000.lfr-demo.se"
      }
    ],
    "rate_limit": 100,
    "os": "macOS",
    "version": "v1.14.3"
  }
  ```

---

### B. `start_tunnel`
Instructs the client to establish a new tunnel connection to the gateway.

* **Arguments**:
  * `subdomain` (string, optional): The requested subdomain. If omitted, falls back to the default configured subdomain.
  * `ports` (array of integers, optional): The ports to expose (e.g., `[8080, 3000]`). If omitted, scans the local workspace.
  * `rate_limit` (integer, optional): Traffic rate limit (requests per minute).
* **Response**:
  ```json
  {
    "status": "success",
    "message": "Tunnel established successfully",
    "subdomain": "peterrichards-se",
    "public_urls": [
      "https://peterrichards-se.lfr-demo.se",
      "https://peterrichards-se-3000.lfr-demo.se"
    ]
  }
  ```

---

### C. `stop_tunnel`
Terminates any active tunnel connection established by the client.

* **Arguments**: None
* **Response**:
  ```json
  {
    "status": "success",
    "message": "Active tunnel connection terminated"
  }
  ```

---

### D. `list_requests`
Retrieves a rolling log of recent HTTP requests routed through the client interceptor. This helps the agent debug headers, payloads, and response statuses.

* **Arguments**:
  * `limit` (integer, optional): Maximum number of requests to return (default: `10`, max: `50`).
  * `filter_path` (string, optional): Substring filter for the request path.
* **Response Schema**:
  ```json
  {
    "requests": [
      {
        "id": "req_1",
        "timestamp": "2026-06-24T10:30:00Z",
        "method": "POST",
        "path": "/o/oauth2/token",
        "request_headers": {
          "Content-Type": "application/x-www-form-urlencoded",
          "Host": "localhost:8080"
        },
        "response_status": 200,
        "duration_ms": 45
      }
    ]
  }
  ```

---

### E. `replay_request`
Instructs the interceptor to replay a previously logged HTTP request against the local target host.

* **Arguments**:
  * `request_id` (string, required): The ID of the request to replay.
* **Response**:
  ```json
  {
    "status": "success",
    "message": "Request req_1 replayed successfully",
    "response_status": 200,
    "duration_ms": 38
  }
  ```

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

---
*Last Updated: 2026-07-02*  
*Last Reviewed: 2026-07-02*
