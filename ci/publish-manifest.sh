#!/usr/bin/env bash
# Publish the canonical version only after both architecture images are pushed.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${PACKAGE_VERSION:?Set PACKAGE_VERSION}"
: "${SOURCE_COMMIT:?Set SOURCE_COMMIT}"
: "${IMAGE_REGISTRY:?Set IMAGE_REGISTRY}"
image="$IMAGE_REGISTRY/ja-rttys:$PACKAGE_VERSION"
export IMAGE_NAME="$image"
python3 - <<'PY'
import json, os
from pathlib import Path
for arch in ('amd64', 'arm64'):
    metadata = json.loads(Path(f'out/release/{arch}/image.json').read_text())
    assert metadata['pushed'] is True
    assert metadata['version'] == os.environ['PACKAGE_VERSION']
    assert metadata['source_commit'] == os.environ['SOURCE_COMMIT']
    assert metadata['architecture'] == arch
    assert metadata['image'] == os.environ['IMAGE_NAME'] + '-' + arch
PY
docker manifest create --insecure --amend "$image" "$image-amd64" "$image-arm64"
digest=$(docker manifest push --insecure --purge "$image" | tail -n 1)
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]
export IMAGE_DIGEST="$digest"
docker manifest inspect --insecure "$image" > out/release/manifest-list.json
python3 - <<'PY'
import json, os
from pathlib import Path
index = json.loads(Path('out/release/manifest-list.json').read_text())
platforms = {(m['platform']['os'], m['platform']['architecture']) for m in index['manifests']}
assert platforms == {('linux', 'amd64'), ('linux', 'arm64')}, platforms
expected = {json.loads(Path(f'out/release/{arch}/image.json').read_text())['digest'].split('@')[1]
            for arch in ('amd64', 'arm64')}
assert {m['digest'] for m in index['manifests']} == expected
metadata = dict(version=os.environ['PACKAGE_VERSION'], source_commit=os.environ['SOURCE_COMMIT'],
                image=os.environ['IMAGE_NAME'], digest=os.environ['IMAGE_DIGEST'], pushed=True,
                platforms=['linux/amd64', 'linux/arm64'])
Path('out/release/image.json').write_text(json.dumps(metadata, indent=2) + '\n')
PY
