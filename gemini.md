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
- [ ] Move DNS files to a dedicated `docs/dns/` directory.
- [ ] Document / set up GitHub branch protection rulesets.
- [x] Update DNS records (DNS resolved to new IP `82.39.133.178`).
- [ ] Test SSH and connect to the VPS with the new IP.

