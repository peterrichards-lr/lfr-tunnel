---
name: lfr-tunnel-ops
description: Operations, deployment, build, and diagnostic helper skill for the Liferay Tunnel project. Activate this skill when asked to build client binaries, sign binaries, deploy to the VPS, enable/disable maintenance mode, run tests, or check/diagnose the gateway.
---

# Liferay Tunnel Operations & Deployment Guide

This skill guides you through the common operational tasks for the Liferay Tunnel (`lfr-tunnel`) project, including compilation, binary signing, deployment to the VPS, maintenance mode controls, and diagnostics.

## 0. One-Time Setup: Deployment Target

Every `lfr-tunnel-ops` command below that talks to the central VPS (`deploy`, `deploy-clients`, `maintenance`, `diagnose`, `reconcile-nginx`) resolves *which* VPS, as which user, with which SSH key -- none of them hardcode a specific server. Resolution order per field, highest precedence first: a command's own `-i`/`-u`/`-s` flags, then `VPS_USER`/`VPS_IP`/`LFT_IDENTITY_FILE` environment variables, then `lfr-tunnel-ops.yaml` (gitignored, at the repo root).

Copy `lfr-tunnel-ops.yaml.example` to `lfr-tunnel-ops.yaml` and fill in your actual central VPS's user/host/SSH key once, and every command below just works without repeating `-i` on each invocation. If nothing is configured through any of the three sources, the command exits with a clear error naming exactly which field is missing, rather than silently deploying to the wrong place.

### Finding the targets — look these up, don't ask

`lfr-tunnel-ops.yaml` typically defines only `central`. **Its lack of edge entries does not mean the edge details are unknown** — they are all on the machine already. Read them rather than asking the operator:

| What | Where to read it |
| --- | --- |
| Which edge nodes exist, and their Elastic IPs | `scripts/liferay/dns/lfr-demo-production.yaml` — the authoritative record, kept in sync with live DNS after #941. **Read it; do not copy the list into other docs, which is how it drifted in the first place.** |
| Public hostnames | `<region>.lfr-demo.se`, one per edge named in that file; central is `tunnel.lfr-demo.se` |
| SSH keys | `ls ~/.ssh/lfr-tunnel-*.pem` — one per box, named by AWS region rather than by edge name, so match on region |
| SSH user | `ubuntu` on central and every edge |
| Which edges the gateway believes in | `edge_nodes` in `/etc/lfr-tunneld/server-config.yaml` on central |
| Live admin settings | `/etc/lfr-tunneld/lfr-tunnel.db` on central, table `admin_settings` — note `domain_allocation_rule` and `default_domain` live here and override the YAML |

So an edge deploy needs no new information — take the region list from the DNS spec, match it to a key, and:

```bash
./bin/lfr-tunnel-ops deploy -u ubuntu -s <region>.lfr-demo.se -i ~/.ssh/<matching-key>.pem
```

Add each edge to `lfr-tunnel-ops.yaml` as a named target to avoid repeating the flags, and set `aws_region` on any edge with a power schedule so `deploy` starts it, deploys, and stops it back.

Remember that deploying an edge restarts it, which drops its control channel to central and will trigger a client failover. Deploy before a failover test, not during one.

**Managing more than one environment** (e.g. staging/production) from the same checkout: use `lfr-tunnel-ops.yaml`'s multi-target shape (a `targets:` map of named entries, see the commented-out block in `lfr-tunnel-ops.yaml.example`) instead of the flat `central:`/`nginx:` shape. Select which one a command uses with `-target <name>` or the `LFT_OPS_TARGET` env var (same flag-wins-over-env precedence as everything else). If the file defines exactly one target, it's used automatically; if it defines more than one and neither is set, every command errors out listing the available names.

---

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

### Pushing the tag is one-shot — verify the run started

`git push origin <tag>` is the only thing that triggers `.github/workflows/release.yml`. Its
trigger is `on: push: tags: ['v*']` and it has **no `workflow_dispatch`**, so there is no manual
way to start it.

**A tag cannot be re-pushed to try again.** The `Protect Version Tags` ruleset forbids `deletion`
and `update` on `refs/tags/v*`, for everyone — the repository owner included. So if the push does
not produce a run, that version has no GitHub release and never will. The next version is the
only remedy.

