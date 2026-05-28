#!/bin/bash

set -e 

echo "Installing system dependencies..."
apt-get update
apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    unzip \
    build-essential \
    gcc \
    g++ \
    libprotobuf32 \
    libprotobuf-c1 \
    libnl-3-200 \
    libnl-route-3-200

echo "Running language installation scripts..."
SCRIPT_DIR="$(dirname "$0")"
for script in "$SCRIPT_DIR/lang_install"/*.sh; do
    if [ -f "$script" ]; then
        echo "Executing $script..."
        bash "$script"
    fi
done

apt-get clean
rm -rf /var/lib/apt/lists/*