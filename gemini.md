# Project: Liferay Tunnel (lfr-tunnel)

Persistent state and planning document.

## Goal
Build an open-source, MIT-licensed tunneling solution tailored for Liferay's Sales Engineering (SE) team. It will allow team members to route traffic from wildcard subdomains on two public domains to their locally running LDM (Liferay Development Manager) / Liferay instances, offload HTTPS/SSL, and handle offline developer machines gracefully.

## Security & Secrets Management Constraints
- **Plain Text Secrets & Passphrases**: The AI assistant must never ask the user to provide any private keys, certificates, or passphrases in plain text. Since the conversation context is shared with remote servers, pasting sensitive credentials in plain text presents a security risk.
- **Secure Secret Handling**: Instead of pasting secrets:
  - Generate temporary keys/certificates locally using scripts or commands.
  - Load passphrases from secure environment variables or files that are not committed.
  - Instruct the user to run secure decryption or configuration commands locally on their system rather than sharing passwords in the chat.
  - Ensure all temporary certificate and private key files are completely deleted before making any git commits to prevent accidental exposure in the repository history.
- **Local Binary Execution Constraints**: The local system EDR blocks unsigned `lfr-tunnel`/`lfr-tunneld` binaries and Go test run executables (`*.test`). However, execution is permitted within the `/private/tmp` directory. To run Go tests safely, set `TMPDIR=/private/tmp` so that the Go test binaries are built and executed inside the whitelisted `/private/tmp` path.
- **Git Conflict Prevention & PR Management**: Because remote GitHub cannot evaluate custom merge drivers defined in `.gitattributes`, the AI assistant must always run `git fetch origin` followed by `git merge origin/master` locally to resolve any potential `gemini.md` conflict before pushing commits or creating/updating a PR.




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
- [x] Clean up build version naming to omit commit suffixes during local build/deploy
- [x] Revert DB toggle logic for Docker workaround panel and enforce config-only control
- [x] Implement client-side platform configuration in config and expose via version endpoint
- [x] Integrate dynamic platform configurations on Admin/Client Dashboard UI
- [x] Align E2E test environments, Docker configuration, and Playwright UI tests
- [x] Document client platform overrides in Docs
- [x] Create automated sign-release.sh script in scripts/ supporting environment configuration and secure terminal prompting for macOS, Windows, and Linux signing
- [x] Update default macOS command to download binary and copy directly to /usr/local/bin/lfr-tunnel
- [x] Update scripts/install.sh to use the S1 whitelisted /tmp/lfr-tunnel staging path




## SentinelOne False Positive Mitigation
- [x] Standardise canonical install path to ~/runningpoc/bin/lfr-tunnel across install.sh, install.ps1, README.md and docs.
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
- [x] Add ~/.ldm/bin/lfr-tunnel to SentinelOne exclusions in README.md and SECURITY.md.
- [x] Create docs/infosec.md documenting client-side security architecture, trust verification details (Publisher CN, Team ID, GPG public keys), path exclusions, and gateway administrative risk-reduction controls for corporate InfoSec review.
- [x] Implement no-backdoor SSO-only lockdown capability (disable_email_login config flag).
- [x] Support custom target installation directory override via LFT_INSTALL_DIR environment variable in install.sh and install.ps1.
- [x] Change default canonical install path to ~/runningpoc/bin/lfr-tunnel (C:\Users\<username>\runningpoc\bin\lfr-tunnel.exe) across install.sh, install.ps1, README.md, and docs.


## Bug Fixes
- [x] Fix unit test TempDir cleanup race by sleeping 50ms before stopping the server
- [x] Fix renderTimestamp parsing of dates with timezone offsets in dashboard.js to prevent table wrapping issues
- [x] Add visual status indicators (Active, Expired, Revoked) for PATs in the dashboard UI
- [x] Add relative "Expires In" column to PAT table in the dashboard UI

