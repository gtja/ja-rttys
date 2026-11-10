#!/usr/bin/env bash
# Build one architecture after CI verification. Registry authentication is supplied by Jenkins.
set -euo pipefail
cd "$(dirname "$0")"
source ci/base-images.sh
: "${SOURCE_COMMIT:?Set SOURCE_COMMIT to the Jenkins-selected SHA}"
: "${PACKAGE_VERSION:?Set a unique PACKAGE_VERSION}"
TARGET_ARCH=${TARGET_ARCH:-amd64}
IMAGE_REGISTRY=${IMAGE_REGISTRY:-registry.jingan.com:32008/ja}
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$PACKAGE_VERSION" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,110}$ ]]
[[ "$TARGET_ARCH" == amd64 || "$TARGET_ARCH" == arm64 ]]
[[ "$IMAGE_REGISTRY" =~ ^[a-zA-Z0-9.:/-]+$ && "$IMAGE_REGISTRY" != -* ]]
tag="$PACKAGE_VERSION-$TARGET_ARCH"
image="$IMAGE_REGISTRY/ja-rttys:$tag"
dir="out/release/$TARGET_ARCH"
mkdir -p "$dir"
container=''
trap '[[ -z "$container" ]] || docker rm -fv "$container" >/dev/null' EXIT
export DOCKER_BUILDKIT=1
bash ci/ensure-builder.sh
docker buildx build --builder "$RTTYS_BUILDER" --load --platform "linux/$TARGET_ARCH" --target runtime \
    --build-arg "NODE_BUILD_IMAGE=$NODE_BUILD_IMAGE" \
    --build-arg "GO_BUILD_IMAGE=$GO_BUILD_IMAGE" \
    --build-arg "RUNTIME_IMAGE=$RUNTIME_IMAGE" \
    --build-arg "SOURCE_COMMIT=$SOURCE_COMMIT" \
    --build-arg "PACKAGE_VERSION=$PACKAGE_VERSION" \
    --build-arg "BUILD_TIME=$(date -u +%FT%TZ)" -t "$image" .
[[ "$(docker image inspect --format '{{.Architecture}}' "$image")" == "$TARGET_ARCH" ]]
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" == "$SOURCE_COMMIT" ]]
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")" == "$PACKAGE_VERSION" ]]
container=$(docker create --platform "linux/$TARGET_ARCH" "$image")
docker cp "$container:/usr/bin/rttys" "$dir/rttys"
export TARGET_ARCH IMAGE_NAME="$image" RELEASE_DIR="$dir"
export IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$image")"
python3 scripts/ci-package.py
if [[ ${PUSH_IMAGE:-0} == 1 ]]; then
    docker push "$image"
    export IMAGE_DIGEST="$(docker image inspect --format '{{join .RepoDigests "\n"}}' "$image" | awk -v repo="${image%:*}@" 'index($0,repo)==1 { print; exit }')"
    [[ "$IMAGE_DIGEST" == *@sha256:* ]]
    python3 scripts/ci-package.py --published
fi
