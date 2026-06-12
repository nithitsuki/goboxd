#!/bin/bash
set -e
echo "Installing Go..."
apt-get install -y --no-install-recommends golang-go
echo "Go installation verified: $(go version)"