## Dependabot Alert Mitigation
- [x] Upgrade github.com/jpillora/chisel to v1.11.6 and other dependencies to patch CVE-2026-48113 and resolve Dependabot alerts.
## Maintenance Mode and Safe Restoration
- [x] Create a self-contained, Liferay-themed static maintenance page `pkg/server/static/maintenance.html`.
- [x] Create `scripts/enable-maintenance.sh` to trigger maintenance mode in Nginx.
- [x] Create `scripts/disable-maintenance.sh` to disable maintenance mode.
- [x] Create a wrapper script `scripts/restore-with-maintenance.sh` that safely coordinates maintenance state and restore-backup.sh.
- [x] Document Nginx maintenance configuration in `docs/setup_guide.md`.
- [x] Support dynamic action, reason, and duration details on the maintenance page via scripts.
- [x] Implement dynamic version number display in the login page footer and sidebar bottom in the dashboard.
- [x] Implement dual-mode maintenance (Bouncer Mode vs Fire Curtain) in portal admin dashboard

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
- [x] Implement full i18n translation support for the dashboard UI elements (sidebar, tab headers, form fields) to allow immediate language switching.
- [x] Fix Docker Hub link URL suffix by stripping the image tag for web links.
- [x] Add `docker_bypass_url` support to server config and expose to frontend via `/api/version`.
- [x] Fix interceptor bypass bug by rewriting client remotes to point to local intercept ports.
- [x] Update interceptor to support LFT_TARGET_HOST and rewrite HTTP Host header to match the target host.
- [x] Verify LDM sharing design compatibility and update documentation.
- [x] Implement real-time user online portal activity check-in tracking and a decluttered User list table layout.
- [x] Implement a unified User Details & Active Tunnels Modal with direct tunnel kick capability.
- [x] Move hardcoded dashboard 'What's New' release notes to a dynamically served JSON file for automated updates.
- [x] Resolve Joined Date HTML escaping tooltip bug in User Details modal.
- [x] Implement Cache-Busting for static JavaScript and CSS assets served by the Gateway.
- [x] Add dynamic admin settings toggle switch to show/hide the Docker Hub CLI workaround panel.
- [x] Write E2E Playwright test assertions to cover Docker panel visibility, Joined Date HTML rendering, and dynamic toggle behavior.
- [x] Fix Nginx control plane server block WebSocket Upgrade routing on VPS.
- [x] Detect Docker environment on the client side and append ' (Docker)' to Client OS stats.
- [x] Support overriding LFT_TARGET_HOST in the Docker wrapper scripts (Bash, Batch, PowerShell).
- [x] Support LDM environment contract fallbacks (LFT_SUBDOMAIN, LFT_SERVER_URL, LFT_TOKEN) in Go client configuration.
- [x] Bump version to v1.7.12 in whats-new.json, tag, and push to trigger release CI workflow.
- [x] Support dynamic inspector binding address based on Docker detection and LFT_INSPECTOR_BIND.
- [x] Document LFT_INSPECTOR_BIND in setup guide / SE guide.
- [x] Bump version to v1.7.13 in whats-new.json, tag, and push to trigger release CI workflow.

## Subdomain Reservation System (v1.8.0)
- [x] Declutter Active Tunnels dashboard table by moving detailed fields into a new Tunnel Details Dialog Modal (with Copy Link, On-Demand Refresh, and Admin controls).
- [x] Fix compile issue in pkg/db/db_test.go.
- [x] Implement database schema migration for `extension_requested` in `pkg/db/db.go`.
- [x] Implement database CRUD updates and `UpdateSubdomainReservation` in `pkg/db/db.go`.
- [x] Implement API endpoints for reserving, releasing, requesting extensions, and promoting subdomains in `pkg/server/api.go`.
- [x] Implement Admin endpoints for approving extensions, demoting reservations, and adjusting user limits in `pkg/server/api.go`.
- [x] Add HTML email templates and integrate outbound email triggers for reservation events in `pkg/mail/`.
- [x] Update tunnel registration and check-subdomain checks to enforce reservations in `pkg/server/server.go`.
- [x] Implement subdomain quarantine period and HTTP 410 Gone fallback page.
- [x] Build portal UI dashboard panel and admin control views for reservation management in `pkg/server/static/dashboard.js`.
- [x] Restrict 'Never' API token expiration option to admin and owner roles on both server and client.
- [x] Write unit and E2E integration tests for the reservation flow.
- [x] Fix E2E integration tests failure by updating `tests/e2e/run.sh` to reserve the subdomain before tunnel connection.
- [x] Bump version to v1.8.0 in whats-new.json, tag, and push to trigger release CI workflow.

## Binary Signing & VPS Upload
- [x] Support LFT_SKIP_GPG in scripts/sign-release.sh.
- [x] Sign client binaries and update checksums.
- [x] Copy signed client binaries and checksums to the VPS.

## Independent Subdomain Limits & Auto-Reservation (v1.9.0)
- [x] Add admin_max_reservations, owner_max_reservations, and allow_client_auto_reservation to ServerConfig.
- [x] Implement getUserMaxReservations helper and enforce in api.go endpoints.
- [x] Implement client-side auto-reservation logic in register handler (server.go).
- [x] Update dashboard UI to render infinity limits correctly.
- [x] Update E2E config YAMLs and verify tests.

