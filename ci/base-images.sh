#!/usr/bin/env bash
# Shared by local builds and Jenkins. Refresh these images with mirror-base-images.sh.
BASE_IMAGE_REGISTRY=${BASE_IMAGE_REGISTRY:-registry.jingan.com:32008/ja}
NODE_BUILD_IMAGE=${NODE_BUILD_IMAGE:-$BASE_IMAGE_REGISTRY/node:22-bookworm-slim}
GO_BUILD_IMAGE=${GO_BUILD_IMAGE:-$BASE_IMAGE_REGISTRY/ja-rttys-build:go1.27.4-vuln1.9.0-v1}
RUNTIME_IMAGE=${RUNTIME_IMAGE:-$BASE_IMAGE_REGISTRY/alpine:3.22}
RTTYS_BUILDER=${RTTYS_BUILDER:-ja-rttys-ci}
RTTYS_BUILDKIT_IMAGE=${RTTYS_BUILDKIT_IMAGE:-$BASE_IMAGE_REGISTRY/buildkit:v0.30.0}
# Keep the builder registry independent from Jenkins' temporary login directory.
BUILDX_CONFIG=${BUILDX_CONFIG:-$HOME/.config/ja-rttys/buildx}
export BASE_IMAGE_REGISTRY NODE_BUILD_IMAGE GO_BUILD_IMAGE RUNTIME_IMAGE RTTYS_BUILDER RTTYS_BUILDKIT_IMAGE BUILDX_CONFIG
