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

echo "==> Building goboxd image (LANGS=${LANGS})"
docker compose build --build-arg LANGS="${LANGS}"

echo ""
echo "==> Done."
echo "    Start the server with: LANGS=${LANGS} docker compose up -d"
