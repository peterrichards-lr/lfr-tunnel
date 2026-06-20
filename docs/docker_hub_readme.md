# Liferay Tunnel (lfr-tunnel)

<p align="center">
  <img src="https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/resources/images/logo.png" alt="Liferay Tunnel Logo" width="120" height="120" />
</p>

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Docker Pulls](https://img.shields.io/docker/pulls/peterjrichards/lfr-tunnel.svg)](https://hub.docker.com/r/peterjrichards/lfr-tunnel)

`lfr-tunnel` is a lightweight, secure reverse-tunneling agent built specifically for Liferay developers. It securely exposes local Liferay DXP or Portal instances running inside private networks to external webhooks, remote testing tools, or third-party client extensions without requiring complex firewall modifications or public IP routing.

By establishing an outbound connection to a remote gateway, `lfr-tunnel` opens a secure public endpoint that routes incoming HTTP traffic directly back to your local development container or workspace.

---

## Why lfr-tunnel?

Standard tunneling utilities often choke on Liferay's sophisticated multi-site architectures, deep session management, and virtual host lookups. When a remote webhook hits a standard tunnel, the absolute URLs, redirected login sequences, or session cookie constraints frequently break down.

`lfr-tunnel` tackles this directly by automatically normalising headers and ensuring cookie structures stay intact between the public entrypoint and your local Tomcat instance.

---

## Quick Start

### 1. Run via Docker CLI

To spin up the tunnel agent and connect it immediately to a running Liferay container on your machine:

```bash
docker run -d \
  --name lfr-tunnel \
  --network liferay-network \
  -p 4040:4040 \
  -e LFT_CLIENT_SERVER="https://tunnel.lfr-demo.se" \
  -e LFT_CLIENT_TOKEN="your-secure-access-token" \
  -e LFT_CLIENT_SUBDOMAIN="your-subdomain" \
  -e LFT_TARGET_HOST="liferay-dxp" \
  -e LFT_CLIENT_PORTS="8080" \
  peterjrichards/lfr-tunnel:latest
```

### 2. Run via Docker Compose

Integrate the tunnel directly into your existing local development stack:

```yaml
version: '3.8'

services:
  liferay:
    image: liferay/portal:7.4.3.112-ga112
    container_name: liferay-dxp
    ports:
      - "8080:8080"
    networks:
      - lfr-dev

  tunnel:
    image: peterjrichards/lfr-tunnel:latest
    container_name: lfr-tunnel-agent
    depends_on:
      - liferay
    environment:
      - LFT_CLIENT_SERVER=https://tunnel.lfr-demo.se
      - LFT_CLIENT_TOKEN=your-secure-access-token
      - LFT_CLIENT_SUBDOMAIN=your-subdomain
      - LFT_TARGET_HOST=liferay-dxp
      - LFT_CLIENT_PORTS=8080
    networks:
      - lfr-dev

networks:
  lfr-dev:
    driver: bridge
```

---

## Configuration Reference

Configure the runtime execution using these environment variables:

| Environment Variable | Canonical | Fallbacks | Description | Default / Example |
| :--- | :--- | :--- | :--- | :--- |
| **Server URL** | `LFT_CLIENT_SERVER` | `LFT_SERVER_URL`, `LFT_SERVER` | The public-facing gateway server managing the external entrypoint. | *Required* (e.g., `https://tunnel.lfr-demo.se`) |
| **Auth Token** | `LFT_CLIENT_TOKEN` | `LFT_TOKEN` | The authentication secret used to register the secure connection with the gateway. | *Required* (e.g., `lfr_pat_...`) |
| **Subdomain** | `LFT_CLIENT_SUBDOMAIN` | `LFT_SUBDOMAIN` | Custom subdomain prefix for your public endpoint. | `your-subdomain` |
| **Target Host** | `LFT_TARGET_HOST` | — | The internal address/IP of your target Liferay instance (e.g., `localhost` or `container_name`). | `localhost` |
| **Ports** | `LFT_CLIENT_PORTS` | — | Comma-separated list of ports to route. | `8080` (or `8080,3000`) |
| **Inspector Bind** | `LFT_INSPECTOR_BIND` | — | Binding address for local inspector dashboard. | `0.0.0.0` or `127.0.0.1` |

---

## Source and Support

The code for this agent is open source. You can view the implementation, report bugs, or request features at the official repository:

👉 [github.com/peterrichards-lr/lfr-tunnel](https://github.com/peterrichards-lr/lfr-tunnel)
