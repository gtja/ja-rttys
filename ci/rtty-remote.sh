#!/usr/bin/env bash
set -euo pipefail
set +x
cd "$(dirname "$0")/.."
: "${DEPLOY_USER:?Bind public-aliyun-server-1 username}"
: "${DEPLOY_PASSWORD:?Bind public-aliyun-server-1 password}"
action=${1:-check}
[[ "$action" == check || "$action" == deploy ]]
[[ "$DEPLOY_USER" =~ ^[a-zA-Z_][a-zA-Z0-9_-]*$ ]]
mkdir -p out
export SSHPASS="$DEPLOY_PASSWORD"
ssh_args=(-o StrictHostKeyChecking=yes -o UserKnownHostsFile="$PWD/ci/rtty-known-hosts" -o GlobalKnownHostsFile=/dev/null -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o PreferredAuthentications=password -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=1)
remote="$DEPLOY_USER@47.111.155.82"
sshpass -e ssh "${ssh_args[@]}" "$remote" 'python3 -' < ci/rtty-preflight.py > out/rtty-preflight.json
cat out/rtty-preflight.json
[[ "$action" == deploy ]] || exit 0
: "${SOURCE_COMMIT:?Set selected source SHA}"
: "${PACKAGE_VERSION:?Set package version}"
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$PACKAGE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{6}$ ]]
archive="ja-rttys-${PACKAGE_VERSION}-linux-amd64.tar.gz"
read -r checksum _ < "out/release/amd64/$archive.sha256"
[[ "$checksum" =~ ^[0-9a-f]{64}$ ]]
(cd out/release/amd64 && sha256sum -c "$archive.sha256")
# Reject schema changes before uploading or stopping the service.
python3 - <<'PY'
import json
from pathlib import Path
live=json.loads(Path('out/rtty-preflight.json').read_text())
package=json.loads(Path('out/release/amd64/manifest.json').read_text())
assert live['deploy_configured'], 'Provision /etc/ja-rttys/ci-deploy.json first; see docs/native-update.md'
assert live['identity_schema']==package['identity_schema'], 'One-time schema migration required; service has not been stopped'
PY
temp=$(sshpass -e ssh "${ssh_args[@]}" "$remote" 'umask 077; mktemp -d /tmp/ja-rttys-ci.XXXXXXXX')
[[ "$temp" =~ ^/tmp/ja-rttys-ci\.[A-Za-z0-9]+$ ]]
cleanup() { sshpass -e ssh "${ssh_args[@]}" "$remote" "rm -rf '$temp'" || true; }
trap cleanup EXIT
sshpass -e scp "${ssh_args[@]}" "out/release/amd64/$archive" "$remote:$temp/package.tar.gz"
sshpass -e scp "${ssh_args[@]}" scripts/ci-deploy.py "$remote:$temp/ci-deploy.py"
# Preserve the remote helper exit code as well as its failure/rollback receipt.
sshpass -e ssh "${ssh_args[@]}" "$remote" \
    "python3 '$temp/ci-deploy.py' --config /etc/ja-rttys/ci-deploy.json --package '$temp/package.tar.gz' --sha256 '$checksum' --commit '$SOURCE_COMMIT' --backup-db > '$temp/result.json'; status=\$?; cat '$temp/result.json'; exit \$status" \
    > out/rtty-deployment.json
