# rttys tenant-auth first release

Standalone fork of rttys upstream `bc1ac60478172aa984c551652a0b1e1ff2a782bc` (v5.6.0 family). Server version: `5.6.0-tenant.2`. Single process, SQLite identity store; no external account platform is required.

## Implemented boundary

- Built-in usernames and bcrypt passwords (12–72 bytes), platform admin and tenant memberships (`admin`, `operator`, `viewer`). No public signup. Bootstrap `admin` comes from a local password file only when the database is empty.
- Tenant administrators enroll devices and obtain an independent 256-bit random credential. Only its SHA-256 digest is persisted. The device wire ID is checked against the credential; tenant and internal opaque device ID are determined by the server, never by the client group.
- Viewer: tenant device metadata only. Operator: explicitly granted device or group. Tenant admin: all devices in that tenant. Platform admin explicitly switches into a tenant to operate its devices.
- All list/detail/count endpoints are scoped. Terminal and existing file-transfer WebSocket share the authorized device lease. Revoke, disable, credential rotation and tenant switches invalidate affected live channels.
- Password changes revoke existing login sessions; disable/member changes are checked again when opening a connection. Login throttles: 10 attempts/account+IP/minute, aggregate 100/IP/minute, with Retry-After. Successful login resets only that account+IP counter. Missing/disabled users perform the same bcrypt cost as existing users. Expensive password work is bounded and outside the policy mutex.
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
./rttys --tenant-db "$PWD/identity.db" --bootstrap-password-file ./bootstrap-password \
  --addr-user 127.0.0.1:18446 --addr-dev 127.0.0.1:19512 \
  --sslcert ./cert.pem --sslkey ./key.pem
