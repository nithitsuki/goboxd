#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing system dependencies..."
pkg_update

pkg_install \
    ca-certificates curl wget unzip \
    build-essential gcc g++ \
    libprotobuf32 libprotobuf-c1 libnl-3-200 libnl-route-3-200

echo "System dependencies installed successfully."
