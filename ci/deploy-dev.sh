#!/usr/bin/env bash
# Upgrade only the separately provisioned dev instance on 105.
set -euo pipefail
set +x
cd "$(dirname "$0")/.."
: "${SOURCE_COMMIT:?Set the Jenkins-selected commit}"
: "${PACKAGE_VERSION:?Set the verified package version}"
: "${DEPLOY_USER:?Bind the ssh-105 username}"
: "${DEPLOY_PASSWORD:?Bind the ssh-105 password}"
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$PACKAGE_VERSION" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,110}$ ]]
archive="ja-rttys-${PACKAGE_VERSION}-linux-amd64.tar.gz"
read -r checksum _ < "out/release/amd64/${archive}.sha256"
[[ "$checksum" =~ ^[0-9a-f]{64}$ ]]
(cd out/release/amd64 && sha256sum -c "${archive}.sha256")

# Reuse the 105 agent's independently provisioned host keys, like the other dev jobs.
export SSHPASS="$DEPLOY_PASSWORD"
ssh_args=(-o StrictHostKeyChecking=yes -o ConnectTimeout=10)
remote="$DEPLOY_USER@172.21.30.105"
temp=$(sshpass -e ssh "${ssh_args[@]}" "$remote" 'umask 077; mktemp -d /tmp/ja-rttys-dev-ci.XXXXXXXX')
[[ "$temp" =~ ^/tmp/ja-rttys-dev-ci\.[A-Za-z0-9]+$ ]]
cleanup() {
    sshpass -e ssh "${ssh_args[@]}" "$remote" "rm -rf '$temp'" || true
}
trap cleanup EXIT
sshpass -e scp "${ssh_args[@]}" "out/release/amd64/$archive" "$remote:$temp/package.tar.gz"
sshpass -e scp "${ssh_args[@]}" scripts/ci-deploy.py "$remote:$temp/ci-deploy.py"
# The helper refuses missing services/config and returns a structured failure receipt.
sshpass -e ssh "${ssh_args[@]}" "$remote" \
    "python3 '$temp/ci-deploy.py' --config /etc/ja-rttys-dev/ci-deploy.json --package '$temp/package.tar.gz' --sha256 '$checksum' --commit '$SOURCE_COMMIT' > '$temp/result.json'; cat '$temp/result.json'" \
    > out/deployment.json
python3 -c 'import json; result=json.load(open("out/deployment.json")); assert result["status"] == "passed", result'
