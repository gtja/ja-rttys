#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${SOURCE_COMMIT:?Set SOURCE_COMMIT to the Jenkins-selected SHA}"
: "${CI_RUN_ID:?Set a unique CI_RUN_ID to rerun verification}"
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
mkdir -p out/verify
run_hash=$(python3 -c 'import hashlib,os; print(hashlib.sha256(os.environ["CI_RUN_ID"].encode()).hexdigest()[:16])')
image="ja-rttys-verify:$run_hash"
container=''
cleanup() {
    [[ -z "$container" ]] || docker rm -fv "$container" >/dev/null
    docker image rm "$image" >/dev/null 2>&1 || true
}
trap cleanup EXIT
export DOCKER_BUILDKIT=1
docker build --target verify --build-arg "CI_RUN_ID=$CI_RUN_ID" -t "$image" . 2>&1 | tee out/verify/docker-build.log
container=$(docker create "$image" /bin/true)
docker cp "$container:/ci/." out/verify/
python3 - <<'PY'
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location('ci_package', 'scripts/ci-package.py')
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)
actual = json.loads(Path('out/verify/e2e.json').read_text())['identity_schema']
if actual != package.IDENTITY_SCHEMA:
    raise SystemExit('Runtime identity schema v{} differs from package schema v{}'.format(actual, package.IDENTITY_SCHEMA))
print('Runtime and package identity schema match: v{}'.format(actual))
PY
