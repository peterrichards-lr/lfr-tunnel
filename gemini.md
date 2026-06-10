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
- [ ] Implement user token database storage with expiration/revocation capabilities on the server.
- [ ] Implement Mock SSO/Developer Backdoor login bypass for local testing.
- [ ] Implement CLI integration (`lfr-tunnel login`) for OIDC token exchanges.
- [ ] Create server-side administrative control endpoints for user promotion and token management.
- [x] Release, tag, and deploy v1.0.1 of lfr-tunneld to the VPS.
- [x] Fix CI build and formatting failures.
- [x] Upgrade Go version in GitHub Action workflows to 1.22.








