# Liferay Tunnel (lfr-tunnel)

`lfr-tunnel` is an open-source, MIT-licensed tunneling utility tailored for Liferay Development and Sales Engineering (SE) teams. It allows local Liferay runtime environments (including LDM workspaces, standalone Liferay Tomcat bundles, and Liferay Docker containers) to be securely exposed through dynamic wildcard subdomains on public domain endpoints.

Unlike generic tunnels, `lfr-tunnel` offers:
- **Zero-Config Port Matching**: Automatically scans Liferay Workspace directories, parses `client-extension.yaml` files, and exposes all client extension asset ports automatically.
- **Automatic Multi-Port Tunneling**: Maps the main Liferay instance (port `8080`) and all client extensions (e.g. port `3000`) under a single subdomain prefix (e.g. `alpha-se.yourdomain.com` and `alpha-se-my-extension.yourdomain.com`).
- **Liferay Header Injection**: Intercepts request headers to inject the correct `X-Forwarded-Host`, `X-Forwarded-Proto`, and client IP headers required for Liferay virtual host mappings and OAuth2 redirect URIs.
- **Beautiful Offline Page**: Serves a premium, Liferay-themed splash/offline fallback screen when a developer machine disconnects.

---

## Supported Domains

The Liferay Tunnel gateway is configured to only support routing and DNS wildcard resolution on the following domains:
- **`lfr-demo.se`**: Primary domain for Sales Engineering demonstrations.
- **`lfr-demo.online`**: Secondary domain mirroring and proxying to the primary gateway.

Any developer tunnel project prefix must be established as a subdomain of one of these two domains (e.g. `your-project.lfr-demo.se` or `your-project.lfr-demo.online`).

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
lfr-tunnel -server https://lfr-demo.se -token <your-token> -subdomain alpha-se
```

Will yield:
1. **Workspace Scanning**: Detects port `8080` (default Liferay) and port `3001` (from the client extension).
2. **Subdomain Mapping**: Generates wildcard URLs for both active domains:
   - `https://alpha-se.lfr-demo.se` ──► Local Liferay (`8080`)
   - `https://alpha-se-my-custom-element.lfr-demo.se` ──► Local Custom Element Server (`3001`)
   - `https://alpha-se.lfr-demo.online` ──► Local Liferay (`8080`)
   - `https://alpha-se-my-custom-element.lfr-demo.online` ──► Local Custom Element Server (`3001`)

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
   lfr-tunnel -server https://lfr-demo.se -token <your-token> -subdomain dev-tomcat -ports 8080
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
3. Under the default instance, set the Virtual Host name to: `dev-tomcat.lfr-demo.se`.
4. Now, any incoming request hitting `https://dev-tomcat.lfr-demo.se` will resolve to the correct virtual instance, preserving clean redirects and cookies.

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
     -server https://lfr-demo.se \
     -token <your-token> \
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

For a full routing walkthrough, read the [Architecture & Routing Guide](architecture.md).

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

### Binary Installation

1. **Build the CLI**:
   ```bash
   go build -o lfr-tunnel ./cmd/lfr-tunnel
   ```
2. **Move to PATH** (optional):
   ```bash
   mv lfr-tunnel /usr/local/bin/
   ```

### Running via Docker (Recommended / EDR Bypass)

If your local environment is protected by security endpoint agents (such as SentinelOne, Defender, or CrowdStrike) that flag or quarantine Go compilers or custom network tunnel tools, running `lfr-tunnel` inside a Docker container is the recommended best practice. 

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
auth_token: "se-shared-secret-key"
subdomain: "alpha-se"
ports:
  - 8080
```

Run using the configuration file:
```bash
lfr-tunnel -config client-config.yaml
```

---

## Future Roadmap

The following server-side administrative capabilities are planned for future versions of `lfr-tunnel`:

1.  **Administrative Web Dashboard**:
    *   A secure web portal (e.g. at `https://tunnel.lfr-demo.se/admin`) to inspect all active subdomains and target ports.
    *   Visual representation of current traffic throughput and latency.
2.  **Audit Logs & Tracking**:
    *   Trace which specific client sessions (e.g., developer name, host machine hostname) registered and exposed each subdomain.
3.  **Active Lease Management**:
    *   Allow administrators to manually terminate (kick) an active subdomain lease or block specific client prefixes directly from the web dashboard.
4.  **Security Integration**:
    *   Support OAuth2/OIDC integration (e.g. log in with Okta/GitHub) for developers registering tunnels, rather than relying solely on a shared secret `auth_token`.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
