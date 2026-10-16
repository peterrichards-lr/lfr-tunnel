# Liferay Tunnel documentation

Liferay Tunnel (`lfr-tunnel`) securely exposes a local Liferay environment — a Docker container, an LDM workspace or a Tomcat bundle — on a public HTTPS subdomain, so you can share it for demos, testing and remote access. These pages cover using the client, running your own gateway, and how it all works.

> [!NOTE]
> ℹ️ **Community project** — not an official Liferay product and not covered by Liferay Support. [About this project](about.md)

## I want to…

| I want to… | Go to |
|---|---|
| Share my local Liferay with someone, for the first time | [Getting Started](getting_started.md) |
| Set my client up once and stop passing flags | [Client configuration file](client_configuration.md) |
| Get set up as a Liferay Sales Engineer | [Liferay SE Guide](liferay-se-guide.md) |
| Drive the tunnel from an AI assistant | [MCP Guide](mcp.md) |
| Expose something other than Liferay | [Exposing Any Local HTTP Service](exposing-any-http-service.md) |
| Use my own domain instead of a gateway subdomain | [Using Your Own Domain](custom_domains.md) |
| Run my own gateway | [Control plane setup](server/setup_guide.md) |
| Add a regional edge node | [Edge node scaling](server/edge_setup_guide.md) |
| Put sign-in behind my identity provider | [SSO/OIDC Cloud Guide](server/sso_cloud_guide.md) |
| Check it's safe to run on a corporate machine | [InfoSec and trust guide](infosec.md) |
| Understand how it works | [System Architecture Guide](architecture.md) |

## By subject

### Client guides

- **[Getting Started Guide](getting_started.md)**: Learn the basic concepts, registration flow, and how to start your first tunnel.
- **[Client configuration file](client_configuration.md)**: Complete key reference for `~/.lfr-tunnel/config.yaml` — every setting, its flag and environment equivalents, which of the three wins, and why `server_url:` is not the same as `-pin`.
- **[Liferay SE Guide](liferay-se-guide.md)**: Team-specific instructions for Liferay Sales Engineering (LDM setup, codesigning path exclusions, dynamic configurations).
- **[MCP Guide](mcp.md)**: Learn how to set up the Model Context Protocol (MCP) server to use the tunnel with AI assistants.
- **[Exposing Any Local HTTP Service](exposing-any-http-service.md)**: Addendum — the tunnel does not inspect what is listening on the port it publishes, so a client extension in dev mode, a Node service or any other local HTTP server works with `-ports`. Naming a port switches the Liferay auto-discovery off.
- **[Using Your Own Domain](custom_domains.md)**: Serve a tunnel at a domain you own rather than a subdomain of the gateway. Point it with a `CNAME`, register it in the portal, connect with `-domain`; the gateway obtains and renews the TLS certificate itself.

### Server and operator guides

All operator deployment and configuration resources are organized under the `docs/server/` directory:

- **[Control plane setup](server/setup_guide.md)**: Central VPS installation guide, including systemd service configurations, Nginx reverse proxy setups, Let's Encrypt certificates, postfix SMTP relaying, and security access lists.
- **[Edge node scaling](server/edge_setup_guide.md)**: Setting up regional Edge Nodes and configuring real-time edge lease kicks and state updates via WebSockets.
- **[AWS EC2 Provisioning Guide](server/aws_setup_guide.md)**: AWS-specific supplement covering EC2 instance/AMI choice, security groups, key pairs, and Elastic IPs for either the control plane or edge nodes.
- **[SSO/OIDC Cloud Guide](server/sso_cloud_guide.md)**: Integrating gateways with Single Sign-On (SSO) providers (Keycloak, Azure AD/Entra, AWS, Google Cloud).

### Architecture and security

- **[System Architecture Guide](architecture.md)**: Deep dive into the unified network routing sequence, HTTP/WebSocket multiplexing, database tables, passwordless magic links, and OIDC auth flows.
- **[InfoSec and trust guide](infosec.md)**: Trust verification, binary codesigning (macOS/Windows), corporate network compliance, path exclusions, and gateway administrative security controls.

### Contributing

- **[Contributing Guide](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/CONTRIBUTING.md)**: General contribution rules, git branching conventions, code style checks, and release checklists.
- **[Testing Protocol](https://github.com/peterrichards-lr/lfr-tunnel/blob/master/CONTRIBUTING.md#-testing--verification-protocol)**: Detailed instructions for running automated unit/E2E tests locally and manual exploratory test scenarios.
- **[About these docs](about.md)**: Who maintains the project, that it is not an official Liferay product, and how these pages are written and reviewed.

## Related Liferay tools

Three community tools from the same maintainer. None is an official Liferay product or covered by Liferay Support.

| Tool | What it does |
|---|---|
| [Liferay Docker Manager](https://peterrichards-lr.github.io/liferay-docker-manager/) | Run, manage and develop Liferay DXP and Portal environments in Docker. |
| [Liferay AI Commerce Accelerator](https://peterrichards-lr.github.io/liferay-ai-commerce-accelerator/) | Generate and manage AI-driven commerce data in Liferay DXP. |
| Liferay Tunnel | You are here. Securely expose a local Liferay environment on a public HTTPS subdomain. |


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-15* | *Last Reviewed: 2026-10-15*