```

Use a certificate appropriate for the deployment. The test deployment uses a private self-signed CA/certificate; the rtty client and replay harness verify that CA. Browser trust is a separate user-controlled step. Legacy password, local-auth bypass and Hook settings do not grant access in the CLI's mandatory tenant mode.

The identity database path is required and must be absolute, in both CLI and YAML configuration. Reuse the existing path on upgrades. The bootstrap file must be a regular, non-symlink file owned by the service user, with mode 0600 or stricter; initialize file-based bootstrap on Linux or macOS. After initialization, change the administrator password and remove the bootstrap file. An existing populated database does not need the bootstrap file on subsequent starts. The packaged systemd configuration uses `/var/lib/rttys/identity.db` and TLS/bootstrap files under `/etc/rttys`; provision these before starting. The container uses `/data/identity.db`; persist `/data` and mount the TLS/bootstrap files under `/etc/rttys`.

Diagnostics are off by default. To enable them, set `--pprof 127.0.0.1:6060 --pprof-token-file /secure/pprof-token`; create a random 32–256 byte token in a service-owned regular file with mode 0600. Every diagnostics request requires `Authorization: Bearer <token>`. Public/wildcard bind addresses and missing or insecure token files are rejected. Use an SSH tunnel for remote collection.

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

Existing focused upstream tests were retained. `TestRttysStress` is an external-client/manual long-running harness and is excluded from unattended regression runs. Security regressions are covered by the `TestSecurity*` tests and the panic-isolation subprocess test. Run `go test ./... -skip TestRttysStress -count=1` and `go vet ./...` after building the UI.

## Rollback and limitations

Stop only `rttys-tenant-poc` on 204 and `rtty-tenant-poc` on 156. Existing rtty, MeshCentral and ShellHub are not replaced. Back up SQLite only with this single instance stopped; keep file ownership/permissions. For upgrade rollback restore the saved binary and, if schema changed, the stopped-instance database backup.

This is a test candidate, not a production rollout. Not covered: HA, external identity providers, approval workflows, arbitrary OS-command policies, terminal recording, bulk fleet migration, large-file/weak-network endurance, tenant ownership transfer, and device credential cloning with a valid stolen secret. A copied valid bearer secret is not hardware attestation. Certificate issuance, backup retention and Internet-facing DoS controls require deployment-specific decisions.

Independent security review identified and drove fixes for public proxy-host credential inference and cross-account throttle resetting. The failing and passing replay evidence are retained in the delivery record. Authentication, proxy and transport failures are reported separately from browser/provider limitations.

## Multi-group management (tenant.2)

Login as tenant-admin, open 管理 → 分组管理 to create, rename or delete tenant-local groups. Under 登记设备, use 设置分组 on an existing device and select multiple groups. New enrollment also offers multiple selection. Operator grants select either one device or a group. Device group lists/search display every membership.

Authorization is a union: any direct device grant or grant on any current device group permits operation. Removing one source preserves existing terminal/file/proxy channels if another source still permits access. Removing the last source closes existing channels and denies new access. Rename preserves memberships and grants. Delete removes memberships and grants, never devices; recreating a name does not restore old grants. Viewers remain read-only; groups cannot bridge tenants.

API: POST /api/manage/groups {name}; PATCH /api/manage/groups/:name {name}; DELETE /api/manage/groups/:name; PUT /api/manage/devices/:id/groups {groups:[name,...]}. Names in paths must be URL encoded. Replacement is atomic, [] clears groups, missing/null groups is rejected. Snapshot contains groups and device_groups; /api/devs returns groups plus a compatibility first-group field. The legacy enrollment group parameter remains supported but no longer owns policy. SQLite schema v2 migrates both existing device groups and preexisting group grants once, transactionally.

Before upgrading stop the single server and back up its binary and identity.db together. Migration clears the legacy group column; rollback MUST restore both stopped-instance database and matching old binary. Never run an old binary against the migrated database. Existing clients and device credentials do not change.

Replay: build cmd/tenant-e2e as /tmp/rttys-groups-check and the new server as /tmp/rttys-groups; run RTTY_BINARY=/tmp/rttys-groups RTTY_SOCKET_BINARY=/tmp/rttys-groups-check python3 scripts/run-local-e2e.py. Set RTTY_LEGACY_BINARY to the tenant.1 binary to verify real database migration. Set RTTY_GROUPS=1 when running cmd/tenant-live-check to test two group grants on the real device; it restores the original memberships/direct test grant and deletes temporary groups afterwards.

## Security scan remediation (2026-10-24)

The white-box archive supplied on 2026-10-24 contains eight findings against the tenant.2 snapshot. The separate black-box list contains two login findings; their VULN numbers are independent of the white-box numbers. Changes below are local source fixes, not confirmation that the scanned server has been upgraded.

| Source / ID | Finding and disposition | Regression evidence |
| --- | --- | --- |
| White-box VULN-01 | Registration heartbeat requires one nonzero byte. Connection panic recovery logs and returns instead of exiting the process. | Empty-TLV registration, connection isolation, panic subprocess |
| White-box VULN-02 | Login/logout/terminal/file/HTTP/serial frames validate lengths before slicing. Uptime requires four bytes; command attributes have an explicit non-null-object guard. The report's null-attribute panic was not reproduced with the pinned JSON dependency, which already rejects that input. | Every short length, short uptime, missing/null command attributes |
| White-box VULN-03 | pprof accepts only explicit loopback IPs and requires a separate private token file. A dedicated HTTP handler checks Bearer authentication before serving any profile. | Public addresses and missing tokens rejected; absent/wrong/valid credentials return 401/401/200 |
| White-box VULN-04 | Login counters use fixed-length hashed keys and a 20,000-entry cap. At capacity, expired/old entries are removed rather than rejecting unrelated users globally. | A valid login succeeds with 20,001 unrelated live entries; the table returns below the cap |
| White-box VULN-05 | File messages use JSON encoding; download names are reduced to a basename, stripped of controls and bounded. The UI handles malformed/unknown control messages and ignores repeated login initialization. | Real WebSocket assertions for duplicate-key injection, quotes, control characters and paths |
| White-box VULN-06 | Removed the relative database default; an explicit absolute path is required. YAML now loads identity/bootstrap fields, CLI overrides remain supported, and packaged systemd/container paths are explicit. | Relative paths rejected before file creation; YAML and override tests; local E2E startup/restart |
| White-box VULN-07 | Private secret files require service ownership and mode 0600 or stricter; symlinks, non-regular files, oversized files and changed files are rejected before use. Opening on POSIX uses no-follow/nonblocking flags. | Insecure modes, symlink/directory/oversize rejection and valid-file acceptance |
| White-box VULN-08 | Device Web windows use `noopener,noreferrer`; console responses also set `Cross-Origin-Opener-Policy: same-origin`. | Source verification and production UI build; real browser/device behavior remains a deployment recheck |
| Black-box VULN-02 | Unknown/disabled usernames now perform a cost-12 dummy bcrypt comparison before the same 401 response. | Existing/unknown-user timing regression and real HTTPS login E2E |
| Black-box VULN-04 | The current baseline already scopes counters to IP + username, unlike the report's global-account interpretation. The window is shortened from five minutes to one and 429 responses include Retry-After. Login from another source remains available. | Source-isolation test, bounded-window test and existing cross-account reset E2E |

Additional report follow-ups: SQLite PRAGMAs are now in the driver DSN so they apply after reconnect; a forced-reconnect test checks foreign keys, exclusive mode, busy timeout and cascading deletion. Console cookies use `__Host-rt_sid`, with no legacy-cookie authentication fallback; both old/new console cookies are stripped from proxy traffic. Users must sign in again after upgrading. Tenant administrators retain the documented ability to configure targets on their own devices (N3). Device/WebSocket connection quotas and front-gateway capacity (N4) remain deployment checks; these changes do not impose an unmeasured fleet/NAT quota.

Dependency checks additionally found five reachable HTTP/2 advisories in `golang.org/x/net v0.59.0`; the dependency is upgraded to v0.60.0. The [Go vulnerability database](https://pkg.go.dev/vuln/GO-2026-6617) documents the affected/fixed versions. The toolchain and Docker builder use Go 1.27.3. Compatible npm lockfile updates fix the build-tool findings without changing direct dependency major versions. `govulncheck` reports zero reachable/imported-package vulnerabilities; its module-only warning for unused `x/crypto/openpgp` remains distinguishable from a reachable finding.

Local verification uses `go test ./... -skip TestRttysStress -count=1`, `go vet ./...`, `npm run build`, `npm audit` and `govulncheck`. The disposable E2E runs real HTTPS, device TLS and WebSockets with synthetic identities; it checks authorization/revocation, terminal/file/proxy traffic, rejection of a second instance and restart persistence. No requests were sent to the reported production/test hosts during this remediation. Upgrade the existing instance using the same absolute database path, then repeat its black-box scan and real-device/browser acceptance.

## Console redesign

The console now uses four task-focused areas: Devices (status/search/pagination, terminal/Web, per-row management menu), Groups (group list with device membership detail), Members and Permissions (select a member to inspect or edit grants), and Activity (searchable paginated recent records). Platform admins additionally see Tenants and Accounts. Enrollment, account creation and authorization use labeled drawers. Device group membership remains a visible checkbox dialog. Tenant API and backend authorization are unchanged.