## Refinements & Telemetry (v1.9.1)
- [x] Implement robust IsDocker() check in client package (pkg/client/inspector.go) and use in client CLI (cmd/lfr-tunnel/main.go) to append " (Docker)" to ClientOS.
- [x] Fix client_ip returning as N/A by including it in /api/me lease response (pkg/server/api.go).
- [x] Fix visitor IP tracking by using incoming request r instead of cloned request req in proxy Director (pkg/server/proxy.go).
- [x] Implement active tunnels limit in database and register handshake checks (pkg/db/db.go, pkg/server/server.go, pkg/server/api.go).
- [x] Standardise portal dates to always render via renderTimestamp() (dashboard.js).
- [x] Enhance Subdomains/Tokens UX: copy host button, Owner column for admin view, client-side CSV Export, and admin Token Extend dialog control.

## Client Codesigning & 1Password Integration
- [x] Fix non-interactive read execution failure by checking TTY availability (`[ -t 0 ]`) in signing script.
- [x] Implement 1Password CLI (`op`) integration in signing script to retrieve passwords and certificate documents from 1Password vault securely.

## Client Inspector API Endpoints (v1.9.2)
- [x] Implement GET `/api/healthz` endpoint on client's internal HTTP server.
- [x] Implement GET `/api/info` endpoint on client's internal HTTP server.
- [x] Respect `LFT_INSPECTOR_BIND` binding constraints for local loopback vs Docker wildcard.
- [x] Write unit tests for inspector endpoints and integration metrics.

## Dashboard Action Menu UI (v1.9.3)
- [x] Implement CSS styling for action menu dropdown.
- [x] Implement Javascript utility for handling toggles and clicking outside.
- [x] Convert Active Tunnels table.
- [x] Convert Personal Access Tokens table.
- [x] Convert Users table.
- [x] Convert Registration Requests table.
- [x] Convert Blacklist table.
- [x] Convert Reservations table.
- [x] Convert Admin Subdomain Extensions table.
- [x] Style and position the action dropdown menu correctly.
- [x] Handle clicking outside to auto-close dropdown menus.

## Client/Server Versioning Separation (v1.9.3)
- [x] Implement latest_client_version key in ServerConfig.
- [x] Integrate latest_client_version fallback inside server version response.
- [x] Adapt Dashboard UI version displays to render server_version instead of client version.
- [x] Fix login page HTML layout nesting crash and restrict login version footer to gateway only.
- [x] Add "Gateway: " prefix label for the server version display on the login/registration page footer.
- [x] Document version management strategy in setup_guide.md.

