#!/bin/bash
set -e

echo "Installing system dependencies..."
apt-get update || (sleep 10 && apt-get update) || (sleep 30 && apt-get update)

apt-get install -y --no-install-recommends \
    ca-certificates curl wget unzip build-essential \
    gcc g++ \
    libprotobuf32 libprotobuf-c1 libnl-3-200 libnl-route-3-200

echo "System dependencies installed successfully."
