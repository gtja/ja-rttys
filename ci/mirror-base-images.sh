#!/usr/bin/env bash
# Run manually on 105 after docker login. Never run this in ordinary branch builds.
set -euo pipefail
cd "$(dirname "$0")/.."
source ci/base-images.sh
[[ "$BASE_IMAGE_REGISTRY" =~ ^[a-zA-Z0-9.:/-]+$ && "$BASE_IMAGE_REGISTRY" != -* ]]
UPSTREAM_IMAGE_PREFIX=${UPSTREAM_IMAGE_PREFIX:-}
[[ -z "$UPSTREAM_IMAGE_PREFIX" || "$UPSTREAM_IMAGE_PREFIX" =~ ^[a-zA-Z0-9.:/-]+$ ]]

publish_manifest() {
    local image=$1
    docker manifest rm "$image" >/dev/null 2>&1 || true
    docker manifest create --insecure "$image" "$image-amd64" "$image-arm64"
    docker manifest push --insecure "$image"
}

source_images=(node:22-bookworm-slim golang:1.27.4-bookworm alpine:3.22)
if [[ ${MIRROR_ONLY_GO:-0} == 1 ]]; then source_images=(golang:1.27.4-bookworm); fi
for source_image in "${source_images[@]}"; do
    target="$BASE_IMAGE_REGISTRY/$source_image"
    upstream="$source_image"
    [[ -z "$UPSTREAM_IMAGE_PREFIX" ]] || upstream="$UPSTREAM_IMAGE_PREFIX/$source_image"
    # Resolve once, then copy both architectures from the same upstream index.
    docker pull --platform linux/amd64 "$upstream"
    digest=$(docker image inspect --format '{{join .RepoDigests "\n"}}' "$upstream" \
        | awk -v repo="${upstream%:*}@" 'index($0,repo)==1 { print; exit }')
    [[ "$digest" == *@sha256:* ]]
    for arch in amd64 arm64; do
        # Let the daemon resolve each platform from the pinned index, using its
        # existing proxy/network configuration just like the initial pull.
        platform_image="$digest"
        docker pull --platform "linux/$arch" "$platform_image"
        [[ "$(docker image inspect --format '{{.Architecture}}' "$platform_image")" == "$arch" ]]
        docker tag "$platform_image" "$target-$arch"
        docker push "$target-$arch"
    done
    publish_manifest "$target"
done

bash ci/ensure-builder.sh
for arch in amd64 arm64; do
    docker buildx build --builder "$RTTYS_BUILDER" --load \
        --platform "linux/$arch" -f ci/Dockerfile.build-base \
        --build-arg "GO_IMAGE=$BASE_IMAGE_REGISTRY/golang:1.27.4-bookworm" \
        -t "$GO_BUILD_IMAGE-$arch" ci
    [[ "$(docker image inspect --format '{{.Architecture}}' "$GO_BUILD_IMAGE-$arch")" == "$arch" ]]
    docker push "$GO_BUILD_IMAGE-$arch"
done
publish_manifest "$GO_BUILD_IMAGE"
