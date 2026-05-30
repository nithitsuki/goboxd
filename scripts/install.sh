#!/bin/bash
set -e

echo "Installing system dependencies..."
apt-get update || (sleep 10 && apt-get update) || (sleep 30 && apt-get update)

apt-get install -y --no-install-recommends \
    ca-certificates curl wget unzip build-essential \
    gcc g++ \
    libprotobuf32 libprotobuf-c1 libnl-3-200 libnl-route-3-200

echo "Installing language toolchains..."
# Python 3 (interpreted)
apt-get install -y --no-install-recommends python3 python3-pip python3-dev

# Rust (bonus language)
apt-get install -y --no-install-recommends rustc cargo || true

# Go (bonus language)
apt-get install -y --no-install-recommends golang-go || true

# Verify
python3 --version && gcc --version && g++ --version
which rustc 2>/dev/null && rustc --version || echo "rustc not installed"
which go 2>/dev/null && go version || echo "go not installed"

apt-get clean
rm -rf /var/lib/apt/lists/*