This is not hypothetical: on 2026-08-26 the `v1.48.6` tag pushed cleanly and GitHub never created
a run, during an Actions backlog that was also producing `startup_failure` on unrelated
workflows. The tag is correct and points at the right commit; the release simply does not exist.

**So immediately after pushing a tag, confirm the run exists** rather than assuming it:

```bash
gh run list --workflow=release.yml --limit 1
gh release view <tag>
```

If no run appeared within a minute or so, say so at once. There is nothing to retry, and the
sooner it is known the sooner the next release can carry the fix.

What a missing run does and does not affect, so the blast radius is not overstated:

- **Missing**: the GitHub Release and its assets, and the push to the orphan `checksums` branch
  that the portal reads over `raw.githubusercontent.com` (see `docs/architecture.md`). Both stay
  at the previous version, *consistently* — nothing is mismatched, that tier simply skips a
  version.
- **Unaffected**: everything in §4 and §5. The gateway deployment, the locally signed client
  binaries and the downloads page are a separate path with separate bytes — `scripts/minisign_helper.go`
  spells out that CI's binaries and the locally OS-codesigned ones are deliberately not the same.
  A missing GitHub release does not hold up a deployment.

If making the run recoverable is wanted, that is a change to `release.yml`: a `workflow_dispatch`
with a tag input, dispatched from `master`. Note it cannot be dispatched *at* an existing tag —
`workflow_dispatch` is only offered where the workflow file at that ref declares it, so any tag
predating the change stays unrecoverable.

---

## 4. Signing Client Binaries

Before deploying client binaries or making releases, they must be signed.
- **Run Signing Script**:
  ```bash
  go build -o bin/lfr-tunnel-ops ./cmd/lfr-tunnel-ops
  op run -- ./bin/lfr-tunnel-ops sign
  ```
  *(**CRITICAL**: You MUST use `op run --` so that 1Password prompts the user to extract the keys needed for Windows and Linux signing. And build first — never `go run ./cmd/lfr-tunnel-ops ...`.)*

  **An agent should run this itself — do not hand it back to the operator.** The 1Password
  prompt is a biometric dialog raised by the desktop app, not a terminal read on stdin, so it
  reaches the operator perfectly well from a tool call and they approve it there. Asking them
  to run the command by hand instead just adds a round trip. Verified 2026-08-26 signing
  v1.48.6: macOS codesign, Windows osslsigncode, both Linux GPG signatures and the minisign
  step all completed from an agent-invoked `op run --`.

  Two preconditions worth checking before you run it, rather than after:
  - `op whoami` failing with *"account is not signed in"* does **not** mean you are blocked.
    `op run` establishes the session through the same biometric prompt. Do not ask for
    `op signin` first.
  - The `LFT_*` / `MINISIGN_*` variables below must already be exported in the environment,
    holding `op://` references — `op run` substitutes references it finds, it does not invent
    them. `env | grep -E '^(LFT_|MINISIGN)'` confirms it. If they are absent, `sign` does not
    fail: it **skips** each platform in turn and still writes `checksums.txt`, which would
    publish unsigned binaries that look signed. That is the one outcome to avoid here.
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

Deployments require SSH access to the VPS -- see §0 for how the target (user/host/key) is resolved. Build `bin/lfr-tunnel-ops` first (see §2) — never `go run ./cmd/lfr-tunnel-ops ...`.

### Deploying Client Binaries
Copies the multi-platform binaries from `dist/` and `checksums.txt` to the VPS static downloads directory (`/var/www/lfr-tunnel/static/downloads`).
```bash
./bin/lfr-tunnel-ops deploy-clients
```

### Deploying Gateway Changes
Cross-compiles the Linux `lfr-tunneld` binary and deploys it along with static assets to the VPS, restarting the systemd service.
```bash
./bin/lfr-tunnel-ops deploy
```
*Note: `deploy` never touches nginx config -- see "Reconciling Nginx Config" below for that.*

