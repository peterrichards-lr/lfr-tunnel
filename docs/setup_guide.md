# Liferay Tunnel (lfr-tunnel) End-to-End Server & DNS Setup Guide

This guide walks you through setting up a complete, production-grade `lfr-tunnel` gateway server from scratch. By following this guide, you will be able to replicate the exact infrastructure used for the official Sales Engineering gateway using your own public domains and VPS hosting.

---

## 1. Domain & DNS Configuration

Before setting up your VPS, you must configure your domain name to route visitor requests and developer tunnels to your gateway's public IP address.

### 1.1. Required DNS Records
Log into your DNS provider (e.g., Cloudflare, Route53, GoDaddy) and add the following records for your domain (e.g. `yourdomain.com`):

| Type  | Name      | Value                 | TTL   | Proxy Status (Cloudflare) | Description |
|-------|-----------|-----------------------|-------|----------------------------|-------------|
| **A** | `@`       | `YOUR_VPS_PUBLIC_IP`  | Auto  | **DNS Only** (Grey Cloud)  | Root domain pointing to VPS |
| **A** | `tunnel`  | `YOUR_VPS_PUBLIC_IP`  | Auto  | **DNS Only** (Grey Cloud)  | Control plane registration endpoint |
| **A** | `*`       | `YOUR_VPS_PUBLIC_IP`  | Auto  | **DNS Only** (Grey Cloud)  | Wildcard for active developer tunnels |

> [!IMPORTANT]
> **Disable Cloudflare Proxy (CDN)**  
> You **MUST** set the Proxy Status to **DNS Only (Grey Cloud)**. If Cloudflare proxies the traffic (Orange Cloud), it will interfere with Chisel's persistent WebSocket connections, block wildcard SSL verification via Nginx, and break direct TCP forwarding.

### 1.2. Email Security TXT Records
Because `lfr-tunneld` can send emails for user registration and administrative approvals, you must configure SPF, DKIM, and DMARC records to prevent mail servers from rejecting or flagging notifications as spam/forgery:

*   **SPF (Sender Policy Framework)**: Add a TXT record for `@`:
    ```text
    v=spf1 ip4:YOUR_VPS_PUBLIC_IPV4 ip6:YOUR_VPS_IPV6 -all
    ```
*   **DMARC (Domain-based Message Authentication)**: Add a TXT record for `_dmarc`:
    ```text
    v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s;
    ```
*   **DKIM (DomainKeys Identified Mail)**: Configure a TXT record `*._domainkey`:
    ```text
    v=DKIM1; p=
    ```

---

## 2. VPS Server Setup & Security Hardening

Provision a clean VPS running **Ubuntu 22.04 LTS** or **Ubuntu 24.04 LTS** (e.g., on DigitalOcean, Hetzner, AWS, or Linode) with at least 1 vCPU and 1GB RAM.

### 2.1. Basic OS & Package Updates
Once logged in via SSH as `root`, update all system packages:
```bash
apt update && apt upgrade -y
```

### 2.2. Create a Restricted Sudo User
Do not run services or manage the server as `root` directly. Create a new administrative user (e.g. `adminuser`):
```bash
# Add the new user
adduser adminuser

# Grant sudo permissions
usermod -aG sudo adminuser
```

### 2.3. Hardening SSH Configuration
Disable root login and password-based authentication to prevent brute-force attacks.

1.  **Authorize your SSH Key** for the new user:
    ```bash
    # Switch to the new user
    su - adminuser
    mkdir -p ~/.ssh
    chmod 700 ~/.ssh
    
    # Paste your public SSH key into authorized_keys
    nano ~/.ssh/authorized_keys
    chmod 600 ~/.ssh/authorized_keys
    exit
    ```

2.  **Modify the SSH Daemon Configuration**:
    Open `/etc/ssh/sshd_config`:
    ```bash
    sudo nano /etc/ssh/sshd_config
    ```
    Ensure the following directives are configured:
    ```text
    PermitRootLogin no
    PasswordAuthentication no
    PubkeyAuthentication yes
    ```

3.  **Restart the SSH service**:
    ```bash
    sudo systemctl restart sshd
    ```
    *Note: Keep your current terminal open and test logging in via a separate terminal to verify you can connect successfully before exiting.*

