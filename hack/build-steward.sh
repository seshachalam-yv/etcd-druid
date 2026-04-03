#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Gardener contributors
#
# SPDX-License-Identifier: Apache-2.0

# build-steward.sh — cross-compile etcd-steward for linux/arm64, build a
# distroless container image, and push it to the local KinD registry.
#
# The image is pushed to the local registry (localhost:5001) that is set up by
# make kind-up. This avoids the need for `kind load` and makes the image
# available to pods without any additional cluster-side configuration.
#
# Usage:
#   ./hack/build-steward.sh <etcd-steward-source-dir> [image-tag]
#
# Arguments:
#   etcd-steward-source-dir   Path to the etcd-steward repository (required)
#   image-tag                 Docker image tag (default: local)
#
# Environment:
#   LOCAL_REGISTRY   Local registry host:port (default: localhost:5001)
#   KIND_CLUSTER     KinD cluster name to verify existence (default: etcd-druid-e2e)
#
# Example:
#   ./hack/build-steward.sh ~/go/src/github.com/gardener/etcd-steward
#   ./hack/build-steward.sh ~/go/src/github.com/gardener/etcd-steward local

set -o errexit
set -o nounset
set -o pipefail

STEWARD_DIR="${1:-}"
IMAGE_TAG="${2:-local}"
LOCAL_REGISTRY="${LOCAL_REGISTRY:-localhost:5001}"
KIND_CLUSTER="${KIND_CLUSTER:-etcd-druid-e2e}"
BUILD_DIR="$(mktemp -d)"

trap 'rm -rf "${BUILD_DIR}"' EXIT

function usage() {
  echo "Usage: $0 <etcd-steward-source-dir> [image-tag]"
  echo "  etcd-steward-source-dir   path to etcd-steward repository (required)"
  echo "  image-tag                 docker image tag (default: local)"
  exit 1
}

function check_prereqs() {
  if [ -z "${STEWARD_DIR}" ]; then
    echo "ERROR: etcd-steward source directory is required"
    usage
  fi
  if [ ! -d "${STEWARD_DIR}" ]; then
    echo "ERROR: directory not found: ${STEWARD_DIR}"
    exit 1
  fi
  for cmd in go docker kind; do
    if ! command -v "${cmd}" &>/dev/null; then
      echo "ERROR: ${cmd} is not installed"
      exit 1
    fi
  done
  if ! kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER}$"; then
    echo "ERROR: KinD cluster '${KIND_CLUSTER}' not found"
    echo "  Run 'make kind-up' first, or set KIND_CLUSTER env var"
    exit 1
  fi
}

function build_binary() {
  echo "Building etcd-steward binary for linux/arm64..."
  mkdir -p "${BUILD_DIR}/bin"
  (
    cd "${STEWARD_DIR}"
    GOOS=linux GOARCH=arm64 go build -o "${BUILD_DIR}/bin/etcd-steward" ./cmd/etcd-steward/
  )
  echo "Binary built: ${BUILD_DIR}/bin/etcd-steward"
}

function build_and_push_image() {
  local full_image="${LOCAL_REGISTRY}/etcd-steward:${IMAGE_TAG}"
  echo "Building docker image ${full_image}..."
  cat > "${BUILD_DIR}/Dockerfile" <<'EOF'
FROM gcr.io/distroless/static-debian11:nonroot
WORKDIR /
COPY bin/etcd-steward /etcd-steward
ENTRYPOINT ["/etcd-steward"]
EOF
  docker build --platform linux/arm64 -t "${full_image}" "${BUILD_DIR}"
  echo "Pushing ${full_image} to local registry..."
  docker push "${full_image}"
  echo "Image available at ${full_image}"
}

function main() {
  check_prereqs
  build_binary
  build_and_push_image
  echo ""
  echo "Done. etcd-steward:${IMAGE_TAG} is available in the local registry."
  echo "Deploy etcd-druid with: make deploy-steward-dev STEWARD_DIR=${STEWARD_DIR}"
}

main "$@"
