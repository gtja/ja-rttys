#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source ci/base-images.sh
: "${SOURCE_COMMIT:?Set SOURCE_COMMIT to the Jenkins-selected SHA}"
: "${CI_RUN_ID:?Set a unique CI_RUN_ID to rerun verification}"
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
mkdir -p out/verify
export DOCKER_BUILDKIT=1
bash ci/ensure-builder.sh
docker buildx build --builder "$RTTYS_BUILDER" --target verification --output type=local,dest=out/verify --build-arg "CI_RUN_ID=$CI_RUN_ID" \
    --build-arg "NODE_BUILD_IMAGE=$NODE_BUILD_IMAGE" \
    --build-arg "GO_BUILD_IMAGE=$GO_BUILD_IMAGE" \
    --build-arg "RUNTIME_IMAGE=$RUNTIME_IMAGE" \
    . 2>&1 | tee out/verify/docker-build.log
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
