# Liferay Tunnel (lfr-tunnel)

<p align="center">
  <img src="https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/resources/images/logo.png" alt="Liferay Tunnel Logo" width="120" height="120" />
</p>

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
  -e LOCAL_SERVICE=liferay-dxp:8080 \
  -e REMOTE_GATEWAY=https://tunnel.yourgateway.com \
  -e TUNNEL_TOKEN=your-secure-access-token \
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
      - LOCAL_SERVICE=liferay-dxp:8080
      - REMOTE_GATEWAY=https://tunnel.yourgateway.com
      - TUNNEL_TOKEN=your-secure-access-token
    networks:
      - lfr-dev

networks:
  lfr-dev:
    driver: bridge
```

---

## Configuration Reference

Configure the runtime execution using these environment variables:

| Environment Variable | Description | Default |
| :--- | :--- | :--- |
| **`LOCAL_SERVICE`** | The internal address and port of your target Liferay instance (e.g., `localhost:8080` or `container_name:8080`). | `localhost:8080` |
| **`REMOTE_GATEWAY`** | The public-facing gateway server managing the external entrypoint. | *Required* |
| **`TUNNEL_TOKEN`** | The authentication secret used to register the secure connection with the gateway. | *Required* |
| **`VIRTUAL_HOST`** | Overrides the Host header sent to Liferay to match custom portal instances. | *Optional* |
| **`LOG_LEVEL`** | Verbosity of the internal engine (`debug`, `info`, `warn`, `error`). | `info` |

---

## Source and Support

The code for this agent is open source. You can view the implementation, report bugs, or request features at the official repository:

👉 [github.com/peterrichards-lr/lfr-tunnel](https://github.com/peterrichards-lr/lfr-tunnel)