### 2.4. Configure the Firewall (UFW)
Only expose necessary ports. Block all other traffic:
```bash
# Allow SSH
sudo ufw allow ssh

# Allow HTTP (port 80) and HTTPS (port 443)
sudo ufw allow http
sudo ufw allow https

# Enable the firewall
sudo ufw enable

# Check status
sudo ufw status
```

---

## 3. Nginx Reverse Proxy & Let's Encrypt Wildcard SSL

`lfr-tunneld` runs on localhost, while Nginx acts as the public-facing entrypoint, handling SSL termination and proxying traffic to the backend.

### 3.1. Install Nginx and Certbot
Install Nginx, Certbot, and the Certbot DNS plugin for your DNS provider (e.g., Cloudflare):
```bash
sudo apt install -y nginx certbot python3-certbot-nginx python3-certbot-dns-cloudflare
```

### 3.2. Obtain Wildcard SSL Certificates
Because dynamic developer subdomains (e.g., `*.yourdomain.com`) are routed on this server, you must obtain a wildcard certificate. Let's Encrypt only supports wildcard validation using the **DNS-01 challenge**.

1.  Create a Cloudflare API Token with permissions to edit your domain's DNS zone files.
2.  Save the token in a secure file on the VPS:
    ```bash
    sudo mkdir -p /etc/letsencrypt
    sudo nano /etc/letsencrypt/cloudflare.ini
    ```
    Write the following:
    ```ini
    dns_cloudflare_api_token = YOUR_CLOUDFLARE_API_TOKEN
    ```
    Secure the credentials file:
    ```bash
    sudo chmod 600 /etc/letsencrypt/cloudflare.ini
    ```
3.  Run Certbot to fetch wildcard certificates for both root and wildcard subdomains:
    ```bash
    sudo certbot certonly \
      --dns-cloudflare \
      --dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini \
      --agree-tos \
      --no-eff-email \
      -m admin@yourdomain.com \
      -d yourdomain.com \
      -d *.yourdomain.com
    ```

### 3.3. Configure Nginx Configuration
Create an Nginx configuration file at `/etc/nginx/sites-available/lfr-tunnel`:
```bash
sudo nano /etc/nginx/sites-available/lfr-tunnel
```

Paste the following configuration (replace `yourdomain.com` with your actual domain):
```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

# 1. HTTP to HTTPS Force Redirect
server {
    listen 80;
    listen [::]:80;
    server_name yourdomain.com *.yourdomain.com;

    return 301 https://$host$request_uri;
}

# 2. Control Plane & Gateway Landing Page
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name yourdomain.com tunnel.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/yourdomain.com/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    # Root landing page (redirects to main page or corporate site)
    location / {
        return 307 https://www.liferay.com;
    }

    # Proxy CLI registration API to lfr-tunneld
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Proxy Chisel WebSocket handshake endpoint
    location /tunnel {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

# 3. Wildcard Subdomain Data Plane Routing
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name *.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/yourdomain.com/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;

        # Increase timeouts for uploading large files/assets
        client_max_body_size 500M;
        proxy_connect_timeout 120s;
        proxy_send_timeout 120s;
        proxy_read_timeout 120s;
    }
}
```

Enable the Nginx configuration and restart Nginx:
```bash
sudo ln -s /etc/nginx/sites-available/lfr-tunnel /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl restart nginx
```

---

## 4. Install & Configure `lfr-tunneld`

### 4.1. Create a Restricted System User
To secure the host, create a dedicated system user `lfr-tunnel` with no shell or home directory to execute the daemon process:
```bash
sudo useradd -r -s /bin/false lfr-tunnel
```

### 4.2. Build the Server Binary
Build the Go binary locally on your computer and copy it to the VPS, or build it directly on the VPS if you have Go installed:
```bash
# Compile
go build -ldflags="-s -w" -o lfr-tunneld ./cmd/lfr-tunneld

# Install binary to system path
sudo cp lfr-tunneld /usr/local/bin/
sudo chmod 755 /usr/local/bin/lfr-tunneld
sudo chown root:root /usr/local/bin/lfr-tunneld
```

### 4.3. Configuration Files Setup
Create a configuration directory and secure it so only the daemon can read the sensitive shared secret token:
```bash
sudo mkdir -p /etc/lfr-tunneld
sudo nano /etc/lfr-tunneld/server-config.yaml
```

