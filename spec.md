# Specification: Liferay Tunnel (lfr-tunnel)

This document describes the technical architecture and design of `lfr-tunnel`, a self-hosted tunneling solution tailored for Liferay local development environments.

## Architecture Overview

```
                      ┌───────────────────────────────────────────────┐
                      │              Public Internet                  │
                      │        (DNS: *.yourdomain.com)                │
                      └───────────────────────┬───────────────────────┘
                                              │
                                              ▼
                      ┌───────────────────────────────────────────────┐
                      │          lfr-tunneld (Server Gateway)         │
                      │  - Port 80/443 (HTTP/HTTPS Router)            │
                      │  - Auto Let's Encrypt / Wildcard SSL          │
                      │  - Injects Liferay-specific Headers           │
                      │  - Fallback page for Offline clients          │
                      │  - In-process Chisel Server (Port 8081)       │
                      └───────────────┬──────────────────────┬────────┘
                                      │                      │
                   (Tunnel Connection)│                      │(Reverse Proxy)
                                      ▼                      ▼
                            ┌───────────┐              ┌───────────┐
                            │ localhost │              │ localhost │
                            │Port 10001 │              │Port 10002 │
                            └─────┬─────┘              └─────┬─────┘
                                  │                          │
                                  └────────────┬─────────────┘
                                               │ (Multiplexed TCP over WebSockets)
                                               ▼
                                      ┌──────────────────┐
                                      │    SE Machine    │
                                      │ Client CLI (lfr) │
                                      └────────┬─────────┘
                                               │
                       ┌───────────────────────┴───────────────────────┐
                       ▼                                               ▼
             ┌──────────────────┐                            ┌──────────────────┐
             │ Liferay Instance │                            │ Assets Server    │
             │ (LocalPort: 8080)│                            │ (LocalPort: 3000)│
             └──────────────────┘                            └──────────────────┘
```

## Detailed Flow

### 1. Handshake and Port Allocation
1. **Client Registration**: The Client CLI (`lfr-tunnel`) scans the workspace (e.g., `client-extension.yaml` or parameters) to determine which local ports need to be tunneled (e.g., `8080` for the Liferay instance, `3000` for client-extension assets).
2. **Subdomain Request**: The client calls the server's registration endpoint `/api/register` with the requested subdomain prefix (e.g., `alpha-se`) and the local port mappings:
   ```json
   {
     "subdomain_prefix": "alpha-se",
     "ports": [8080, 3000],
     "auth_token": "se-shared-secret"
   }
   ```
3. **Allocation**:
   - The server validates the `auth_token`.
   - The server verifies if subdomains `alpha-se.yourdomain.com` and `alpha-se-assets.yourdomain.com` are available.
   - The server allocates a unique localhost TCP port for each (e.g., `10001` for `8080`, `10002` for `3000`).
   - The server registers the routing mappings:
     - `alpha-se.yourdomain.com` -> `127.0.0.1:10001`
     - `alpha-se-assets.yourdomain.com` -> `127.0.0.1:10002`
   - The server creates a temporary Chisel user profile with a generated password (session token) and allowed remotes:
     - `R:10001:localhost:8080`
     - `R:10002:localhost:3000`
4. **Response**: The server returns the allocated remotes and session token to the client.

### 2. Tunnel Establishment
1. The client starts its internal Chisel client, connecting to the Chisel WebSocket endpoint on the server (e.g., `wss://tunnel.yourdomain.com/tunnel`).
2. It authenticates using the session token.
3. The server authorizes the connection and exposes ports `10001` and `10002` locally on the server.
4. The client binds these to its local `8080` and `3000` ports.

### 3. HTTP Request Routing and Header Injection
When a public user visits `https://alpha-se.yourdomain.com/web/guest/home`:
1. The request hits the server gateway on port 443.
2. The server looks up the host `alpha-se.yourdomain.com` in its routing table.
3. If the host is found and active:
   - The server reverse-proxies the HTTP request to `http://127.0.0.1:10001`.
   - The server injects the following headers:
     - `X-Forwarded-Host: alpha-se.yourdomain.com`
     - `X-Forwarded-Proto: https`
     - `X-Forwarded-For: <visitor-ip>`
     - `X-Real-IP: <visitor-ip>`
   - The request travels through the Chisel WebSocket tunnel to the client CLI, which forwards it to local port `8080`.
4. If the host is **not found** or the backend TCP connection to `127.0.0.1:10001` fails (e.g. client disconnects or client's local server is offline):
   - The server serves a beautiful Liferay-themed "Offline" page.

---

## Directory Structure

```
lfr-tunnel/
├── cmd/
│   ├── lfr-tunneld/             # Server daemon entrypoint
│   │   └── main.go
│   └── lfr-tunnel/              # Client CLI entrypoint
│       └── main.go
├── pkg/
│   ├── server/                  # Server routing, API, proxy, and Chisel wrapper
│   │   ├── server.go
│   │   ├── proxy.go
│   │   └── auth.go
│   ├── client/                  # Client CLI and Chisel wrapper
│   │   └── client.go
│   └── config/                  # Shared configuration structures
│       └── config.go
├── static/                      # Static assets (Liferay Offline page)
│   └── offline.html
├── client-extension.yaml
├── go.mod
├── go.sum
├── gemini.md
└── spec.md
```

## Configuration

### Server Configuration (`server-config.yaml` or Env vars)
```yaml
domain1: "yourdomain.com"
domain2: "yoursecondarydomain.com"
bind_addr: ":443"
http_bind_addr: ":80"
chisel_bind_addr: ":8081"
auth_token: "se-shared-secret" # Shared secret for client registration
ssl_cert_file: ""              # Path to wildcard SSL cert (or empty for Let's Encrypt auto)
ssl_key_file: ""               # Path to wildcard SSL key
```

### Client Configuration (`client-config.yaml` or CLI arguments)
```yaml
server_url: "https://tunnel.yourdomain.com"
auth_token: "se-shared-secret"
subdomain: "alpha-se"
ports:
  - "8080"
  - "3000"
```

## Core Design Decisions & Predictable Failures

### 1. Host Header rewrite vs Ingress matching
- We route requests based on the `Host` header of the incoming HTTP request.
- The proxy must preserve the original Host header in `X-Forwarded-Host` but rewrite the `Host` header in the forwarded request to the target (or preserve it, depending on Liferay configuration). By default, Liferay expects the original host header to match its virtual host settings or uses `X-Forwarded-Host`. We will support forwarding the host header transparently.

### 2. Auto SSL Management
- We will support using pre-provisioned wildcard certificates (great for SE teams with custom domains) or auto Let's Encrypt for subdomains.
- For Let's Encrypt wildcard certificates, DNS-01 challenge is required, which requires provider-specific APIs. To avoid requiring DNS credentials on the server, we recommend using a pre-installed wildcard certificate or utilizing HTTP-01 challenge for individual subdomains on the fly as they are registered.
- Dynamic HTTP-01 challenges: When a subdomain is registered, our server can dynamically handle Let's Encrypt HTTP-01 challenge requests on port 80.

### 3. Predictable Failures and Handling
- **Failure 1: Dynamic port exhaustion on the server.**
  - *Mitigation*: We will reuse ports. Once a client disconnects, its port is put back into a pool of available ports. The server will keep a registry and release ports after connection timeouts.
- **Failure 2: Port conflicts on local developer machines.**
  - *Mitigation*: The client CLI will verify if the target ports (e.g. `8080`, `3000`) are active locally before opening the tunnel. If not, it will warn the developer.
