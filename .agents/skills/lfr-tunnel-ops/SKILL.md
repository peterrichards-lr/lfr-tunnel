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
  *Note: On local dev machines, tests must run inside `/private/tmp` due to local EDR restrictions. The Makefile handles this automatically by setting `TMPDIR=/private/tmp`.*
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
  ./scripts/build-client-binaries.sh
  ```
  *(Outputs to `dist/`: Darwin arm64/amd64, Linux arm64/amd64, and Windows amd64)*

---

## 3. Creating a Release & Bumping Version

To automate the release lifecycle (bumping the version in `whats-new.json`, creating a branch and tag, pushing them, and raising an auto-merging Pull Request), use the automated release script.

- **Run Release Automation**:
  ```bash
  ./scripts/create-release-tag.sh <NEW_VERSION_TAG>
  ```
  - `NEW_VERSION_TAG` must follow semantic versioning (e.g., `v1.17.0`).
  - *Note: You must be on the `master` branch with a clean working tree (no uncommitted changes other than `gemini.md`) before running this script.*
  - **CRITICAL COMPLIANCE NOTE**: Never use `--admin` to bypass branch protection rules to merge the resulting PR, or any other PR. The AI assistant must let CI/CD checks pass naturally and follow the repository rules to the letter.

---

## 4. Signing Client Binaries

Before deploying client binaries or making releases, they must be signed.
- **Run Signing Script**:
  ```bash
  ./scripts/sign-client-binaries.sh
  ```
  - **Environment Variables** (used to bypass interactive prompts):
    - `LFT_MACOS_IDENTITY`: macOS codesigning identity (e.g. from `security find-identity`).
    - `LFT_SIGN_P12` / `LFT_SIGN_KEY` / `LFT_SIGN_CRT`: Credentials for Windows code signing (can refer to 1Password reference `op://...` or local path).
    - `LFT_SIGN_PASS`: Password for Windows signing.
    - `LFT_GPG_KEY`: GPG Key ID for Linux signing (defaults to `LFT_SIGN_PASS` for GPG passphrase).
    - `LFT_SKIP_GPG`: Set to `true` to skip GPG Linux signatures.

---

## 4. Deploying to the VPS

Deployments require SSH access to the VPS. The private key is typically `~/.ssh/id_vm6_networks_vps`.

### Deploying Client Binaries
Copies the multi-platform binaries from `dist/` and `checksums.txt` to the VPS static downloads directory (`/var/www/lfr-tunnel/static/downloads`).
```bash
./scripts/deploy-client-binaries.sh -i ~/.ssh/id_vm6_networks_vps
```

### Deploying Gateway Changes
Cross-compiles the Linux `lfr-tunneld` binary and deploys it along with static assets, error pages, email templates, and translation resources to the VPS, restarting the systemd service.
- **Deploy immediately**:
  ```bash
  ./scripts/deploy.sh -i ~/.ssh/id_vm6_networks_vps
  ```
- **Deploy with user countdown warning** (broadcasts alert message to active tunnels):
  ```bash
  ./scripts/deploy.sh -i ~/.ssh/id_vm6_networks_vps -w 30
  ```

---

## 5. Maintenance and Recovery Operations

Manage Nginx maintenance mode or perform safe database backups/restores on the VPS.

- **Enable Maintenance Mode** (serves static Liferay-themed maintenance page):
  ```bash
  ./scripts/enable-maintenance.sh -i ~/.ssh/id_vm6_networks_vps -a "Upgrade" -r "System Maintenance & Database Upgrade" -d "15m"
  ```
- **Disable Maintenance Mode**:
  ```bash
  ./scripts/disable-maintenance.sh -i ~/.ssh/id_vm6_networks_vps
  ```
- **Safe Restore Backup** (automatically enables maintenance mode, restores DB, and disables maintenance):
  ```bash
  ./scripts/restore-with-maintenance.sh -i ~/.ssh/id_vm6_networks_vps -f /home/peterrichards/backups/lfr-tunnel-backup.sql
  ```

---

## 6. VPS Diagnostics

Run remote diagnostic checks on the VPS (checking system stats, systemd service status, Nginx configuration, database connection, and firewall rules):
```bash
./scripts/diagnose-gateway.sh -i ~/.ssh/id_vm6_networks_vps
```


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-02* | *Last Reviewed: 2026-07-02*