*Note (#1050): pass `-aws-region <region>` (or set `AWS_REGION`/`central.aws_region`) if the
target might be a stopped EC2 instance -- e.g. edge-us/edge-apac's deliberately-wrong
midnight-8am shutdown schedule kept as a live test case for #885. When set, `deploy` starts
the instance first if it's stopped, waits for SSH, deploys, then stops it back afterward
(whether the deploy succeeds or fails). Requires the `aws` CLI to be authenticated already --
`deploy` doesn't handle AWS credentials itself. Leave unset for central (never scheduled off)
or any target you know is already running.*

### Reconciling Nginx Config
`deploy` only ever uploads the `lfr-tunneld` binary and static assets -- a fix to the nginx config template (e.g. the #979 ACME-fallback location block) only ever reached a box on its *initial* provision, never on a normal `deploy` (#997). Use `reconcile-nginx` to regenerate the current central's nginx config and push it to an already-provisioned box. Safe to re-run repeatedly: it backs up the existing config, swaps in the new one, runs `nginx -t`, and only reloads if that passes -- otherwise it restores the backup and reloads that instead, so a bad reconcile can't leave the box without a working config. `scripts/common/setup-central-vps.sh` (initial provisioning) generates its nginx config the same way, via `lfr-tunnel-ops render-nginx-config` -- the two paths share exactly one template now and can't drift apart from each other again (#1026).
```bash
./bin/lfr-tunnel-ops reconcile-nginx
```
`-domains`/`-port` can be passed as flags to override `lfr-tunnel-ops.yaml`'s `nginx:` section for a one-off run; otherwise they're required there. `-port` must match the live `server-config.yaml`'s `http_bind_addr` port.

**`-domains` takes APEX domains, not hostnames, and the list must be complete.** Each entry
generates two server blocks -- `<entry>` and `*.<entry>` -- and reads its certificate from
`/etc/letsencrypt/live/<entry>/`. Liferay's central is therefore
`-domains lfr-demo.se,lfr-demo.online`, **not** `tunnel.lfr-demo.se`: the wildcard certificates
are issued for the apex names, and `tunnel.` is served by the `*.lfr-demo.se` block.

Two ways to get this wrong, and only one of them is caught for you:

- **A hostname instead of an apex** asks for `live/tunnel.lfr-demo.se/`, which does not exist.
  `nginx -t` fails, reconcile rolls back, nothing is harmed. Loud and safe.
- **An incomplete list is not caught.** The generated config replaces the whole file, so a
  domain group left out is a vhost deleted. A single-entry list on this two-domain gateway
  removes `lfr-demo.online` entirely -- and that config is perfectly valid, so `nginx -t`
  passes and it applies. On 2026-08-26 exactly this was attempted with
  `-domains tunnel.lfr-demo.se`; the missing certificate is the only reason it failed instead
  of taking `lfr-demo.online` offline.

So read the live config before running it, every time:

```bash
ssh <target> 'sudo grep -E "server_name|ssl_certificate " /etc/nginx/sites-available/lfr-tunnel'
```

and confirm the rendered output matches those pairings before applying:

```bash
./bin/lfr-tunnel-ops render-nginx-config -domains <list> -port <port> | grep -E "server_name|ssl_certificate "
```

`render-nginx-config` writes to stdout and touches nothing, so this is free.

**A release that changes the nginx template needs this step, and `deploy` will not tell you.**
`deploy` only ships the binary and static assets. After deploying a release, check whether the
template moved -- `git diff <previous-tag>..<tag> -- pkg/ops/nginx.go` -- and reconcile every box
if it did. v1.48.6 changed `X-Forwarded-For` from `$proxy_add_x_forwarded_for` to `$remote_addr`
and that change reached no gateway until reconcile was run separately.

---

## 6. Maintenance and Recovery Operations

Manage Nginx maintenance mode or perform safe database backups/restores on the VPS.

- **Enable Maintenance Mode** (serves static Liferay-themed maintenance page):
  ```bash
  ./bin/lfr-tunnel-ops maintenance enable
  ```
- **Disable Maintenance Mode**:
  ```bash
  ./bin/lfr-tunnel-ops maintenance disable
  ```
- **Safe Restore Backup** (run directly on the VPS as root — this is not part of `lfr-tunnel-ops` and takes no SSH identity flag; it automatically enables maintenance mode, restores the DB, and disables maintenance):
  ```bash
  sudo ./scripts/common/restore-with-maintenance.sh [backup_file]
  ```

---

## 7. VPS Diagnostics

Run remote diagnostic checks on the VPS (system uptime/load, systemd service status, Nginx config test, UFW firewall rules, Let's Encrypt certificate status, and recent `lfr-tunneld` error logs):
```bash
./bin/lfr-tunnel-ops diagnose
```


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-26* | *Last Reviewed: 2026-08-26*
