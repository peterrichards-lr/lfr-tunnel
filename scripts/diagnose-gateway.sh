#!/usr/bin/env bash
#
# Liferay Tunnel Gateway Diagnostics Tool
# Performs network, DNS, port, and application level health checks against the VPS gateway.
#

set -euo pipefail

# Text formatting helper constants
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Domains to check
DOMAINS=("lfr-demo.se" "tunnel.lfr-demo.se" "lfr-demo.online" "tunnel.lfr-demo.online")
DEFAULT_IPV4="82.39.133.178"
DEFAULT_IPV6="2a13:9500:10e:2a::a"

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]  ${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1"
}

echo -e "${BOLD}========================================================"
echo -e "       Liferay Tunnel Gateway Diagnostics Tool"
echo -e "========================================================${NC}\n"

# Check dependencies
log_info "Checking required dependencies..."
for cmd in dig ping curl nc; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log_error "Required tool '$cmd' is not installed."
        exit 1
    fi
done
log_success "All dependencies satisfied.\n"

# Step 1: DNS Diagnostics
echo -e "${BOLD}--- [Step 1: DNS Resolution Verification] ---${NC}"
DNS_ERRORS=0
MISMATCHES=0

for domain in "${DOMAINS[@]}"; do
    echo -e "\nChecking domain: ${BOLD}${domain}${NC}"
    
    # 1. Local DNS Resolution
    local_ipv4=$(dig +short "$domain" A | tail -n1)
    local_ipv6=$(dig +short "$domain" AAAA | tail -n1)
    
    if [ -z "$local_ipv4" ]; then
        log_error "Local DNS: Failed to resolve A record (IPv4)"
        DNS_ERRORS=$((DNS_ERRORS + 1))
    else
        log_success "Local DNS: IPv4 -> ${local_ipv4}"
    fi
    
    if [ -z "$local_ipv6" ]; then
        log_warn "Local DNS: No AAAA record resolved (IPv6)"
    else
        log_success "Local DNS: IPv6 -> ${local_ipv6}"
    fi

    # 2. Public DNS - Cloudflare (1.1.1.1)
    cf_ipv4=$(dig @1.1.1.1 +short "$domain" A | tail -n1)
    if [ -n "$cf_ipv4" ] && [ "$cf_ipv4" != "$local_ipv4" ]; then
        log_warn "DNS Mismatch: Local DNS ($local_ipv4) differs from Cloudflare 1.1.1.1 ($cf_ipv4)"
        MISMATCHES=$((MISMATCHES + 1))
    fi

    # 3. Public DNS - Google (8.8.8.8)
    google_ipv4=$(dig @8.8.8.8 +short "$domain" A | tail -n1)
    if [ -n "$google_ipv4" ] && [ "$google_ipv4" != "$local_ipv4" ]; then
        log_warn "DNS Mismatch: Local DNS ($local_ipv4) differs from Google 8.8.8.8 ($google_ipv4)"
        MISMATCHES=$((MISMATCHES + 1))
    fi
done

echo ""
if [ "$DNS_ERRORS" -gt 0 ]; then
    log_error "DNS Diagnostics completed with errors."
elif [ "$MISMATCHES" -gt 0 ]; then
    log_warn "DNS Diagnostics completed: Stale local DNS cache detected."
else
    log_success "DNS Diagnostics completed successfully: DNS is correctly configured."
fi
echo ""

# Step 2: Network-level reachability
echo -e "${BOLD}--- [Step 2: Network-level Reachability (Ping)] ---${NC}"
PING_V4_OK=0
PING_V6_OK=0

log_info "Pinging target IPv4: ${DEFAULT_IPV4}..."
if ping -c 3 -t 3 "$DEFAULT_IPV4" >/dev/null 2>&1; then
    log_success "Ping IPv4: Host is reachable."
    PING_V4_OK=1
else
    # Try alternative syntax in case of Linux ping (which uses -W instead of -t)
    if ping -c 3 -W 3 "$DEFAULT_IPV4" >/dev/null 2>&1; then
        log_success "Ping IPv4: Host is reachable."
        PING_V4_OK=1
    else
        log_error "Ping IPv4: Host is unreachable (100% packet loss)."
    fi
fi

log_info "Pinging target IPv6: ${DEFAULT_IPV6}..."
# Detect ping6 command or ping -6
PING6_CMD=""
if command -v ping6 >/dev/null 2>&1; then
    PING6_CMD="ping6"
elif ping -6 -c 1 localhost >/dev/null 2>&1; then
    PING6_CMD="ping -6"
fi

if [ -n "$PING6_CMD" ]; then
    if $PING6_CMD -c 3 -W 3 "$DEFAULT_IPV6" >/dev/null 2>&1; then
        log_success "Ping IPv6: Host is reachable."
        PING_V6_OK=1
    else
        log_warn "Ping IPv6: Host is unreachable. (Could be due to local IPv6 routing unavailability)"
    fi
