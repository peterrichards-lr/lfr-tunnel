# Liferay Tunnel (lfr-tunnel)

`lfr-tunnel` is an open-source, MIT-licensed tunneling utility tailored for Liferay Development and Sales Engineering (SE) teams. It allows local Liferay runtime environments (including LDM workspaces, standalone Liferay Tomcat bundles, and Liferay Docker containers) to be securely exposed through dynamic wildcard subdomains on public domain endpoints.

Unlike generic tunnels, `lfr-tunnel` offers:
- **Zero-Config Port Matching**: Automatically scans Liferay Workspace directories, parses `client-extension.yaml` files, and exposes all client extension asset ports automatically.
- **Automatic Multi-Port Tunneling**: Maps the main Liferay instance (port `8080`) and all client extensions (e.g. port `3000`) under a single subdomain prefix (e.g. `alpha-se.yourdomain.com` and `alpha-se-my-extension.yourdomain.com`).
- **Liferay Header Injection**: Intercepts request headers to inject the correct `X-Forwarded-Host`, `X-Forwarded-Proto`, and client IP headers required for Liferay virtual host mappings and OAuth2 redirect URIs.
- **Beautiful Offline Page**: Serves a premium, Liferay-themed splash/offline fallback screen when a developer machine disconnects.

---

## Domain Configuration

`lfr-tunnel` is designed to work with **any** two domains you control. When deploying your own gateway, you configure the domains via the `domain1` and `domain2` fields in `server-config.yaml`:

```yaml
domain1: "yourdomain.com"
domain2: "yourdomain.org"
```

The gateway will then issue wildcard subdomain URLs on both domains for every registered tunnel (e.g. `your-project.yourdomain.com` and `your-project.yourdomain.org`).

> [!NOTE]
> **Getting Started**: If you are setting up `lfr-tunnel` for the first time, please read the [**Getting Started Guide**](docs/getting_started.md). The client CLI is of no use standalone and requires a valid Personal Access Token (PAT) from a gateway server to connect.
>
> **Liferay Sales Engineering Team**: If you are a member of the Liferay SE team connecting to the shared hosted gateway, please read the [**Liferay SE Quick-Start Guide**](docs/liferay-se-guide.md) which has team-specific instructions, domain details, and registration steps.

---

## Supported Liferay Runtimes & Usage Examples

Because `lfr-tunnel` operates at the network port level, it is fully runtime-agnostic. Below are detailed usage examples and step-by-step configurations for the three most common local Liferay development environments.

### 1. LDM (Liferay Development Manager) / Workspace Projects

LDM workspaces typically orchestrate the main Liferay portal instance alongside one or more Liferay Client Extensions (e.g., React custom elements, custom theme contributors, or standalone backend services). 

#### Zero-Config Automatic Port Scanning
When running the native client binary `lfr-tunnel` from the root of a Liferay Workspace, the client automatically crawls all workspace subdirectories, detects `client-extension.yaml` (or `.yml`) files, and extracts their configured development ports.

For example, suppose your workspace contains a custom element project with the following `client-extension.yaml`:
```yaml
my-custom-element:
    name: My Custom Element
    type: customElement
    port: 3001
```

Running the tunnel in the workspace root:
```bash
lfr-tunnel -server https://tunnel.yourdomain.com -subdomain alpha-se
```

Will yield:
1. **Workspace Scanning**: Detects port `8080` (default Liferay) and port `3001` (from the client extension).
2. **Subdomain Mapping**: Generates wildcard URLs for both active domains:
   - `https://alpha-se.yourdomain.com` ──► Local Liferay (`8080`)
   - `https://alpha-se-my-custom-element.yourdomain.com` ──► Local Custom Element Server (`3001`)
   - `https://alpha-se.yourdomain.org` ──► Local Liferay (`8080`)
   - `https://alpha-se-my-custom-element.yourdomain.org` ──► Local Custom Element Server (`3001`)

> [!IMPORTANT]
> **Docker Wrapper Scanning Limitation**  
> If you run the tunnel client via the Dockerized wrapper scripts (`./lfr-tunnel.sh`, `lfr-tunnel.bat`, `lfr-tunnel.ps1`), directory scanning is isolated inside the Docker container. You must explicitly pass your ports using the `-ports` argument or define them in your `.env` configuration:
> ```bash
> # Run using the Docker wrapper and pass ports manually
> ./lfr-tunnel.sh -subdomain alpha-se -ports 8080,3001
> ```

---

### 2. Standalone Liferay Tomcat Bundles (Native Run)

