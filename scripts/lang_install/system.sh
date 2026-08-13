#!/bin/bash
set -e

echo "Installing system dependencies..."
apt-get update || (sleep 10 && apt-get update) || (sleep 30 && apt-get update)

apt-get install -y --no-install-recommends \
    ca-certificates=20250419~deb12u1 curl=7.88.1-10+deb12u15 wget=1.21.3-1+deb12u1 unzip=6.0-28 build-essential=12.9 \
    gcc=4:12.2.0-3 g++=4:12.2.0-3 \
    libprotobuf32=3.21.12-3+deb12u1 libprotobuf-c1=1.4.1-1+b1 libnl-3-200=3.7.0-0.2+b1 libnl-route-3-200=3.7.0-0.2+b1

echo "System dependencies installed successfully."