else
    log_warn "Ping IPv6: Tool not available on this platform."
fi
echo ""

# Step 3: Protocol Ports Verification (TCP)
echo -e "${BOLD}--- [Step 3: Protocol Ports Verification (TCP)] ---${NC}"
PORT_22_OK=0
PORT_80_OK=0
PORT_443_OK=0

# Detect connection timeout flag based on OS (macOS uses -G, Linux uses -w)
TIMEOUT_FLAG="-w"
if [ "$(uname -s)" = "Darwin" ]; then
    TIMEOUT_FLAG="-G"
fi

ports=(22 80 443)
for port in "${ports[@]}"; do
    log_info "Probing TCP Port ${port}..."
    if nc -z "$TIMEOUT_FLAG" 5 "$DEFAULT_IPV4" "$port" >/dev/null 2>&1; then
        log_success "Port ${port}: OPEN"
        if [ "$port" -eq 22 ]; then PORT_22_OK=1; fi
        if [ "$port" -eq 80 ]; then PORT_80_OK=1; fi
        if [ "$port" -eq 443 ]; then PORT_443_OK=1; fi
    else
        log_error "Port ${port}: UNRESPONSIVE (Blocked / Timed Out)"
    fi
done
echo ""

# Step 4: Application Layer Health Check
echo -e "${BOLD}--- [Step 4: Application Layer Health Check] ---${NC}"
HTTP_STATUS=0
CURL_ERROR=""