If you are running a native Tomcat bundle (e.g. `liferay-ce-portal-7.4.3.112-ga112`) directly on your host machine, it binds to `127.0.0.1:8080` by default.

#### Launching the Tunnel
1. **Via Native Binary**:
   ```bash
   lfr-tunnel -server https://tunnel.yourdomain.com -subdomain dev-tomcat -ports 8080
   ```
2. **Via Docker Wrapper**:
   Since the wrapper defaults to exposing port 8080, you can simply run:
   ```bash
   ./lfr-tunnel.sh -subdomain dev-tomcat
   ```

#### Liferay Virtual Host Configuration
To verify that absolute links, redirect URIs, and resource paths render correctly through the proxy:
1. Log into your local Liferay instance (`http://localhost:8080`).
2. Navigate to **Control Panel ──► Instance Settings ──► Virtual Hosts**.
3. Under the default instance, set the Virtual Host name to: `dev-tomcat.yourdomain.com`.
4. Now, any incoming request hitting `https://dev-tomcat.yourdomain.com` will resolve to the correct virtual instance, preserving clean redirects and cookies.

---

### 3. Liferay Running in a Local Docker Container

If your Liferay development server is running in a local Docker container (non-LDM, e.g. via `docker run` or `docker-compose`), you must configure how the tunnel connects to it.

#### Scenario A: Using Host Port Mapping (Recommended)
If your Liferay container maps port `8080` to the host machine's port `8080` (e.g., `docker run -p 8080:8080 liferay/portal`):

1. **Native Client**: The native `lfr-tunnel` CLI runs on the host and targets `localhost:8080` directly.
2. **Dockerized Client Wrapper**:
   The wrapper container runs with `-e LFT_TARGET_HOST=host.docker.internal` configured automatically. This tells Chisel to route the inbound tunnel traffic from the gateway server back out of the tunnel container to the host loopback port `8080`.
   
   **Run the wrapper**:
   ```bash
   ./lfr-tunnel.sh -subdomain dev-docker
   ```

#### Scenario B: Using a Shared Docker Network (Container-to-Container)
If you want to run `lfr-tunnel` inside its own Docker container and have it route traffic directly to the Liferay container *without* publishing ports to the host loopback:

1. **Create/Identify the Docker Network**:
   Ensure both containers are on the same bridge network (e.g., `liferay-net`).
2. **Run the Liferay Portal Container**:
   ```bash
   docker run --name liferay-portal --network liferay-net -d liferay/portal:latest
   ```
3. **Run the Client Container**:
   Build the client image:
   ```bash
   docker build -t lfr-tunnel-client .
   ```
   Run the client, overriding the target host to match the Liferay container's network name (`liferay-portal`):
   ```bash
    docker run --rm -it \
      --network liferay-net \
      -e LFT_TARGET_HOST=liferay-portal \
      lfr-tunnel-client \
      -server https://tunnel.yourdomain.com \
      -subdomain dev-docker \
      -ports 8080
   ```

---

## Architecture Quick Look

```
[ Visitor Browser ] 
        │ (HTTPS)
        ▼
[ Public VPS (Caddy/Nginx TLS Term) ]
        │ (HTTP - Port 80)
        ▼
[ lfr-tunneld (Gateway Server) ]
        │ (WebSocket tunnel on Port 8081)
        ▼
[ lfr-tunnel CLI (Developer Laptop) ] ──► [ Local Liferay Instance (8080) ]
                                       └──► [ Local Frontend Assets (3000) ]
```

For a full routing walkthrough, read the [Architecture & Routing Guide](docs/architecture.md).

---

## 1. Server Gateway Installation Guide (`lfr-tunneld`)

The server gateway should be deployed on a public VPS (like a $5 DigitalOcean or Hetzner droplet) with wildcard DNS records (`*.yourdomain.com`) pointing to the VPS IP address.

### Binary Installation

1. **Build the binary**:
   ```bash
   go build -o lfr-tunneld ./cmd/lfr-tunneld
   ```
2. **Start the gateway**:
   ```bash
   ./lfr-tunneld \
     -domain1 yourdomain.com \
     -token se-shared-secret-key \
     -http-bind :8080
   ```
   *Note: Binding to a high port like `8080` allows you to run it behind a reverse proxy like Caddy or Nginx without needing root permissions.*

### Production Setup with Caddy (Recommended)

Caddy automatically handles wildcard Let's Encrypt certificate generation and renewal.

