# rttys tenant-auth first release

Standalone fork of rttys upstream `bc1ac60478172aa984c551652a0b1e1ff2a782bc` (v5.6.0 family). Server version: `5.6.0-tenant.1`. Single process, SQLite identity store; no external account platform is required.

## Implemented boundary

- Built-in usernames and bcrypt passwords (12–72 bytes), platform admin and tenant memberships (`admin`, `operator`, `viewer`). No public signup. Bootstrap `admin` comes from a local password file only when the database is empty.
- Tenant administrators enroll devices and obtain an independent 256-bit random credential. Only its SHA-256 digest is persisted. The device wire ID is checked against the credential; tenant and internal opaque device ID are determined by the server, never by the client group.
- Viewer: tenant device metadata only. Operator: explicitly granted device or group. Tenant admin: all devices in that tenant. Platform admin explicitly switches into a tenant to operate its devices.
- All list/detail/count endpoints are scoped. Terminal and existing file-transfer WebSocket share the authorized device lease. Revoke, disable, credential rotation and tenant switches invalidate affected live channels.
- Password changes revoke existing login sessions; disable/member changes are checked again when opening a connection. Login throttles: 10 attempts/account+IP/5 minutes, aggregate 100/IP/minute. Successful login resets only that account+IP counter. Expensive password work is bounded and outside the policy mutex.
- Device Web proxy accepts only administrator-configured exact IPv4 scheme/host/port targets. It uses per-session wildcard DNS hostnames, a one-time 30-second claim, and a separate random bearer cookie stored by digest. Public hostnames are not credentials. Proxy sessions expire after 15 minutes, login sessions after 8 hours.
- API and device listeners require TLS. Proxy requires a separate HTTPS wildcard hostname. Dedicated proxy credentials and internal cookies are not forwarded to the device. Target cookies are restricted to the proxy session host. Origin checks reject cross-origin console WebSockets and proxy requests.
- SQLite exclusive locking rejects a second server using the same database. Accounts, memberships, credentials and grants survive process restart. Live devices reconnect; live channels are not resumed.
- Basic management/access audit records are persisted. This is not terminal recording or compliance-certified audit storage.

Commands, serial access, SSH/TCP sharing are explicitly denied in secure mode. A shell grants the privileges of its OS user: disabling dedicated file-transfer controls would not prevent shell-based file access. Revocation cannot retract data already delivered or undo commands/background processes already started.

## Build and initialize

```sh
cd ui && npm ci && npm run build && cd ..
go build -o rttys ./cmd/rttys
umask 077
openssl rand -base64 24 > bootstrap-password
./rttys --tenant-db ./identity.db --bootstrap-password-file ./bootstrap-password \
  --addr-user 127.0.0.1:18446 --addr-dev 127.0.0.1:19512 \
  --sslcert ./cert.pem --sslkey ./key.pem
```

Use a certificate appropriate for the deployment. The test deployment uses a private self-signed CA/certificate; the rtty client and replay harness verify that CA. Browser trust is a separate user-controlled step. Legacy password, local-auth bypass and Hook settings do not grant access in the CLI's mandatory tenant mode.

Create a tenant in the management page, select it, create accounts/members, enroll a device, and grant an operator the device or its group. Token material is shown only at creation/rotation; rotation disconnects the previous device connection. Device ownership transfer is not supported in this release; disable the old enrollment and re-enroll explicitly.

The test device uses unmodified `rtty-go v1.2.2` with a root-readable config file to keep credentials out of argv. The daemon runs as root to start OS login; its configured terminal user is `nvidia`. `/usr/local/bin/rtty-tenant-poc -R` receives a file in the terminal's current directory; `-S path` sends a file. The helper is executable by the terminal user; the credential directory is not.

## Authorized test environment

- Server: `172.21.30.204`, service `rttys-tenant-poc`, root `/opt/rttys-tenant-poc`.
- Console: `https://172.21.30.204:18446/`; device TLS: `19512`.
- Web proxy: `https://<session>.172.21.30.204.sslip.io:18447/`. Test-only DNS dependency; production must use an organization-controlled wildcard hostname and dedicated console hostname.
- Device: `172.21.0.156`, service `rtty-tenant-poc`, root `/opt/rtty-tenant-poc`.
- Deployment/provisioning scripts under `scripts/` are intentionally once-only and reject an existing target.
- Passwords, device token, TLS key and database remain outside Git. Test login handoff lives in `/opt/rttys-tenant-poc/access.txt`.

## Reproducible evidence

```sh
go build -o /tmp/rttys-tenant ./cmd/rttys
go build -o /tmp/rttys-socket-check ./cmd/tenant-e2e
python3 scripts/run-local-e2e.py
```

The runner creates a fresh database, uses distinct ephemeral loopback ports, and writes a JSON result plus server/test logs even on failures. The protocol fixture is synthetic; the server and TLS/HTTP/WebSocket paths are real. It includes wrong/absent credentials, wire-ID binding, tenant isolation, file data, live revocation, public-proxy-host cookie forgery, account throttling, exclusive database ownership and restart persistence.

Real device acceptance:

```sh
go build -o /tmp/rttys-live-check ./cmd/tenant-live-check
RTTY_ACCESS=/secure/test-access.json RTTY_CA=/secure/cert.pem \
  RTTY_LIVE_EVIDENCE=/tmp/live-results.json /tmp/rttys-live-check
```

This uses the private provisioning output and the enrolled NVIDIA device. It opens a real terminal, uploads and downloads identical bytes, reads an explicit temporary device HTTP proof service, revokes operator access and checks existing/new channels, then restores the grant. It does not test the browser UI. The temporary HTTP proof fixture is `127.0.0.1:18083/proof.txt`, content `RTTY_WEB_REAL_DEVICE`; start it only for testing. The independent real service target `127.0.0.1:8080` is also allowlisted.

Existing focused upstream tests were retained. `TestRttysStress` is an external-client/manual long-running harness; an initial blanket run was cancelled and is not reported as passing. No new unit-test layer was added.

## Rollback and limitations

Stop only `rttys-tenant-poc` on 204 and `rtty-tenant-poc` on 156. Existing rtty, MeshCentral and ShellHub are not replaced. Back up SQLite only with this single instance stopped; keep file ownership/permissions. For upgrade rollback restore the saved binary and, if schema changed, the stopped-instance database backup.

This is a test candidate, not a production rollout. Not covered: HA, external identity providers, approval workflows, arbitrary OS-command policies, terminal recording, bulk fleet migration, large-file/weak-network endurance, tenant ownership transfer, and device credential cloning with a valid stolen secret. A copied valid bearer secret is not hardware attestation. Certificate issuance, backup retention and Internet-facing DoS controls require deployment-specific decisions.

Independent security review identified and drove fixes for public proxy-host credential inference and cross-account throttle resetting. The failing and passing replay evidence are retained in the delivery record. Authentication, proxy and transport failures are reported separately from browser/provider limitations.
