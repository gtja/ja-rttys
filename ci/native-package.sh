#!/usr/bin/env bash
# Use cached toolchains to export a standalone executable, without a runtime image.
set -euo pipefail
cd "$(dirname "$0")/.."
source ci/base-images.sh
: "${SOURCE_COMMIT:?Set the selected source commit}"
: "${PACKAGE_VERSION:?Set the release version}"
TARGET_ARCH=${TARGET_ARCH:-amd64}
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$PACKAGE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{6}$ ]]
[[ "$TARGET_ARCH" == amd64 || "$TARGET_ARCH" == arm64 ]]
dir="out/release/$TARGET_ARCH"
mkdir -p "$dir"
bash ci/ensure-builder.sh
docker buildx build --builder "$RTTYS_BUILDER" --platform "linux/$TARGET_ARCH" --target native \
    --output "type=local,dest=$dir" \
    --build-arg "NODE_BUILD_IMAGE=$NODE_BUILD_IMAGE" \
    --build-arg "GO_BUILD_IMAGE=$GO_BUILD_IMAGE" \
    --build-arg "SOURCE_COMMIT=$SOURCE_COMMIT" \
    --build-arg "PACKAGE_VERSION=$PACKAGE_VERSION" \
    --build-arg "BUILD_TIME=$(date -u +%FT%TZ)" .
export TARGET_ARCH RELEASE_DIR="$dir"
python3 scripts/ci-package.py --native
(cd "$dir" && sha256sum rttys > rttys.sha256)
