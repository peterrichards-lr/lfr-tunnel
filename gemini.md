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
- [x] Implement CLI integration (`lfr-tunnel login`) for OIDC token exchanges and Hybrid Handoff.
- [x] Create server-side administrative control endpoints for user promotion and token management.
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
- [x] Debug and verify local E2E Docker integration test suite (resolved control domain routing via Nginx network aliases and added Nginx startup checks in run.sh).
- [x] Integrate the E2E Docker integration test suite into the GitHub Actions CI workflow (.github/workflows/ci.yml).
- [x] Simplify client-side setup by updating default ServerURL to https://tunnel.lfr-demo.se.
- [x] Recompile and deploy latest lfr-tunneld to the VPS.
- [x] Implement client-side versioning (-version) and self-upgrade (-upgrade) capability targeting GitHub Releases.
- [x] Update README.md with simplified installation instructions (one-liner curl and PowerShell commands).
- [x] Implement binary integrity checks during self-upgrade (SHA256 checksum validation).
- [x] Update release.yml workflow to automatically generate and upload checksums.txt.
- [x] Fix E2E script on GitHub Actions by supporting docker compose subcommand fallback.
- [x] Fix unchecked Decode errcheck warnings in pkg/server/server_test.go.
- [x] Resolve fragile sleep timeout in E2E integration test runner.
- [x] Investigate and resolve E2E Docker Integration Test failure in CI (use --no-deps for docker-compose run to prevent recreate/wipe of server database).
- [x] Release, tag, and deploy v1.1.0 of lfr-tunnel to GitHub and VPS.
- [x] Restructure documentation: make README.md and architecture.md generic (self-hostable), create docs/liferay-se-guide.md for Liferay SE team specifics.
- [x] Implement server-side administrative control endpoints and audit log (v1.2.0).
- [x] Implement DDOS auto-ban rate limiter in the data plane and application layer.
- [x] Add IP Blacklist database table and CRUD API endpoints.
- [x] Inject missing audit log events (user.registered, token.claimed, lease.kicked, ip.blacklisted).
- [x] Implement `-rate-limit` CLI flag for Subdomain traffic protection.
- [x] Build the Admin Web Dashboard
- [x] Implement configurable admin email alerts from the UI.
- [x] Implement manual email verification flow for users to prevent spam.
- [x] Implement Theming and Aesthetics engine in Admin Dashboard (Light/Dark mode).
- [x] Implement Resource/Bandwidth Monitoring per tunnel (Bytes In/Out tracking).
- [x] Implement Cloud User Portal (Behind `enable_user_portal` feature flag with Magic Link auth).
- [x] Implement automated Abuse Reporting endpoint (`/api/portal/report`) and sliding portal session expiration window.
- [x] Implement global Version API Endpoint (`/api/version`) for external script integrations.
- [x] Implement asynchronous start-up compatibility check and `-check-version` CLI flag to ensure backward compatibility and LDM integration.
- [x] Implement Admin Magic-Link login flow (removing static tokens).
- [x] Implement Admin Owner role restriction and Admin email domain registration whitelist.
- [x] Implement support for domains list and simplified Owner block configuration in `server-config.yaml`.
- [x] Investigate and fix GitHub Actions CI Failures by updating E2E tests for the new configuration schema.
- [x] Unify the Identity Provider and Dashboard into a single responsive Light/Dark mode interface.
- [x] Reconfigure VPS Postfix to natively present Let's Encrypt certificates to pass secure `STARTTLS`.
- [x] Reconfigure VPS Postfix to use dual-stack routing (`inet_protocols = all`) and whitelist external IP `mynetworks` for secure SMTP relaying.
- [x] Fix Magic Link delivery failures by implementing strict dual-stack SPF validation.
- [x] Implement strict single-session concurrency and proactive background polling for kick alerts.
- [x] Implement Global Admin Broadcast messaging system with real-time UI banners.
- [x] Implement vanilla JS data table pagination, sorting, and filtering across all dashboard views.
- [x] Create automated dev restart shell scripts to accelerate developer iteration loop.

