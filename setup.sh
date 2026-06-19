#!/bin/bash
set -euo pipefail

echo "==> Initializing git submodules (recursive for nsjail -> kafel)..."
git submodule update --init --recursive

echo ""
echo "==> Building Docker images..."
make build

echo ""
echo "==> Starting server..."
make run
