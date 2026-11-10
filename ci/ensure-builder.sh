#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source ci/base-images.sh
[[ "$RTTYS_BUILDER" =~ ^[a-zA-Z][a-zA-Z0-9_.-]+$ ]]
if ! docker buildx inspect "$RTTYS_BUILDER" >/dev/null 2>&1; then
    docker buildx create --name "$RTTYS_BUILDER" --driver docker-container \
        --driver-opt "image=$RTTYS_BUILDKIT_IMAGE" --driver-opt network=host \
        --buildkitd-config ci/buildkitd.toml \
        || docker buildx inspect "$RTTYS_BUILDER" >/dev/null
fi
docker buildx inspect "$RTTYS_BUILDER" --bootstrap
