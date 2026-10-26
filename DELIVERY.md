# Standalone tenant-auth delivery contract

User-authorized scope: built-in users/tenants; pre-enrolled per-device credentials; tenant and device/group grants; terminal/file/web proxy authorization and revocation. Deploy isolated test instance on 172.21.30.204, client 172.21.0.156. Preserve other services and edge-manager changes.

Failure modes (before implementation): anonymous/loopback bypass; fabricated tenant/group/ID; cross-tenant lists/counts/details; valid credential used for another wire ID; wrong/disabled/revoked credential; duplicate wire IDs in different tenants; viewer or ungranted operator opening terminal/file/proxy; foreign WebSocket Origin; revoke racing with new session creation; old terminal/file/proxy surviving revoke; unrestricted proxy target; account/tenant escalation; persistence loss on restart.

Acceptance oracle: explicit negative HTTP statuses, tenant-scoped responses, rejected device registration, real WebSocket closure and absence of subsequent forwarded data, plus authorized device terminal/file round trips. Tests exercise actual running server HTTP/TLS/device sockets; existing upstream tests remain intact. No post-implementation unit tests.

First release is single instance, SQLite; users have tenant memberships; grants target a device or group. Tenant admins cannot grant platform admin. Commands, serial and shares disabled in secure mode. Full terminal access permits OS-level file access regardless of dedicated file-channel switches. Closing sessions cannot undo previously executed/background commands.

Boundaries: API/device TLS required; proxy uses a separate configured origin and short-lived server-side sessions. Test credentials remain outside source control. Runtime revocation is fail-closed; no anonymous fallback. Bootstrap admin from a local password file only.

Rollback: stop only rttys-tenant-poc server/client services, retain data and binary backups. No production cutover. Evidence distinguishes simulated transport fixture from actual NVIDIA-device validation. Browser acceptance recorded separately.

## Multi-group increment
User requests independent group CRUD, multi-group membership within one tenant and union authorization. Failure modes: lost legacy membership/grants; foreign-tenant group or device mutation; duplicate/invalid names; partial membership replacement; rename losing grants; deletion resurrecting grants; revoking one grant disconnecting a still-authorized session; removing last group/direct grant leaving terminal/file/proxy open; restart restoring deleted membership. E2E extends the existing real-server socket oracle before implementation. Test deployment only on 204 with 156; stopped-database plus binary backup is required for rollback.

## Device lifecycle increment
Failures to expose before implementation: wrong/absent confirmation rotates a token; old token reconnects after reset; cross-tenant deletion; deletion leaves live sessions or enables reconnect; tombstoned devices reappear in inventory/groups/grants; client -D confused with administrator name or lost across restart; search omits registration -I/-D. Preserve deleted identifiers and audit history; no implicit restore. Installer must retain TLS and existing unrelated rtty services.

## Download-window failure modes
Test before implementation: unsupported negotiation; more than16 unacknowledged chunks; stale ACK/cancel affecting subsequent file; cancellation aborting the terminal or leaving helper blocked; disconnected download leaving the next session unable to transfer; partial files accepted as success. Preserve ordinary transfer integrity and the existing device protocol. Evidence distinguishes local simulated transport from deployed174 file checks.
