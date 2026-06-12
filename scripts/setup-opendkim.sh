#!/bin/bash
set -e

echo "Installing OpenDKIM..."
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y opendkim opendkim-tools

echo "Configuring OpenDKIM..."
sudo bash -c 'cat > /etc/opendkim.conf <<EOF
AutoRestart             Yes
AutoRestartRate         10/1h
UMask                   002
Syslog                  yes
SyslogSuccess           Yes
LogWhy                  Yes

Canonicalization        relaxed/simple

ExternalIgnoreList      refile:/etc/opendkim/TrustedHosts
InternalHosts           refile:/etc/opendkim/TrustedHosts
KeyTable                refile:/etc/opendkim/KeyTable
SigningTable            refile:/etc/opendkim/SigningTable

Mode                    sv
PidFile                 /var/run/opendkim/opendkim.pid
SignatureAlgorithm      rsa-sha256

UserID                  opendkim:opendkim
Socket                  inet:12301@localhost
EOF'

sudo bash -c 'echo "SOCKET=\"inet:12301@localhost\"" > /etc/default/opendkim'

echo "Setting up directories and files..."
sudo mkdir -p /etc/opendkim/keys/lfr-demo.se

sudo bash -c 'cat > /etc/opendkim/TrustedHosts <<EOF
127.0.0.1
localhost
82.39.133.178
*.lfr-demo.se
EOF'

sudo bash -c 'cat > /etc/opendkim/KeyTable <<EOF
mail._domainkey.lfr-demo.se lfr-demo.se:mail:/etc/opendkim/keys/lfr-demo.se/mail.private
EOF'

sudo bash -c 'cat > /etc/opendkim/SigningTable <<EOF
*@lfr-demo.se mail._domainkey.lfr-demo.se
EOF'

echo "Generating keys..."
sudo opendkim-genkey -s mail -d lfr-demo.se -D /etc/opendkim/keys/lfr-demo.se
sudo chown -R opendkim:opendkim /etc/opendkim
sudo chmod go-rw /etc/opendkim/keys

echo "Configuring Postfix..."
sudo postconf -e "milter_protocol = 6"
sudo postconf -e "milter_default_action = accept"
sudo postconf -e "smtpd_milters = inet:localhost:12301"
sudo postconf -e "non_smtpd_milters = inet:localhost:12301"

echo "Restarting services..."
sudo systemctl restart opendkim
sudo systemctl restart postfix

echo "=== DKIM PUBLIC KEY FOR DNS ==="
sudo cat /etc/opendkim/keys/lfr-demo.se/mail.txt
echo "==============================="
