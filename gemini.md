# Project: Liferay Tunnel (lfr-tunnel)

Persistent state and planning document.

## Goal
Build an open-source, MIT-licensed tunneling solution tailored for Liferay's Sales Engineering (SE) team. It will allow team members to route traffic from wildcard subdomains on two public domains to their locally running LDM (Liferay Development Manager) / Liferay instances, offload HTTPS/SSL, and handle offline developer machines gracefully.

## Proposed Architecture
The solution will consist of:
1. **Server Gateway (`lfr-tunnel-server` / `lfr-tunneld`)**:
   - Deployed on a public VPS.
   - Central TCP/HTTP multiplexer.
   - SSL termination (auto Let's Encrypt / Certmagic, or custom certificate loading).
   - Injects Liferay-specific headers (`X-Forwarded-Host`, `X-Forwarded-Proto`, etc.).
   - Handles offline client machines gracefully by serving a beautiful, themed "Offline" fallback page.
2. **Client CLI (`lfr-tunnel`)**:
   - Simple command-line interface run locally by developers.
   - Detects local Liferay/LDM configuration (e.g., listening ports).
   - Establishes a secure connection to the VPS gateway.
   - Handles multi-port tunneling (e.g., local Liferay at `8080` + assets server at `3000`).

## Tech Stack
- **Language**: Go (both Server/Gateway and Client CLI)
- **Engine**: Chisel (TCP over HTTP via WebSockets)

## Persistent State & Next Steps
- [x] Align on technology stack (Go + Chisel).
- [x] Implement Configuration System.
- [x] Create Liferay-Themed Offline Page.
- [x] Implement Server Registry and Auth Manager.
- [x] Create detailed technical design in `spec.md`.
- [x] Implement Server Gateway (`lfr-tunnel-server`).
- [x] Implement Client CLI (`lfr-tunnel`).
- [x] Add unit and integration tests.
- [x] Create detailed architecture and routing documentation in `architecture.md`.
- [x] Create user installation guide in `README.md`.
- [x] Add project `LICENSE` (MIT).
- [x] Configure local pre-commit hooks (formatting, linting, testing).
- [x] Create CI validation workflow in `.github/workflows/ci.yml`.
- [x] Fix CI build failures (errcheck linting errors).
- [x] Move DNS files to a dedicated `docs/dns/` directory.
- [x] Document / set up GitHub branch protection rulesets (configured ready in docs/github/).
- [x] Document Nginx, SSL redirection, and Let's Encrypt configurations in docs/server/.
- [x] Configure systemd service and run lfr-tunneld under a dedicated restricted system user.
- [x] Configure VPS: firewall (ufw), new sudo user `peterrichards`, disable root SSH.
- [x] Upgrade dependencies to patch Dependabot vulnerability alerts.
- [x] Update DNS records (DNS resolved to new IP `82.39.133.178`).
- [x] Test SSH and connect to the VPS with the new IP.
- [x] Document supported domains constraint (lfr-demo.se and lfr-demo.online) in README.md and architecture.md.
- [x] Document support for LDM, native Tomcat bundles, and Docker runtimes in README.md.
- [x] Create a Makefile for developer commands (fmt, vet, test, build).
- [x] Generate wildcard Let's Encrypt SSL certificates for domains.
- [x] Activate Nginx reverse proxy configuration on VPS.
- [x] Build and deploy lfr-tunneld binary to VPS as a systemd service.
- [x] Connect and verify client tunnel through VPS.
- [x] Implement server-side supported domains endpoint/response and client-side public URL printing.
- [x] Implement client-side target host override (LFT_TARGET_HOST) and Dockerfile for containerized local execution.
- [x] Fix WebSocket path routing by appending /tunnel to client and rewriting to / on server.
- [x] Create cross-platform Docker wrapper scripts (Bash, Batch, PowerShell) for client run.
- [x] Add Docker-based gitleaks pre-commit hook to prevent API key and token leaks.
- [x] Add detailed runtime usage examples (LDM, Tomcat bundle, Docker container) to README.md.
- [x] Verify formatting and contents of documentation.
- [x] Design OAuth2/Liferay SSO authentication and per-user token management architecture.
- [x] Implement user token database storage with expiration/revocation capabilities on the server.
- [x] Implement Administrative static developer token provisioning and configuration file storage.
- [ ] Implement CLI integration (`lfr-tunnel login`) for OIDC token exchanges.
- [ ] Create server-side administrative control endpoints for user promotion and token management.
- [x] Release, tag, and deploy v1.0.1 of lfr-tunneld to the VPS.
- [x] Fix CI build and formatting failures.
- [x] Upgrade Go version in GitHub Action workflows to 1.22.
- [x] Update auth architecture document with SMTP configuration and email verification flows.
- [x] Implement self-registration database models and request states (pending, approved, active).
- [x] Implement outbound email sending service in the server (supporting local postfix and external SMTP relays).
- [x] Add admin approval endpoints and email notification flows.
- [x] Fix Mermaid syntax parsing errors in auth_architecture.md.
- [x] Move architecture.md to docs/ directory and update references.
- [x] Fully quote all Mermaid diagram labels, nodes, and messages in auth_architecture.md to fix GitHub rendering.
- [x] Replace real admin email address with dummy email in auth_architecture.md.
- [x] Purge real admin email from git history (files and commit authors).
- [x] Upgrade dependencies to patch new Dependabot vulnerability alerts.
- [x] Add subdomain validation and reserved subdomain filtering in Registry.
- [x] Create a unified, step-by-step VPS and DNS replication setup guide.
- [x] Fix CI build and formatting failures (compile golangci-lint from source with goinstall).
- [x] Clean up .golangci.yml config after compiling golangci-lint from source.
- [x] Implement subdomain availability check API on the server (protected by auth token) with alternative suggestions on conflict.
- [x] Create GitHub rulesets documentation (docs/github/) and defer remote application due to GitHub Free private repository limitation.
- [x] Implement client-side token loading from home directory file (~/.lfr-tunnel/token or LFT_TOKEN_FILE).
- [x] Add clean target to Makefile and make build depend on clean.
- [x] Implement domain-specific registration and subdomain checking to keep domain1 and domain2 separate.
- [x] Print the clickable public URLs at the end of client connection setup.
- [x] Implement client-side background execution (-background), check status (-status), and termination (-stop) flags.
- [x] Allow developer PAT authentication on check-subdomain endpoint.
- [ ] Implement local E2E Docker integration test suite.














