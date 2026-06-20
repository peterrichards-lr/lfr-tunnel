# Liferay Tunnel (lfr-tunnel)

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Docker Pulls](https://img.shields.io/docker/pulls/peterjrichards/lfr-tunnel.svg)](https://hub.docker.com/r/peterjrichards/lfr-tunnel)

**Liferay Tunnel** is a high-performance, secure tunneling solution tailored for the Liferay Sales Engineering (SE) team. It allows developers and SEs to route traffic from public wildcard subdomains to locally running Liferay / Tomcat instances, offload SSL, and inspect traffic in real-time.

This image is the client-side component of `lfr-tunnel` designed to run seamlessly inside Docker environments, including orchestration via **Liferay Development Manager (LDM)**.

---

## Key Features in Docker

- **Zero-Dependency Startup:** Simply pass your personal access token and server endpoint to establish a secure WebSocket-based tunnel.
- **Dynamic Port Mapping:** Expose Liferay Tomcat (`8080`) and local asset compilation servers (e.g., `3000`) simultaneously.
- **Host-to-Container Interceptor:** Transparently intercepts incoming requests, injection-aware header rewriting (`LFT_TARGET_HOST`), and dynamic header additions.
- **Embedded Web Inspector:** Visualizes webhooks and API traffic in real-time. Runs on port `4040` (automatically binds to `0.0.0.0` inside containers to support host port mapping).
- **Graceful Offline Fallback:** If your local machine goes offline, the public endpoint automatically displays a beautifully themed maintenance/offline screen without losing your subdomain lease.

---

## Quick Start (Docker Run)

Run the tunnel client container directly to expose your local Liferay instance running at `http://host.docker.internal:8080` to the gateway:

```bash
docker run -d \
  --name lfr-tunnel \
  -p 4040:4040 \
  -e LFT_CLIENT_SERVER="https://tunnel.lfr-demo.se" \
  -e LFT_CLIENT_TOKEN="your_personal_access_token" \
  -e LFT_CLIENT_SUBDOMAIN="your-subdomain" \
  -e LFT_TARGET_HOST="host.docker.internal" \
  -e LFT_CLIENT_PORTS="8080" \
  peterjrichards/lfr-tunnel:latest
```

Once running, access the local inspector dashboard on your host at `http://localhost:4040`.

---

## Environment Configuration Contract

The container fully respects the standard configuration contract, resolving canonical variables or falling back to standard LDM variables:

| Environment Variable | Canonical | Fallbacks | Description | Example |
| :--- | :--- | :--- | :--- | :--- |
| **Server URL** | `LFT_CLIENT_SERVER` | `LFT_SERVER_URL`, `LFT_SERVER` | Gateway server endpoint | `https://tunnel.lfr-demo.se` |
| **Auth Token** | `LFT_CLIENT_TOKEN` | `LFT_TOKEN` | Gateway developer PAT | `lfr_pat_...` |
| **Subdomain** | `LFT_CLIENT_SUBDOMAIN` | `LFT_SUBDOMAIN` | Custom subdomain prefix | `pjrtest` |
| **Target Host** | `LFT_TARGET_HOST` | — | Backend hostname/IP to route to | `host.docker.internal` |
| **Ports** | `LFT_CLIENT_PORTS` | — | Comma-separated list of ports | `8080,3000` |
| **Inspector Bind** | `LFT_INSPECTOR_BIND` | — | Binding address for local inspector | `0.0.0.0` or `127.0.0.1` |

---

## Docker Compose Example

Deploy the tunnel alongside your local Liferay portal container:

```yaml
version: "3.8"

services:
  liferay:
    image: liferay/portal:7.4.3.8-ga8
    ports:
      - "8080:8080"

  tunnel:
    image: peterjrichards/lfr-tunnel:latest
    ports:
      - "4040:4040"
    environment:
      - LFT_CLIENT_SERVER=https://tunnel.lfr-demo.se
      - LFT_CLIENT_TOKEN=lfr_pat_your_token_here
      - LFT_CLIENT_SUBDOMAIN=my-liferay-instance
      - LFT_TARGET_HOST=liferay
      - LFT_CLIENT_PORTS=8080
    depends_on:
      - liferay
```

---

## Getting Help

- For self-hosting and server gateway configuration, check the [Liferay Tunnel Setup Guide](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/docs/setup_guide.md).
- To view full client features and EDR whitelist paths, see the [Liferay SE User Guide](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/docs/liferay-se-guide.md).
