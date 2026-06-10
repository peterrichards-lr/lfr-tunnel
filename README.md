# Liferay Tunnel (lfr-tunnel)

`lfr-tunnel` is an open-source, MIT-licensed tunneling utility tailored for Liferay Development and Sales Engineering (SE) teams. It allows multiple local LDM (Liferay Development Manager) or Liferay instances to be securely exposed through dynamic wildcard subdomains on public domain endpoints.

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

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
