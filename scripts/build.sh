#!/bin/bash
# Build the goboxd Docker image.
#
# Usage:
#   ./scripts/build.sh              # install all languages
#   ./scripts/build.sh py3,c,swift  # install only these languages
#   LANGS=py3,c ./scripts/build.sh  # same, via environment
#
# Downloads and extracted toolchains are cached in BuildKit cache mounts
# (see the Dockerfile), so rebuilding a language layer does not re-download
# the compilers. `docker builder prune` clears the cache.
set -euo pipefail

LANGS="${LANGS:-${1:-all}}"

# Use a docker-container builder: its isolated BuildKit cache store keeps
# cache mounts (apt archives, toolchains) across builds. The builder is
# passed explicitly with --builder, so the build uses it regardless of the
# CLI default builder. Idempotent: create only when missing.
if ! docker buildx inspect goboxd-builder >/dev/null 2>&1; then
    docker buildx create --name goboxd-builder --driver docker-container --bootstrap
fi

echo "==> Building goboxd image (LANGS=${LANGS})"
COMMIT="$(git rev-parse --short=7 HEAD 2>/dev/null || echo dev)"
docker compose build --builder goboxd-builder \
    --build-arg LANGS="${LANGS}" \
    --build-arg COMMIT="${COMMIT}" \
    --build-arg VERSION="${VERSION:-0.1.0}"

echo ""
echo "==> Done."
echo "    Start the server with: LANGS=${LANGS} docker compose up -d"
