# Liferay Tunnel (lfr-tunnel)

`lfr-tunnel` is an open-source, MIT-licensed tunneling utility tailored for Liferay Development and Sales Engineering (SE) teams. It allows local Liferay runtime environments (including LDM workspaces, standalone Liferay Tomcat bundles, and Liferay Docker containers) to be securely exposed through dynamic wildcard subdomains on public domain endpoints.

Unlike generic tunneling tools, `lfr-tunnel` is built specifically for Liferay:
- **Zero-Config Port Matching**: Scans Liferay Workspace directories to extract and map client extension development ports automatically.
- **Multi-Port Tunneling**: Maps the main Liferay portal instance (port `8080`) and all client extensions under a single subdomain prefix (e.g. `alpha-se.yourdomain.com` and `alpha-se-my-extension.yourdomain.com`).
- **Liferay Header Injection**: Injects custom HTTP proxy headers required for Liferay virtual host mappings and OAuth2 redirect URIs.
- **Themed Offline Page**: Serves a premium, Liferay-themed fallback page when the local client machine goes offline.

---

## 📖 Documentation Directory

To prevent information overload, our documentation is divided into dedicated, topic-specific guides:

### 🚀 Getting Started & CLI Usage
*   **[Getting Started Guide](docs/getting_started.md)** — Step-by-step instructions to install the client CLI (`lfr-tunnel`), register for access, claim your Personal Access Token (PAT), and connect your first tunnel.
*   **[Liferay Sales Engineering (SE) Guide](docs/liferay-se-guide.md)** — Team-specific quickstart instructions, Dockerized wrapper scripts, EDR/SentinelOne bypass instructions, and Tomcat/Docker network setups.
*   **[Model Context Protocol (MCP) Guide](docs/mcp.md)** — Integration specifications for using `lfr-tunnel` tools inside AI agentic coding workspaces.

### 🛠️ Server Administration
*   **[Server Gateway Setup Guide](docs/server/setup_guide.md)** — Comprehensive setup guide to host your own gateway server (`lfr-tunneld`), configure wildcard DNS, Caddy/Nginx reverse proxies, Postfix secure TLS SMTP relays, and scheduled backups.
*   **[Edge Gateways Setup Guide](docs/server/edge_setup_guide.md)** — How to configure regional edge server nodes to minimize latency during live demonstrations.
*   **[SSO & OIDC Integration Guide](docs/server/sso_cloud_guide.md)** — Single Sign-On and multi-tenant OpenID Connect authentication setup (Google Cloud, Azure Entra ID, Keycloak, Liferay Portal).

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


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-02* | *Last Reviewed: 2026-07-02*