Paste the following configurations:
```yaml
domains:
  - "yourdomain.com"
http_bind_addr: "0.0.0.0:8080"
chisel_bind_addr: ":8081"
db_path: "/etc/lfr-tunneld/server.db"

# Owner and Admins
owner:
  user_id: "admin@yourdomain.com"
  name: "Gateway Admin"

admin_notification_email: "admin@yourdomain.com"
enable_registration: true
enable_user_portal: true

> [!TIP]
> **Native Multi-Factor Authentication (MFA / TOTP)**  
> If `enable_user_portal` is set to `true`, users can activate 6-digit Time-Based One-Time Password (TOTP) MFA from their **Account Settings** tab. This secures passwordless portal sessions using two independent factors: possession of email (magic link) + possession of device (authenticator app). Gateway administrators can reset or disable a user's MFA status directly from the Admin Dashboard in case of lost devices.

# Access Control
allowed_email_domains:
  - "liferay.com"

# SMTP Relay Configuration (Required for registration & magic links)
# Note: Use your domain here instead of 127.0.0.1 to securely pass TLS verification
# with your Let's Encrypt certificates.
smtp_server:
  host: "yourdomain.com"
  port: 25
  username: ""
  password: ""
  from_address: "Liferay Tunnel <noreply@yourdomain.com>"
```

Apply restricted file permissions:
```bash
sudo chown -R lfr-tunnel:lfr-tunnel /etc/lfr-tunneld
sudo chmod 700 /etc/lfr-tunneld
sudo chmod 600 /etc/lfr-tunneld/server-config.yaml
```

### 4.4. Local Postfix Email Relay (TLS Verification)
If you are running a local Postfix daemon to send emails, you must securely configure Postfix to present your Let's Encrypt certificates. If Postfix uses a self-signed certificate, the Go gateway will securely reject the `STARTTLS` handshake.

To bind the certificates to Postfix and allow relaying from the domain's resolved IP, run the following:
```bash
sudo postconf -e "smtpd_tls_cert_file=/etc/letsencrypt/live/yourdomain.com/fullchain.pem"
sudo postconf -e "smtpd_tls_key_file=/etc/letsencrypt/live/yourdomain.com/privkey.pem"
sudo postconf -e "mynetworks = 127.0.0.0/8 [::ffff:127.0.0.0]/104 [::1]/128 YOUR_VPS_PUBLIC_IPV4 YOUR_VPS_PUBLIC_IPV6"
sudo systemctl restart postfix
```

Make sure that your `smtp_server.host` in the `server-config.yaml` points to your public domain (e.g., `yourdomain.com`) rather than `127.0.0.1` so that the hostname securely matches the Common Name (CN) of the certificate! Also ensure your VPS's external IP addresses are added to Postfix's `mynetworks` as shown above.

### 4.5. systemd Service Setup
Create a systemd unit file at `/etc/systemd/system/lfr-tunneld.service`:
```bash
sudo nano /etc/systemd/system/lfr-tunneld.service
```

Paste the hardened service script:
```ini
[Unit]
Description=Liferay Tunnel Gateway Daemon
After=network.target

[Service]
Type=simple
User=lfr-tunnel
Group=lfr-tunnel
WorkingDirectory=/etc/lfr-tunneld
ExecStart=/usr/local/bin/lfr-tunneld --config /etc/lfr-tunneld/server-config.yaml
Restart=on-failure
RestartSec=5s

# Security Hardening (systemd Sandboxing)
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
CapabilityBoundingSet=
ReadOnlyPaths=/usr/local/bin/lfr-tunneld
ReadWritePaths=/etc/lfr-tunneld

[Install]
WantedBy=multi-user.target
```

Enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable lfr-tunneld
sudo systemctl start lfr-tunneld
```

Verify service is active and listening on localhost:
```bash
sudo systemctl status lfr-tunneld
sudo journalctl -u lfr-tunneld -n 50 -f
```

### 4.6. Service Self-Healing (Nginx & Watchdog)
To make your VPS gateway fully self-healing and immune to crashes, configuration dependency omissions, or system freezes, deploy passive and active watchdogs:

#### 1. Nginx Systemd Auto-Restart Override
By default, Nginx does not automatically recover on failure in standard OS packages. To enable Nginx self-healing:
1. Create the systemd override directory:
   ```bash
   sudo mkdir -p /etc/systemd/system/nginx.service.d/
   ```
2. Create `/etc/systemd/system/nginx.service.d/override.conf`:
   ```ini
   [Service]
   Restart=on-failure
   RestartSec=5s
   ```
3. Reload systemd and restart Nginx:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart nginx
   ```

#### 2. Active Watchdog Script & Timer
An active watchdog runs every minute to verify Nginx and `lfr-tunneld` ports, and automatically heals missing Let's Encrypt configuration files on-the-fly:
1. Create the watchdog script at `/usr/local/bin/gateway-watchdog.sh`:
   ```bash
   #!/usr/bin/env bash
   set -euo pipefail

   # 1. Self-Heal missing Certbot options or DH parameter files
   OPTIONS_FILE="/etc/letsencrypt/options-ssl-nginx.conf"
   DHPARAMS_FILE="/etc/letsencrypt/ssl-dhparams.pem"
   HEALED=0

   if [ ! -f "${OPTIONS_FILE}" ]; then
       sudo mkdir -p /etc/letsencrypt
       sudo tee "${OPTIONS_FILE}" > /dev/null << 'EOF'
   ssl_session_cache shared:le_nginx_SSL:10m;
   ssl_session_timeout 1440m;
   ssl_session_tickets off;
   ssl_protocols TLSv1.2 TLSv1.3;
   ssl_prefer_server_ciphers off;
   ssl_ciphers "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384";
   EOF
       HEALED=1
   fi

   if [ ! -f "${DHPARAMS_FILE}" ]; then
       sudo mkdir -p /etc/letsencrypt
       sudo openssl dhparam -out "${DHPARAMS_FILE}" 2048
       HEALED=1
   fi

   if [ "${HEALED}" -eq 1 ]; then
       if sudo nginx -t; then
           sudo systemctl restart nginx
       fi
   fi

   # 2. Check and heal lfr-tunneld Daemon
   if ! curl -sf --connect-timeout 5 "http://127.0.0.1:8080/api/version" > /dev/null; then
       sudo systemctl restart lfr-tunneld
   fi

   # 3. Check and heal Nginx Active State
   if ! curl -sfk --connect-timeout 5 "https://127.0.0.1" > /dev/null; then
       if ! systemctl is-active --quiet nginx; then
           sudo systemctl restart nginx
       fi
   fi
   ```
2. Make it executable:
   ```bash
   sudo chmod 700 /usr/local/bin/gateway-watchdog.sh
   sudo chown root:root /usr/local/bin/gateway-watchdog.sh
   ```
3. Create the watchdog systemd service `/etc/systemd/system/gateway-watchdog.service`:
   ```ini
   [Unit]
   Description=Gateway Active Watchdog
   After=network-online.target
   Wants=network-online.target

   [Service]
   Type=oneshot
   ExecStart=/usr/local/bin/gateway-watchdog.sh
   User=root
   Group=root
   ```
4. Create the watchdog systemd timer `/etc/systemd/system/gateway-watchdog.timer`:
   ```ini
   [Unit]
   Description=Trigger Gateway Active Watchdog every 1 minute

   [Timer]
   OnBootSec=1min
   OnUnitActiveSec=1min

   [Install]
   WantedBy=timers.target
   ```
5. Register, enable, and start the timer:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now gateway-watchdog.timer
   ```

---

## 5. Client CLI Setup & Connection

Now that your server is running, any user can connect to it using the `lfr-tunnel` CLI.

1.  **Configure Client Configuration File** (e.g. `~/.lfr-tunnel/config.yaml`):
    ```yaml
    server_url: "https://yourdomain.com"
    auth_token: "YOUR_SHARED_SECRET_TOKEN_KEY"
    subdomain: "my-dev-env"
    ports:
      - 8080
    ```
2.  **Start the Tunnel**:
    ```bash
    lfr-tunnel -config ~/.lfr-tunnel/config.yaml
    ```
3.  Your local Liferay instance running on port `8080` is now securely exposed to the web at `https://my-dev-env.yourdomain.com`!