log_info "Fetching Version API endpoint (https://tunnel.lfr-demo.se/api/version)..."
set +e
http_response=$(curl -sSf -w "%{http_code}" --connect-timeout 5 -o /tmp/diag_resp.json https://tunnel.lfr-demo.se/api/version 2>/tmp/diag_err.log)
exit_code=$?
set -e

if [ "$exit_code" -eq 0 ]; then
    HTTP_STATUS=$(echo "$http_response" | tail -n1)
    if [ "$HTTP_STATUS" -eq 200 ]; then
        version_data=$(cat /tmp/diag_resp.json)
        log_success "HTTP Status: 200 OK"
        log_success "Gateway Version Endpoint Response: ${version_data}"
    else
        log_warn "HTTP Status: ${HTTP_STATUS}"
    fi
else
    CURL_ERROR=$(cat /tmp/diag_err.log)
    log_error "Curl request failed (Exit code: ${exit_code}): ${CURL_ERROR}"
fi
echo ""

# Step 5: WHOIS Lookup
echo -e "${BOLD}--- [Step 5: IP Ownership Verification] ---${NC}"
log_info "Checking IP WHOIS details for ${DEFAULT_IPV4}..."
if command -v whois >/dev/null 2>&1; then
    set +e
    whois_info=$(whois "$DEFAULT_IPV4" 2>/dev/null | grep -Ei "org-name|netname|descr|owner|isp|asn" | head -n 10)
    set -e
    if [ -n "$whois_info" ]; then
        echo -e "${whois_info}"
    else
        log_warn "WHOIS lookup completed but returned no matched fields."
    fi
else
    log_warn "WHOIS command not found. Skipping ownership check."
fi
echo ""

# Step 6: Hosting Provider Status check (VM6 Networks)
echo -e "${BOLD}--- [Step 6: Hosting Provider Status Verification] ---${NC}"
VM6_STATUS_URL="https://status.vm6.co.uk/api/status.php?action=status"
VM6_OUTAGE_DETECTED=0
VM6_OUTAGE_MSG=""

log_info "Querying VM6 Networks service status page..."
set +e
vm6_json=$(curl -sSf --connect-timeout 5 "$VM6_STATUS_URL" 2>/dev/null)
vm6_exit=$?
set -e

if [ "$vm6_exit" -eq 0 ] && [ -n "$vm6_json" ]; then
    if command -v jq >/dev/null 2>&1; then
        overall_status=$(echo "$vm6_json" | jq -r '.overall_status // empty')
        overall_msg=$(echo "$vm6_json" | jq -r '.overall_message // empty')
    elif command -v python3 >/dev/null 2>&1; then
        overall_status=$(echo "$vm6_json" | python3 -c "import sys, json; print(json.load(sys.stdin).get('overall_status', ''))")
        overall_msg=$(echo "$vm6_json" | python3 -c "import sys, json; print(json.load(sys.stdin).get('overall_message', ''))")
    else
        overall_status=$(echo "$vm6_json" | grep -o '"overall_status":"[^"]*' | cut -d'"' -f4 || echo "")
        overall_msg=$(echo "$vm6_json" | grep -o '"overall_message":"[^"]*' | cut -d'"' -f4 || echo "")
    fi

    if [ -n "$overall_status" ]; then
        if [ "$overall_status" = "operational" ]; then
            log_success "VM6 Networks Status: OPERATIONAL - ${overall_msg:-All systems normal}"
        else
            # uppercase overall_status for printing
            upper_status=$(echo "$overall_status" | tr '[:lower:]' '[:upper:]')
            log_warn "VM6 Networks Status: ${upper_status} - ${overall_msg}"
            VM6_OUTAGE_DETECTED=1
            VM6_OUTAGE_MSG="${overall_msg}"
            
            # Print details of degraded or offline nodes
            log_info "Identifying affected provider nodes:"
            if command -v jq >/dev/null 2>&1; then
                echo "$vm6_json" | jq -r '.nodes[] | select(.status != "online") | "  - \(.name) (\(.location)): \(.status | ascii_upcase) (\(.packet_loss)% packet loss)"' || true
            elif command -v python3 >/dev/null 2>&1; then
                echo "$vm6_json" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for node in data.get('nodes', []):
    if node.get('status') != 'online':
        print(f\"  - {node.get('name')} ({node.get('location')}): {node.get('status').upper()} ({node.get('packet_loss')}% packet loss)\")
" || true
            else
                log_info "Detailed node list requires 'jq' or 'python3'."
            fi
        fi
    else
        log_warn "Could not parse status from VM6 status page response."
    fi
else
    log_warn "Failed to reach VM6 Networks status page (timed out or DNS error)."
fi
echo ""

# Diagnostics Summary & Recommendations
echo -e "${BOLD}========================================================"
echo -e "                 DIAGNOSTIC REPORT SUMMARY"
echo -e "========================================================${NC}"

# Case 1: Perfect Health
if [ "$PORT_443_OK" -eq 1 ] && [ "$HTTP_STATUS" -eq 200 ]; then
    echo -e "${GREEN}${BOLD}STATUS: FULLY OPERATIONAL${NC}"
    echo -e "The Liferay Tunnel gateway is online, DNS resolves correctly, and the server daemon is responding."

# Case 2: DNS Failures
elif [ "$DNS_ERRORS" -gt 0 ]; then
    echo -e "${RED}${BOLD}STATUS: DNS FAILURE${NC}"
    echo -e "DNS records are missing or cannot be resolved locally. Check your internet connection or DNS resolver settings."

# Case 3: Completely offline (Ping / SSH / Ports all down)
elif [ "$PING_V4_OK" -eq 0 ] && [ "$PORT_22_OK" -eq 0 ] && [ "$PORT_443_OK" -eq 0 ]; then
    if [ "$VM6_OUTAGE_DETECTED" -eq 1 ]; then
        echo -e "${RED}${BOLD}STATUS: SERVER OFFLINE (PROVIDER OUTAGE DETECTED)${NC}"
        echo -e "The IP address ${DEFAULT_IPV4} is completely unreachable."
        echo -e "${YELLOW}VM6 Networks status page confirms an active outage: ${VM6_OUTAGE_MSG}${NC}"
        echo -e "The server's unreachability is highly likely caused by this provider network outage."
    else
        echo -e "${RED}${BOLD}STATUS: SERVER OFFLINE / HOST UNREACHABLE${NC}"
        echo -e "The IP address ${DEFAULT_IPV4} did not respond to ping, SSH, or web requests."
        echo -e "Is the gateway VPS powered down or suspended? Verify the power/network state in your hosting console."
    fi

# Case 4: Nginx online, daemon offline
elif [ "$PORT_443_OK" -eq 1 ] && [ "$HTTP_STATUS" -ne 200 ]; then
    echo -e "${YELLOW}${BOLD}STATUS: DAEMON OFFLINE / BAD GATEWAY${NC}"
    echo -e "Nginx is running and responding on port 443, but the tunnel daemon (port 8080) is not responding."
    echo -e "SSH into the VPS and check the systemd service:"
    echo -e "  ${BOLD}sudo systemctl status lfr-tunneld${NC}"

# Case 5: Ping works, ports blocked (Firewall)
elif [ "$PING_V4_OK" -eq 1 ] && [ "$PORT_22_OK" -eq 0 ] && [ "$PORT_443_OK" -eq 0 ]; then
    echo -e "${YELLOW}${BOLD}STATUS: FIREWALL BLOCKED / PORT FILTERS ON${NC}"
    echo -e "The server is responding to network pings, but all ports (22, 80, 443) are completely unresponsive."
    echo -e "Ensure your VPS firewall (UFW/security groups) allows incoming traffic on ports 22 and 443."

# Fallback: Ambiguous status
else
    echo -e "${YELLOW}${BOLD}STATUS: DEGRADED / UNKNOWN STATE${NC}"
    echo -e "Some checks passed while others failed. Review the detailed log above to isolate the issue."
fi

echo -e "========================================================\n"