1. **Install Caddy** with your DNS provider plugin (needed for Let's Encrypt DNS-01 wildcard verification).
2. **Create a `Caddyfile`**:
   ```caddy
   *.yourdomain.com, yourdomain.com {
       # Use your DNS provider plugin for wildcard SSL
       tls {
           dns route53 # Example for AWS Route53
       }

       # Route traffic to the lfr-tunneld gateway
       reverse_proxy localhost:8080 {
           # Enable websocket upgrading
           header_up Host {host}
           header_up X-Real-IP {remote_host}
       }
   }
   ```
3. Run Caddy as a systemd service.

---

## 2. Client CLI Installation & Usage (`lfr-tunnel`)

Developers run `lfr-tunnel` locally on their developer machines.

### Quick One-Line Installation

The installer downloads the pre-compiled binary for your platform and places it in `~/bin/lfr-tunnel` — the single canonical install location. This consistent path makes it simple for your organisation's security team to apply exclusions.

#### macOS — Homebrew (Recommended)

```bash
brew tap peterrichards-lr/tap
brew trust peterrichards-lr/tap
brew install lfr-tunnel
```

Homebrew verifies the SHA-256 checksum independently and removes the macOS quarantine attribute automatically. Install path: `/opt/homebrew/bin/lfr-tunnel` (Apple Silicon) or `/usr/local/bin/lfr-tunnel` (Intel).

#### Windows — Scoop (Recommended)

```powershell
scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket
scoop install lfr-tunnel
```

Scoop verifies the SHA-256 checksum independently. Creates a shim at `~\scoop\shims\lfr-tunnel.exe` on your `PATH`.

#### macOS / Linux — Direct Download (Fallback)

Use this for Linux, CI/CD pipelines, or environments where Homebrew is not available:

```bash
curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.sh | sh
```

Places the binary at `~/bin/lfr-tunnel`. If `~/bin` is not yet in your `PATH`, the installer prints the one-line export to add to your shell profile.

#### Windows — Direct Download, PowerShell (Fallback)

Use this for environments where Scoop is not available:

```powershell
iwr https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.ps1 | iex
```

Places the binary at `%USERPROFILE%\bin\lfr-tunnel.exe` and adds it to your user `PATH` automatically.

#### Verifying Binary Integrity

All release binaries are covered by **GitHub Artifact Attestations** — cryptographic, OIDC-backed provenance records that prove the binary was produced by the official GitHub Actions workflow at a specific commit.

Verify any downloaded binary using the GitHub CLI:
```bash
gh attestation verify ~/bin/lfr-tunnel --repo peterrichards-lr/lfr-tunnel
```

SHA-256 checksums for each release are also available in `checksums.txt` on the [Releases](https://github.com/peterrichards-lr/lfr-tunnel/releases) page.

### Versioning & Self-Upgrading

You can check the current version of the client or upgrade it to the latest release at any time:

*   **Check Version**:
    ```bash
    lfr-tunnel -version
    ```
*   **Self-Upgrade**:
    ```bash
    lfr-tunnel -upgrade
    ```

### Build from Source

1. **Build the CLI**:
   ```bash
   go build -ldflags="-s -w" -trimpath -o lfr-tunnel ./cmd/lfr-tunnel
   ```
2. **Move to the canonical install location**:
   ```bash
   mkdir -p ~/bin && mv lfr-tunnel ~/bin/
   # Ensure ~/bin is in your PATH:
   export PATH="$HOME/bin:$PATH"
   ```

### Running via Docker (Alternative / EDR Bypass)

The recommended approach for managing security endpoint agent (EDR) alerts is to install `lfr-tunnel` to `~/bin/` and ask your security admin to add a wildcard path exclusion for `/Users/*/bin/lfr-tunnel` (macOS) or `C:\Users\*\bin\lfr-tunnel.exe` (Windows). See the [Security & EDR Compatibility](#security--edr-compatibility) section for details.

If a path exclusion cannot be applied, running `lfr-tunnel` inside a Docker container is an alternative. 

This isolates the execution inside an unmonitored virtual machine while routing traffic back to your host machine's port `8080` (where your Liferay server is running).

#### 1. Setup Environment Configuration
Copy the `.env.example` file to `.env` in the repository root and fill in your token:
```bash
cp .env.example .env
```
*(The `.env` file is git-ignored and contains your sensitive `LFT_TOKEN` credentials).*

#### 2. Run the Client Wrapper Script
To start the tunnel, run the wrapper script matching your operating system. It will automatically build the Docker image if it is missing, load your `.env` variables, and establish the tunnel:

*   **macOS / Linux (Bash)**:
    ```bash
    ./lfr-tunnel.sh
    ```
*   **Windows CMD**:
    ```cmd
    lfr-tunnel.bat
    ```
*   **Windows PowerShell**:
    ```powershell
    .\lfr-tunnel.ps1
    ```

*Note: You can override any environment configuration on the fly by passing standard client CLI arguments directly to the script, e.g. `./lfr-tunnel.sh -subdomain my-temp-se`.*

#### 3. Docker Hub Image (Zero-Install & Zero-Build Method)

You can run our pre-built, multi-architecture client container directly from Docker Hub! This requires absolutely **no local repository cloning, zero building, and is 100% immune to SentinelOne/EDR host-level alerts**:

```bash
docker run -d --name lfr-tunnel \
  -e LFT_CLIENT_TOKEN="YOUR_PERSONAL_ACCESS_TOKEN" \
  -p 8080:8080 \
  peterrichards/lfr-tunnel:latest \
  -server https://tunnel.lfr-demo.se \
  -subdomain peter-dev \
  -ports 8080
```

*   **`-d`**: Runs the tunnel in the background as an isolated daemon.
*   **`-e LFT_CLIENT_TOKEN`**: Passes your secure developer PAT.
*   **`-p 8080:8080`**: Exposes the port.
*   **`peterrichards/lfr-tunnel:latest`**: Pulls the official, pre-scanned, and optimized image dynamically from Docker Hub (supporting both Apple Silicon M1/M2/M3 and Intel natively!).
*   **`-server`**: Points to your production server gateway.
*   **`-ports 8080`**: Explicitly routes the incoming wildcard traffic from the gateway back out of the tunnel container to your host Liferay port `8080`.

### Secret Leak Prevention (Pre-Commit Hook)

To prevent API keys, tokens, or passwords from ever being accidentally committed to the repository, we use **Gitleaks** packaged inside a Docker container. This scans your staged files automatically on every commit.

To enable the hook on your local machine, run:
```bash
make install-hook
```

If Gitleaks detects a secret, the commit will be blocked. If it flags a false positive, Gitleaks will output a fingerprint hash. You can copy that hash and paste it in a new line inside `.gitleaksignore` in the root of the project to whitelist it.

### Quick Start (Zero-Config Mode)

Navigate to the root directory of your Liferay Workspace (which contains your client extensions) and start the tunnel:

```bash
lfr-tunnel -server https://tunnel.yourdomain.com -token se-shared-secret-key -subdomain alpha-se
```

- `lfr-tunnel` will automatically scan the current directory for `client-extension.yaml` files.
- It will detect your ports (e.g. `8080` for the Liferay instance, `3000` for your React custom element).
- It will print the active endpoints:
  - `https://alpha-se.yourdomain.com` ──► Local Liferay (`8080`)
  - `https://alpha-se-my-extension-id.yourdomain.com` ──► Local Extension Assets (`3000`)
- Press `Ctrl+C` to cleanly disconnect and release your subdomains.

### Manual Port Configuration

If you want to manually specify which local ports to expose (bypassing workspace auto-detection):

```bash
lfr-tunnel \
  -server https://tunnel.yourdomain.com \
  -token se-shared-secret-key \
  -subdomain alpha-se \
  -ports 8080,3000,9000
```

### Client Config File (`client-config.yaml`)

To avoid typing the server and token every time, create a `client-config.yaml` file in your home directory or workspace:

```yaml
server_url: "https://tunnel.yourdomain.com"
subdomain: "alpha-se"
ports:
  - 8080
```

Run using the configuration file:
```bash
lfr-tunnel -config client-config.yaml
```

### Running in the Background

By default, `lfr-tunnel` runs as a foreground process blocking the terminal. You can run the client in the background using the `-background` flag:

```bash
lfr-tunnel -config client-config.yaml -background
```

When started with `-background`:
* It spawns a detached background process and prints the child PID.
* Logs/outputs are redirected to `~/.lfr-tunnel/client.log`.

To check if the background tunnel is currently active:
```bash
lfr-tunnel -status
```

To gracefully stop the background tunnel and release subdomains on the gateway:
```bash
lfr-tunnel -stop
```
*(This sends a SIGINT to trigger clean teardown, falling back to a force-kill if it doesn't respond in 2 seconds).*


### Loading Credentials Securely

To avoid storing your sensitive `auth_token` in the workspace-specific `client-config.yaml` (which might be committed to source control), the client can securely load the token from a file:

1. **Default Path**: Save your token value directly to `~/.lfr-tunnel/token`.
2. **Custom Path**: Save your token to a file and set the `LFT_TOKEN_FILE` environment variable to point to it:
   ```bash
   export LFT_TOKEN_FILE="/path/to/your/secure/token"
   ```

If the token file exists, the client will automatically load it on startup, allowing you to safely omit the `auth_token` field from your configuration files.


---

## Enterprise-Ready Security & Administration

`lfr-tunnel` includes a built-in **Admin Web Dashboard** and **Identity Provider (IdP)**.

1. **OAuth2 / Magic Link Authentication**: No shared secrets. Developers register for accounts and authenticate via secure, passwordless Magic Links (or SSO).
2. **Personal Access Tokens (PATs)**: Each developer generates scoped PATs to authenticate their CLI tunnels.
3. **Admin Web Dashboard**: A responsive Light/Dark mode web portal to inspect all active subdomains and target ports.
4. **Audit Logs & Tracking**: Trace which specific client sessions registered each subdomain, and view full historical access logs.
5. **DDoS Protection & Rate Limiting**: Built-in sliding-window rate limiters automatically ban malicious IPs abusing the tunnel endpoints.
6. **Telemetry & Analytics**: Track global and per-user bandwidth usage, and monitor the distribution of client OS/versions connecting to the gateway.

## Headless Testing Stack & State Coordinator

To facilitate reliable, asynchronous orchestration of E2E integration tests (such as CI workflows, external test runners, or local automation scripts), the repository implements the **State Coordinator Pattern** using a lightweight progress mailbox. 

A plain-text file named `.progress-signal` is generated at the workspace root during test execution, signaling the exact status of the lifecycle:

1. **`BUILDING`**: Written at the very beginning of compiling the binaries or packaging Docker services.
2. **`WAITING_HEALTHY`**: Staged when services are running and container health checks or port warm-ups are being verified.
3. **`TESTING`**: Staged the exact millisecond the actual test curl/assertions runner begins.
4. **`SUCCESS`**: Exited and written if all E2E integration test assertions pass cleanly (Exit Code 0).
5. **`FAILED`**: Written if any step, compilation, health check, or assertion fails or times out (Exit Code > 0).

### Querying Progress

External tools or CI pipelines can poll the `.progress-signal` file to track progress dynamically without scanning long log files:

```bash
while true; do
  STATUS=$(cat .progress-signal 2>/dev/null || echo "No signal")
  echo "Current State: $STATUS"
  if [ "$STATUS" = "SUCCESS" ] || [ "$STATUS" = "FAILED" ]; then
    break
  fi
  sleep 2
done
```

## Security & EDR Compatibility

`lfr-tunnel` is an unsigned binary (it does not hold an Apple Developer ID or Windows Authenticode certificate). Endpoint Detection and Response (EDR) agents such as SentinelOne, CrowdStrike, and Defender may flag it as suspicious based on static heuristics. This is a known false positive caused by the way Go binaries embed their runtime and by the absence of an OS-level publisher certificate.

### Why Binaries Are Unsigned

Obtaining an **Apple Developer ID** (required to appear as `Type=Signed` on macOS) costs $99/year and requires membership of Apple's Developer Program. **Windows Authenticode** certificates require a paid CA certificate and an HSM-backed key. Neither of these is required to run the software — they are purely trust signals consumed by the operating system loader and by EDR engines.

### What We Do Instead

All release binaries are built with the following hardening flags to minimise the static analysis surface:

```
-ldflags="-s -w"  # Strip debug info and symbol table
-trimpath          # Remove local filesystem paths embedded in the binary
```

Every release binary is also covered by a **GitHub Artifact Attestation** — a free, keyless, OIDC-backed cryptographic provenance record (powered by Sigstore) that cryptographically proves the binary was produced by the official GitHub Actions workflow at a specific commit SHA, and has not been tampered with since.

Verify any downloaded binary with the GitHub CLI:
```bash
gh attestation verify ~/bin/lfr-tunnel --repo peterrichards-lr/lfr-tunnel
```

### Recommended SentinelOne Exclusion (for Admins)

The canonical install location for `lfr-tunnel` is `~/bin/lfr-tunnel` on all platforms:

| Platform | Canonical Path |
|---|---|
| macOS | `/Users/*/bin/lfr-tunnel` |
| Linux | `/home/*/bin/lfr-tunnel` |
| Windows | `C:\Users\*\bin\lfr-tunnel.exe` |

We recommend that your SentinelOne (or equivalent EDR) administrator add a **wildcard path exclusion** for the paths above. Because all users install to the same predictable location, a single wildcard rule covers the entire team without needing per-user or per-hash exceptions.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
