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

## Supported Liferay Runtimes

Because `lfr-tunnel` operates at the network port level, it is fully runtime-agnostic and supports exposing the following local development setups:
1.  **LDM (Liferay Development Manager)**: Auto-detects port configurations from your active Liferay workspace files.
2.  **Liferay Tomcat Bundles**: Works out of the box with native Tomcat zip bundles running directly on your host machine.
3.  **Liferay Docker Containers (non-LDM)**: Exposes any local Liferay instances running inside Docker containers, provided their ports (e.g. `8080`) are mapped to your local loopback interface.

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

#### 1. Build the Docker Image
Build the Docker image locally from the repository root:
```bash
docker build --load -t lfr-tunnel-client:latest .
```

#### 2. Run the Containerized Client
To start the tunnel, run the container and specify the target host as `host.docker.internal` using the `LFT_TARGET_HOST` environment variable:
```bash
docker run --rm -it \
  -e LFT_TARGET_HOST=host.docker.internal \
  lfr-tunnel-client:latest \
  -server https://lfr-demo.se \
  -token cf0927dfa60b943d002a2491a629b350 \
  -subdomain test-se \
  -ports 8080
```

*   `LFT_TARGET_HOST=host.docker.internal` directs the tunnel client to forward requests from the public gateway to the macOS host system instead of the container loopback interface.
*   The container will output your public URLs (e.g. `https://test-se.lfr-demo.se`) and connect automatically.

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
