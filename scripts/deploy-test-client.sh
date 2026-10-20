#!/bin/sh
set -eu
base=/opt/rtty-tenant-poc
test ! -e "$base"
install -d -m 0700 "$base"
install -m 0755 /tmp/rtty-tenant-linux-arm64 "$base/rtty"
test ! -e /usr/local/bin/rtty-tenant-poc
install -m 0755 /tmp/rtty-tenant-linux-arm64 /usr/local/bin/rtty-tenant-poc
install -m 0600 /tmp/rtty-tenant-client.conf "$base/rtty.conf"
install -m 0644 /tmp/rttys-tenant-cert.pem "$base/cert.pem"
cat > /etc/systemd/system/rtty-tenant-poc.service <<'UNIT'
[Unit]
Description=RTTY tenant authentication test client
After=network-online.target
Wants=network-online.target
[Service]
WorkingDirectory=/opt/rtty-tenant-poc
ExecStart=/opt/rtty-tenant-poc/rtty --conf /opt/rtty-tenant-poc/rtty.conf
Restart=on-failure
RestartSec=5
UMask=0077
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now rtty-tenant-poc.service
rm -f /tmp/rtty-tenant-client.conf
systemctl is-active rtty-tenant-poc.service
