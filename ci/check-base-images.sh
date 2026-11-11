#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source ci/base-images.sh
bash ci/ensure-builder.sh
for image in "$NODE_BUILD_IMAGE" "$GO_BUILD_IMAGE"; do
    docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image"
done
docker run --rm --network none "$NODE_BUILD_IMAGE" sh -ec 'node --version; npm --version'
docker run --rm --network none "$GO_BUILD_IMAGE" sh -ec \
    'go version; python3 --version; openssl version; govulncheck -version'
mkdir -p out/verify
python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess

images = {}
for key in ['NODE_BUILD_IMAGE', 'GO_BUILD_IMAGE']:
    name = os.environ[key]
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', name], text=True))[0]
    images[key] = dict(reference=name, image_id=info['Id'], architecture=info['Architecture'],
                       repo_digests=info.get('RepoDigests', []))
Path('out/verify/base-images.json').write_text(json.dumps(images, indent=2) + '\n')
PY