## Future Roadmap Suggestions
- [x] Implement client-side Terminal UI (TUI) Dashboard for active connection metrics and scrolling request paths.
- [x] Create local Request Inspector & Replay web dashboard (similar to Ngrok's local interface) for debugging client extensions and webhooks.
- [ ] Implement multi-region edge VPS gateways to reduce demo latency globally.
- [x] Integrate tunnel provisioning directly into Liferay Development Manager (LDM) execution loops.
- [x] Implement live WebSocket-driven telemetry updates in the portal Admin Web Dashboard.

- [x] Integrate a lightweight Web Application Firewall (WAF) shield on the gateway to filter basic exploit payloads during public presentations.

## Dynamic Loopback Port Routing Fix (v1.9.4)
- [x] Explicitly bind the reverse tunnel listening port on the server side to `127.0.0.1` by prefixing it in the dynamic remote format returned by `/api/register`.
- [x] Verify that all unit and E2E integration tests continue to pass.

## Response Header Cookie and Location Domain Rewriting & Body Truncation Fix (v1.9.5)
- [x] Implement `Location` header URL rewriting in the client's interceptor transport to replace local target host references with the public gateway domain name (`X-Forwarded-Host`/`X-Forwarded-Proto`).
- [x] Implement `Set-Cookie` header domain stripping/rewriting in the client's interceptor transport to prevent browser rejection of localhost/127.0.0.1 cookie domains.
- [x] Fix request and response body stream truncation bug in the interceptor using `io.MultiReader`.
- [x] Write unit tests to cover redirect URL, cookie domain rewriting logic, and large body payloads.
- [x] Verify E2E integration test suite runs successfully.

## System Analytics & Active Visitor IP Tracking Fixes (v1.9.6)
- [x] Fix the zero-value RecordedAt timestamp metric logging bug causing empty System Analytics reports.
- [x] Implement differential delta bandwidth tracking on leases to prevent over-counting metrics.
- [x] Increase default VisitorTimeout from 30s to 5 minutes to keep active connections visible in Details modal.
- [x] Add reverse proxy logging for auditing visitor IP addresses.
- [x] Fix "Your Client: Never Connected" dashboard issue by returning last_client_version and last_client_os in /api/me response.
- [x] Verify that all unit tests compile and run successfully.


## Checksums Branch Protection and Documentation
- [x] Document the checksums branch persistent requirement in CONTRIBUTING.md.
- [x] Create GitHub ruleset configuration for the checksums branch to block deletion.
- [x] Update resources/github/README.md with instructions to apply the checksums ruleset.


## Rate Limiter Memory Leak Prevention and Cleanup
- [x] Implement RemoveRateLimiter on ProxyHandler to delete rate limiters on lease cleanup.
- [x] Wrap API rate limiters with access timestamps and run a background cleanup task to prune stale IP limiters.
- [x] Add unit test coverage for the rate limiter cleanup routines.

## Onboarding Experience Enhancements (Redirection Install & Port Auto-Detection)
- [x] Implement /install and /install.ps1 endpoints on the server gateway to serve installer scripts.
- [x] Implement smart port auto-detection (8080 / 13000) in the client CLI on zero-config startup.
- [x] Write unit tests to cover auto-detection logic and server-side installer routes.

## Client JSON State and LDM Integration (v1.9.7)
- [x] Implement client-side JSON state file writing on startup.
- [x] Implement status-json CLI query command.
- [x] Add unit tests for JSON state serialization and CLI status querying.

## Local Request Replay in the Inspector Web Dashboard (v1.9.8)
- [x] Implement ReplayRequest logic in pkg/client/inspector.go.
- [x] Add /api/replay HTTP endpoint.
- [x] Add Replay Request button and javascript handler in pkg/client/inspector.html.
- [x] Add unit tests for the replay endpoint.

## Gateway Web Application Firewall (WAF) Shield (v1.10.0)
- [x] Implement EnableWAF config key in ServerConfig.
- [x] Create pkg/server/waf.go containing lightweight threat patterns (XSS, SQLi, Path Traversal, Command Injection) and body scanners.
- [x] Integrate WAF check into ProxyHandler's ServeHTTP before proxying requests.
- [x] Serve a beautiful, themed "Blocked by WAF" error page (similar to the Offline page) with HTTP 403 Forbidden.
- [x] Add unit tests for WAF detection rules and configuration toggles.
- [x] Verify standard and Keycloak SSO E2E integration test suites pass.

## Client Terminal UI (TUI) Dashboard (v1.11.0)
- [x] Add ActiveConnections tracking inside pkg/client/interceptor.go.
- [x] Add -no-tui CLI flag and isatty detection in cmd/lfr-tunnel/main.go.
- [x] Create pkg/client/tui.go with alt-screen rendering, stats, latency averages, and log redirection.
- [x] Integrate TUI execution loop inside cmd/lfr-tunnel/main.go.
- [x] Add unit tests for TUI lifecycle, rendering, and formatter helpers.
- [x] Verify standard and Keycloak SSO E2E integration test suites pass.

## Live Telemetry & EDR Workaround (v1.12.0)
- [x] Implement WebSocket-based live telemetry updates for the Admin Web Dashboard.
- [x] Restore deleted release scripts and commit telemetry changes.
- [x] Verify test execution via GitHub Actions CI to bypass local EDR restrictions.
- [x] Fix errcheck linter warnings in telemetry files.
- [x] Update whats-new.json with v1.12.0 release details.

## Local EDR Test Environment Support
- [x] Export TMPDIR=/private/tmp in Makefile and pre-commit hook scripts to execute Go tests from the whitelisted temporary directory.
- [x] Exclude pkg/server tests from local Makefile and pre-commit hook executions to avoid compiling/running server.test entirely.
- [x] Make docs/infosec.md generic by removing specific developer names, VPS locations, and domain names.
- [x] Add 1Password and Docker complexity generic explanations to workspace docs/infosec.md.

## Release v1.13.0 & Deploy
- [x] Implement local-only loopback broadcast endpoint (`/api/local/broadcast`) on the gateway.
- [x] Add unit test coverage for loopback IP and proxy header validation checks.
- [x] Update `scripts/deploy.sh` to accept optional `-w <seconds>` warning delay parameter.
- [x] Integrate local SSH warning broadcast and countdown sleep sequence into `scripts/deploy.sh`.
- [x] Bump version to v1.13.1 in whats-new.json, tag, and push to trigger release CI workflow.
- [x] Deploy signed binaries and gateway changes to the VPS.

## Dashboard WebSocket Routing & JS Crash Fix (v1.13.2)
- [x] Fix JS crash in `dashboard.js` by renaming `login-panel` reference to `login-screen`.
- [x] Add WebSocket Upgrade proxy headers to Nginx main domain `location /` configuration on the VPS.
- [x] Include the maintenance countdown on the Overview screen under the Welcome message when it is active (pending or true).
- [x] Compile and deploy the updated static dashboard changes to the VPS.

## Integration of Deployment Warning Countdown (v1.13.3)
- [x] Support `countdown_seconds` and `duration_minutes` in local broadcast API (`pkg/server/server.go`).
- [x] Add unit test coverage for local-triggered soft maintenance scheduling (`pkg/server/server_test.go`).
- [x] Update `scripts/deploy.sh` to send warning seconds and duration to the local broadcast API.
- [x] Compile, tag, and deploy v1.13.3 to the VPS.
- [x] Extend Playwright E2E UI tests to cover the Overview screen maintenance countdown widget (`tests/e2e/ui/tests/dashboard.spec.ts`).






## Documentation Refinements
- [x] Temporarily strikeout Method A in liferay-se-guide.md and add security team note.
- [x] Add comparison and usage examples for alternative providers (ngrok, cloudflared) in docs/liferay-se-guide.md.

## Development Process Improvements
- [x] Configure git union merge driver for gemini.md in .gitattributes to avoid PR merge conflicts.

## Gateway-First Client Self-Upgrade (v1.14.0)
- [x] Load client config to retrieve ServerURL before executing upgrade flag in main.go.
- [x] Update pkg/client/upgrade.go to query the gateway's version endpoint for updates if ServerURL is present.
- [x] Download latest binary and checksums directly from the gateway with SHA256 verification.
- [x] Implement graceful fallback to GitHub Releases if ServerURL is missing or if the gateway check fails.
- [x] Write unit tests to cover both gateway-based and GitHub-fallback upgrade paths.


## Gateway Diagnostics & Troubleshooting
- [x] Create scripts/diagnose-gateway.sh script for gateway diagnostics.
- [x] Integrate VM6 Networks provider status check into scripts/diagnose-gateway.sh.
- [x] Add Better Stack status badge to docs/liferay-se-guide.md.
- [x] Implement gateway startup time tracking and display server uptime in the Admin Dashboard.
- [x] Implement historical uptime record in database (gateway_runs table, startup/shutdown tracking) and expose via admin API.
- [x] Fix golangci-lint errcheck errors in pkg/db/db.go and pkg/db/db_test.go.
- [x] Add a link to status.lfr-demo.se Better Stack status page from the portal pages, fallback pages, and client connection errors.
- [x] Create a release helper tag script scripts/create-release-tag.sh.

## Release v1.14.1 Automation
- [x] Create release automation script `scripts/create-release-tag.sh` to bump version in `whats-new.json`, create a branch/tag, push them, raise a PR, and enable auto-merge.
- [x] Run the release automation script to trigger the v1.14.1 release.
- [x] Deploy signed binaries and gateway changes of v1.14.1 to the VPS.

## Post v1.14.1 Enhancements
- [x] Add 5-second logout delay in `dashboard.js` when entering maintenance mode so users can read the red banner.
- [x] Update dashboard buttons (`Download Binary` and `Other OSs`) to display OS-specific installer commands served from the VPS instead of binary files.
- [x] Support config option to completely disable the `Download Binary` button on the dashboard.
- [x] Support triggering a mock release warning/shutdown sequence for portal verification.

## GitHub Actions CI Linter Fix
- [x] Fix CI linter job failure in `.github/workflows/ci.yml` by removing `install-mode: goinstall` to use precompiled binary installation.

## Subdomain Reservation Error Enhancement (v1.14.2)
- [x] Add `portal_url` to server config struct and environment parsing (LFT_PORTAL_URL)
- [x] Add `portal_url` field to `RegisterResponse` struct in client and server
- [x] Populate `PortalURL` in register endpoint response on the server
- [x] Update client to parse custom RegistrationError containing `PortalURL`
- [x] Format and print a beautiful, clear instructions message in the CLI pointing to the portal URL on 403


