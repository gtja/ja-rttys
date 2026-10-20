#!/bin/sh
# Run as root on the explicitly authorized 204 test server after uploading binary.
set -eu
base=/opt/rttys-tenant-poc
test ! -e "$base" || { echo 'Target exists; use reviewed upgrade procedure'; exit 1; }
install -d -m 0750 "$base" "$base/data" "$base/evidence"
install -m 0755 /tmp/rttys-tenant-linux-amd64 "$base/rttys"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$base/key.pem" -out "$base/cert.pem" -days 30 -subj '/CN=172.21.30.204' -addext 'subjectAltName=IP:172.21.30.204,DNS:*.172.21.30.204.sslip.io' >/dev/null 2>&1
python3 -c 'import secrets,pathlib; p=pathlib.Path("/opt/rttys-tenant-poc/bootstrap-password");p.write_text(secrets.token_urlsafe(24));p.chmod(0o600)'
id rttys-tenant >/dev/null 2>&1 || useradd --system --no-create-home --shell /sbin/nologin rttys-tenant
chown -R rttys-tenant:rttys-tenant "$base"
chmod 0600 "$base/key.pem" "$base/bootstrap-password"
cat > /etc/systemd/system/rttys-tenant-poc.service <<'UNIT'
[Unit]
Description=RTTYS tenant authentication test instance
After=network-online.target
Wants=network-online.target
[Service]
User=rttys-tenant
Group=rttys-tenant
WorkingDirectory=/opt/rttys-tenant-poc
ExecStart=/opt/rttys-tenant-poc/rttys --tenant-db /opt/rttys-tenant-poc/data/identity.db --bootstrap-password-file /opt/rttys-tenant-poc/bootstrap-password --addr-user 172.21.30.204:18446 --addr-dev 172.21.30.204:19512 --addr-http-proxy 172.21.30.204:18447 --http-proxy-redir-url https://172.21.30.204.sslip.io:18447 --sslcert /opt/rttys-tenant-poc/cert.pem --sslkey /opt/rttys-tenant-poc/key.pem
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
UMask=0077
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now rttys-tenant-poc.service
systemctl is-active rttys-tenant-poc.service
