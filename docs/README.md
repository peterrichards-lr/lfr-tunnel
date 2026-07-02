# Liferay Tunnel Documentation

Welcome to the Liferay Tunnel documentation! This is the central hub for learning how to use, deploy, and contribute to `lfr-tunnel`.

## 📖 Developer & Client Guides

- **[Getting Started Guide](getting_started.md)**: Learn the basic concepts, registration flow, and how to start your first tunnel.
- **[Liferay SE Guide](liferay-se-guide.md)**: Team-specific instructions for Liferay Sales Engineering (LDM setup, codesigning path exclusions, dynamic configurations).
- **[MCP Guide](mcp.md)**: Learn how to set up the Model Context Protocol (MCP) server to use the tunnel with AI assistants.

## ⚙️ Server & Operator Deployment Guides

All operator deployment and configuration resources are organized under the `docs/server/` directory:
- **[Control Plane Setup](server/setup_guide.md)**: Central VPS installation guide, including systemd service configurations, Nginx reverse proxy setups, Let's Encrypt certificates, postfix SMTP relaying, and security access lists.
- **[Edge Node Scaling](server/edge_setup_guide.md)**: Setting up regional Edge Nodes and configuring real-time edge lease kicks and state updates via WebSockets.
- **[SSO/OIDC Cloud Guide](server/sso_cloud_guide.md)**: Integrating gateways with Single Sign-On (SSO) providers (Keycloak, Azure AD/Entra, AWS, Google Cloud).

## 🛡️ Architecture & Security Compliance

- **[System Architecture Guide](architecture.md)**: Deep dive into the unified network routing sequence, HTTP/WebSocket multiplexing, database tables, passwordless magic links, and OIDC auth flows.
- **[InfoSec & Trust Guide](infosec.md)**: Trust verification, binary codesigning (macOS/Windows), corporate network compliance, path exclusions, and gateway administrative security controls.

## 🤝 Contributing & Development

- **[Contributing Guide](../CONTRIBUTING.md)**: General contribution rules, git branching conventions, code style checks, and release checklists.
- **[Testing Protocol](../CONTRIBUTING.md#-testing--verification-protocol)**: Detailed instructions for running automated unit/E2E tests locally and manual exploratory test scenarios.

---
*Last Updated: 2026-07-02*  
*Last Reviewed: 2026-07-02*
