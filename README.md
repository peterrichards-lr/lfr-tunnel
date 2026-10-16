# Liferay Tunnel (lfr-tunnel)

`lfr-tunnel` is an open-source, MIT-licensed tunneling utility tailored for Liferay Development and Sales Engineering (SE) teams. It allows local Liferay runtime environments (including LDM workspaces, standalone Liferay Tomcat bundles, and Liferay Docker containers) to be securely exposed through dynamic wildcard subdomains on public domain endpoints.

> [!NOTE]
> ℹ️ **Community project** — not an official Liferay product and not covered by Liferay Support. [About this project](https://peterrichards-lr.github.io/lfr-tunnel/about/)

📖 Full documentation: <https://peterrichards-lr.github.io/lfr-tunnel/>

Unlike generic tunneling tools, `lfr-tunnel` is built specifically for Liferay:
- **Zero-Config Port Matching**: Scans Liferay Workspace directories to extract and map client extension development ports automatically.
- **Multi-Port Tunneling**: Maps the main Liferay portal instance (port `8080`) and all client extensions under a single subdomain prefix (e.g. `alpha-se.yourdomain.com` and `alpha-se-my-extension.yourdomain.com`).
- **Liferay Header Injection**: Injects custom HTTP proxy headers required for Liferay virtual host mappings and OAuth2 redirect URIs.
- **Themed Offline Page**: Serves a premium, Liferay-themed fallback page when the local client machine goes offline.

---

## 📖 Documentation Directory

To prevent information overload, our documentation is divided into dedicated, topic-specific guides. They are also published as a searchable site at **[peterrichards-lr.github.io/lfr-tunnel](https://peterrichards-lr.github.io/lfr-tunnel/)**, whose front door is the [documentation hub](docs/README.md).

### 🚀 Getting Started & CLI Usage
*   **[Getting Started Guide](docs/getting_started.md)** — Step-by-step instructions to install the client CLI (`lfr-tunnel`), register for access, claim your Personal Access Token (PAT), and connect your first tunnel.
*   **[Client configuration file](docs/client_configuration.md)** — Complete reference for `~/.lfr-tunnel/config.yaml`: every setting with its type, default and flag/environment equivalent, how flags, environment and file interact, and why `server_url:` avoids the pinning that `-pin` applies.
*   **[Liferay Sales Engineering (SE) Guide](docs/liferay-se-guide.md)** — Team-specific quickstart instructions, Dockerized wrapper scripts, EDR/SentinelOne bypass instructions, and Tomcat/Docker network setups.
*   **[Model Context Protocol (MCP) Guide](docs/mcp.md)** — Integration specifications for using `lfr-tunnel` tools inside AI agentic coding workspaces.
*   **[Exposing Any Local HTTP Service](docs/exposing-any-http-service.md)** — The tunnel does not inspect what is listening on the port it publishes, so a Node service, a client extension in dev mode or any other local HTTP server works with `-ports`.
*   **[Using Your Own Domain](docs/custom_domains.md)** — Serve a tunnel at a domain you own. Point it at the gateway with a `CNAME` and register it in the portal; the gateway obtains and renews the certificate for you.
*   **[About these docs](docs/about.md)** — Who maintains the project, that it is not an official Liferay product, and how these pages are written and reviewed.

### 🛠️ Server Administration
*   **[Server Gateway Setup Guide](docs/server/setup_guide.md)** — Comprehensive setup guide to host your own gateway server (`lfr-tunneld`), configure wildcard DNS, Caddy/Nginx reverse proxies, Postfix secure TLS SMTP relays, and scheduled backups.
*   **[Edge Gateways Setup Guide](docs/server/edge_setup_guide.md)** — How to configure regional edge server nodes to minimize latency during live demonstrations.
*   **[SSO & OIDC Integration Guide](docs/server/sso_cloud_guide.md)** — Single Sign-On and multi-tenant OpenID Connect authentication setup (Google Cloud, Azure Entra ID, Keycloak, Liferay Portal).
*   **[AWS EC2 Provisioning Guide](docs/server/aws_setup_guide.md)** — AWS-specific supplement to the two guides above: instance and AMI choice, security groups, key pairs, and the Elastic IP step other providers do not need.

### 🔒 Security & Architecture
*   **[Architecture & Routing Walkthrough](docs/architecture.md)** — Deep dive into the client-server websocket routing engine, data plane metrics, and E2E headless coordination signals.
*   **[InfoSec Review & EDR Compatibility](docs/infosec.md)** — Security trust validation details (Publisher CN, Team ID, code signing status), path exclusions, and gateway administrative risk-reduction controls.

---

## ⚡ Quick Connect Example

Once your client is installed and your token is saved, navigate to your Liferay Workspace root directory and run:

```bash
# Automatically scan workspace, detect ports, and connect:
lfr-tunnel -subdomain my-dev-subdomain
```

This will automatically expose:
- Your local Liferay Portal instance (port `8080`) -> `https://my-dev-subdomain.yourdomain.com`
- Any active client extension servers (e.g. port `3000`) -> `https://my-dev-subdomain-my-extension.yourdomain.com`

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

---

## About this project

Liferay Tunnel is a community project maintained by Peter Richards. It is not an official Liferay product and is not covered by Liferay Support.

This documentation is written with the help of AI coding agents, with the maintainer accountable for what ships. Each page shows when it was last updated and when it was last reviewed (reviews are carried out by the agents as part of keeping the docs current).

Found something wrong or out of date? [Open an issue](https://github.com/peterrichards-lr/lfr-tunnel/issues/new/choose).


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-15* | *Last Reviewed: 2026-10-15*
