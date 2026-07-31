---
name: lfr-tunnel-ops
description: Operations, deployment, build, and diagnostic helper skill for the Liferay Tunnel project. Activate this skill when asked to build client binaries, sign binaries, deploy to the VPS, enable/disable maintenance mode, run tests, or check/diagnose the gateway.
---

# Liferay Tunnel Operations & Deployment Guide

This skill guides you through the common operational tasks for the Liferay Tunnel (`lfr-tunnel`) project, including compilation, binary signing, deployment to the VPS, maintenance mode controls, and diagnostics.

## 1. Running Tests and Checks

Always run unit and E2E integration tests before proposing deployment.
- **Unit Tests (EDR Safe)**:
  ```bash
  make test
  ```
  *Note: On local dev machines, tests must run inside `/private/tmp` due to local EDR restrictions. The Makefile handles this automatically by exporting `GOTMPDIR` (default `/private/tmp` via `LFT_TEST_DIR`). See the `edr-constraints` skill for why — never run bare `go test`.*
- **Standard Docker E2E Tests**:
  ```bash
  make e2e
  ```
- **SSO/Keycloak E2E Integration Tests**:
  ```bash
  make e2e-sso
  ```

---

## 2. Compilation and Binary Building

- **Build Local Client & Server Binaries**:
  ```bash
  make build
  ```
  *(Outputs `bin/lfr-tunnel` and `bin/lfr-tunneld`)*
- **Build Multi-Platform Client Binaries**:
  ```bash
  go build -o bin/lfr-tunnel-ops ./cmd/lfr-tunnel-ops
  ./bin/lfr-tunnel-ops build
  ```
  *(Outputs to `dist/`: Darwin arm64/amd64, Linux arm64/amd64, and Windows amd64)*
  *Note: build the `lfr-tunnel-ops` binary first and run the compiled binary — never `go run ./cmd/lfr-tunnel-ops ...`. That pattern is the same unsigned-binary-executed-from-temp-dir shape the local EDR flags; see the `edr-constraints` skill.*

---

## 3. Creating a Release & Bumping Version

To automate the release lifecycle (bumping the version in `pkg/config/version.go` and `whats-new.json`, creating a release branch, pushing it, and raising an auto-merging Pull Request), use the automated release script.

- **Run Release Automation**:
  ```bash
  ./scripts/create-release-tag.sh <NEW_VERSION_TAG>
  ```
  - `NEW_VERSION_TAG` must follow semantic versioning (e.g., `v1.17.0`).
  - *Note: the script requires you to be on the `master` branch (it pulls latest before branching); it does not itself check for a clean working tree — commit or stash unrelated changes first regardless.*
  - *Note: the script creates and pushes a `release-<version>` branch and PR only. It does not create or push a git tag — tagging is a manual step the script prints as its final instruction.*
  - **CRITICAL COMPLIANCE NOTE**: Never use `--admin` to bypass branch protection rules to merge the resulting PR, or any other PR. The AI assistant must let CI/CD checks pass naturally and follow the repository rules to the letter.

---

## 4. Signing Client Binaries

Before deploying client binaries or making releases, they must be signed.
- **Run Signing Script**:
  ```bash
  go build -o bin/lfr-tunnel-ops ./cmd/lfr-tunnel-ops
  op run -- ./bin/lfr-tunnel-ops sign
  ```
  *(**CRITICAL**: You MUST use `op run --` so that 1Password prompts the user to extract the keys needed for Windows and Linux signing. And build first — never `go run ./cmd/lfr-tunnel-ops ...`.)*
  - **Environment Variables** (used to bypass interactive prompts):
    - `LFT_MACOS_IDENTITY`: macOS codesigning identity (e.g. from `security find-identity`).
    - `LFT_SIGN_P12` / `LFT_SIGN_KEY` / `LFT_SIGN_CRT`: Credentials for Windows code signing (can refer to 1Password reference `op://...` or local path).
    - `LFT_SIGN_PASS`: Password for Windows signing.
    - `LFT_GPG_KEY`: GPG Key ID for Linux signing.
    - `LFT_GPG_PASS`: GPG passphrase (falls back to `LFT_SIGN_PASS` if unset; set to `none`/`skip` for no passphrase).
    - `LFT_GPG_SECRET`: GPG private key material or path, for importing the signing key.
    - `LFT_SKIP_GPG`: Set to `true` to skip GPG Linux signatures.

---

## 5. Deploying to the VPS

Deployments require SSH access to the VPS. The private key is typically `~/.ssh/id_vm6_networks_vps`. Build `bin/lfr-tunnel-ops` first (see §2) — never `go run ./cmd/lfr-tunnel-ops ...`.

### Deploying Client Binaries
Copies the multi-platform binaries from `dist/` and `checksums.txt` to the VPS static downloads directory (`/var/www/lfr-tunnel/static/downloads`).
```bash
./bin/lfr-tunnel-ops deploy-clients -i ~/.ssh/id_vm6_networks_vps
```

### Deploying Gateway Changes
Cross-compiles the Linux `lfr-tunneld` binary and deploys it along with static assets to the VPS, restarting the systemd service.
```bash
./bin/lfr-tunnel-ops deploy -i ~/.ssh/id_vm6_networks_vps
```

---

## 6. Maintenance and Recovery Operations

Manage Nginx maintenance mode or perform safe database backups/restores on the VPS.

- **Enable Maintenance Mode** (serves static Liferay-themed maintenance page):
  ```bash
  ./bin/lfr-tunnel-ops maintenance enable -i ~/.ssh/id_vm6_networks_vps
  ```
- **Disable Maintenance Mode**:
  ```bash
  ./bin/lfr-tunnel-ops maintenance disable -i ~/.ssh/id_vm6_networks_vps
  ```
- **Safe Restore Backup** (run directly on the VPS as root — this is not part of `lfr-tunnel-ops` and takes no SSH identity flag; it automatically enables maintenance mode, restores the DB, and disables maintenance):
  ```bash
  sudo ./scripts/common/restore-with-maintenance.sh [backup_file]
  ```

---

## 7. VPS Diagnostics

Run remote diagnostic checks on the VPS (system uptime/load, systemd service status, Nginx config test, UFW firewall rules, Let's Encrypt certificate status, and recent `lfr-tunneld` error logs):
```bash
./bin/lfr-tunnel-ops diagnose -i ~/.ssh/id_vm6_networks_vps
```


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-31* | *Last Reviewed: 2026-07-31*