## Roadmap (Post Public Visibility PR Flow)
- [x] Implement targeted user messaging (selectively target individual active users from the dashboard with custom alerts).
- [x] Implement Playwright UI E2E test automation for full, deterministic Dashboard test coverage.
- [x] Track and display user registration origin (invite, registration, SSO) in users table.
- [x] Fix pagination button width and add First/Last/page-select controls.
- [x] Fix Chart.js infinite page growth on analytics page (constrain canvas in relative div).
- [x] Set AuthMethod at user creation in all registration flows (registration, invite, SSO).
- [x] Add SSO/Keycloak E2E integration test suite with isolated Docker environment (`make e2e-sso`).
- [x] Investigate VPS outbound IPv4 port 25 block for Fastmail/non-Google recipients.
- [x] Fix E2E Docker Integration Test failure in CI (serve setup.html from embedded FS and update token extraction regex in tests/e2e/run.sh).
- [x] Implement Zero-Dependency Asynchronous E2E State Coordinator using `.progress-signal` lifecycle.
- [x] Fix Keycloak health check in `tests/e2e/docker-compose-sso.yml` using zero-dependency `/dev/tcp` check.
- [x] Fix Keycloak SSO E2E token verification failure by implementing `skip_issuer_check` in SSO/OIDC provider config.
- [x] Fix missing `status` field in `/api/me` response to satisfy SSO E2E verification.
- [x] Create comprehensive SSO/OIDC cloud integration guide (Azure, Google Cloud, AWS) with local mock configuration instructions.
- [x] Bump version to v1.7.0 in pkg/config/version.go
- [x] Commit, tag v1.7.0, and push to trigger automated GitHub Release
- [x] Bump version to v1.7.5, integrate Homebrew/Scoop, and create Getting Started Guide (standardising Windows binary naming across platforms)
- [x] Deploy automated dual-stack (IPv4 & IPv6) Cloudflare Dynamic DNS updater service and timer on the VPS.
- [x] Implement passive and active service self-healing (Nginx auto-restart systemd overrides and gateway watchdog monitor) on the VPS.

## SentinelOne False Positive Mitigation
- [x] Standardise canonical install path to ~/bin/lfr-tunnel across install.sh, install.ps1, README.md and docs.
- [x] Add -trimpath to all go build commands (Makefile + release.yml) to strip embedded local filesystem paths from binaries.
- [x] Add GitHub Artifact Attestations (actions/attest-build-provenance@v2) to release.yml for free supply-chain provenance tied to GitHub OIDC.
- [x] Create SECURITY.md documenting binary signing status, EDR compatibility, package manager trust signals, and canonical exclusion paths for admins.
- [x] Add Homebrew Tap support (peterrichards-lr/homebrew-tap) — Formula/lfr-tunnel.rb auto-updated on each release by scripts/update-tap-bucket.sh.
- [x] Add Scoop Bucket support (peterrichards-lr/scoop-bucket) — bucket/lfr-tunnel.json auto-updated on each release by scripts/update-tap-bucket.sh.
- [x] Update release.yml with Update Homebrew Tap & Scoop Bucket step (requires TAP_BUCKET_PAT secret).
- [x] Update README.md and docs/liferay-se-guide.md to lead with Homebrew/Scoop as recommended install methods.
- [x] Add TAP_BUCKET_PAT secret to lfr-tunnel repo (GitHub PAT, contents:write on homebrew-tap and scoop-bucket).
- [ ] Apply to SignPath Foundation for free Windows Authenticode signing (https://signpath.org).
- [ ] Submit official release binary hashes to SentinelOne false-positive review portal.
- [ ] Ask SentinelOne admin to configure path exclusions per SECURITY.md (Homebrew + direct-install paths).

## Bug Fixes
- [x] Fix unit test TempDir cleanup race by sleeping 50ms before stopping the server

## Dependabot Alert Mitigation
- [x] Upgrade github.com/jpillora/chisel to v1.11.6 and other dependencies to patch CVE-2026-48113 and resolve Dependabot alerts.
## Maintenance Mode and Safe Restoration
- [x] Create a self-contained, Liferay-themed static maintenance page `pkg/server/static/maintenance.html`.
- [x] Create `scripts/enable-maintenance.sh` to trigger maintenance mode in Nginx.
- [x] Create `scripts/disable-maintenance.sh` to disable maintenance mode.
- [x] Create a wrapper script `scripts/restore-with-maintenance.sh` that safely coordinates maintenance state and restore-backup.sh.
- [x] Document Nginx maintenance configuration in `docs/setup_guide.md`.
- [x] Implement dynamic version number display in the login page footer and sidebar bottom in the dashboard.

## Documentation and Branching Strategy
- [x] Review and align markdown files with the current repository state and document the branching/PR conventions in `CONTRIBUTING.md`.

## Bug Fixes
- [x] Fix MFA Setup button and modal display logic in the dashboard UI.
- [x] Fix Export CSV button layout width.
- [x] Implement System Analytics PDF export feature.
- [x] Test configuration parsing of DockerImage environment variable and configuration key in config_test.go.
- [x] Add direct unit test coverage for TOTP/MFA algorithms in totp_test.go.
- [x] Format System Analytics PDF print layout to support clean multi-page flows and page breaks.
- [x] Fix missing `totp_enabled` field in `/api/me` response causing MFA to always display as disabled.


