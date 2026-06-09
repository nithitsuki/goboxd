#!/bin/bash
set -euo pipefail

echo "==> Initializing git submodules..."
git submodule update --init

echo ""
echo "==> Building Docker images..."
make build

echo ""
echo "==> Starting server..."
make run
